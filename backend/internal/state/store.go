// Package state keeps the small amount of OpenCloud-side memory the
// cloud features need: what a person lets Synaplan read, the key used
// while they are away, and files this service wrote or is keeping in
// sync. Secrets are encrypted. The file is optional; without a path
// everything stays in memory.
package state

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// ReadScope is what a person lets Synaplan read.
type ReadScope struct {
	Mode    string   `json:"mode"`
	Folders []Folder `json:"folders"`
}

// Folder is one folder a person named.
type Folder struct {
	ID   string `json:"id"`
	Path string `json:"path"`
}

// User is one person's stored settings.
type User struct {
	ReadScope  ReadScope `json:"readScope"`
	CopyFolder string    `json:"copyFolder"`
	// BackgroundKey is set only in memory after decrypt. It is not written
	// as plaintext.
	BackgroundKey string `json:"-"`
	KeyCipher     []byte `json:"keyCipher,omitempty"`
}

// Synced is a searchable file this OpenCloud is keeping up to date.
type Synced struct {
	ResourceID string `json:"resourceId"`
	Etag       string `json:"etag"`
	Path       string `json:"path"`
	UserID     string `json:"userId"`
	GroupKey   string `json:"groupKey"`
}

// Watch is a watch started here, so reconciliation still knows the owner
// if the index call is briefly unavailable.
type Watch struct {
	ID         int    `json:"id"`
	TargetKind string `json:"targetKind"`
	TargetID   string `json:"targetId"`
	UserID     string `json:"userId"`
}

// OwnWrite is a file this service just wrote, so a watch does not upload it.
type OwnWrite struct {
	UserID     string `json:"userId"`
	ResourceID string `json:"resourceId"`
	At         int64  `json:"at"`
}

type disk struct {
	Users          map[string]User `json:"users"`
	Synced         []Synced        `json:"synced"`
	Watches        []Watch         `json:"watches"`
	Owns           []OwnWrite      `json:"owns"`
	InstanceID     string          `json:"instanceId,omitempty"`
	InstanceSecret []byte          `json:"instanceSecret,omitempty"`
}

// Store is safe for concurrent use.
type Store struct {
	mu   sync.Mutex
	path string
	aead cipher.AEAD
	disk disk
}

// Open loads path, or an empty memory store when path is empty.
// key encrypts away-keys. An empty key keeps those keys in memory only
// and refuses to write them to disk.
func Open(path, key string) (*Store, error) {
	s := &Store{
		path: path,
		disk: disk{Users: map[string]User{}},
	}
	if key != "" {
		sum := sha256.Sum256([]byte(key))
		block, err := aes.NewCipher(sum[:])
		if err != nil {
			return nil, err
		}
		aead, err := cipher.NewGCM(block)
		if err != nil {
			return nil, err
		}
		s.aead = aead
	}
	if path == "" {
		return s, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return s, nil
		}
		return nil, err
	}
	if len(raw) == 0 {
		return s, nil
	}
	if err := json.Unmarshal(raw, &s.disk); err != nil {
		return nil, err
	}
	if s.disk.Users == nil {
		s.disk.Users = map[string]User{}
	}
	for id, user := range s.disk.Users {
		if len(user.KeyCipher) > 0 && s.aead != nil {
			plain, err := s.decrypt(user.KeyCipher)
			if err == nil {
				user.BackgroundKey = string(plain)
				s.disk.Users[id] = user
			}
		}
	}
	return s, nil
}

// User returns a copy of the person's settings. The read scope defaults
// to "watched" when nothing was stored.
func (s *Store) User(id string) User {
	if s == nil {
		return User{ReadScope: ReadScope{Mode: "watched"}}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	user, ok := s.disk.Users[id]
	if !ok || user.ReadScope.Mode == "" {
		user.ReadScope.Mode = "watched"
	}
	return user
}

// PutUser stores the person's settings.
func (s *Store) PutUser(id string, user User) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if user.BackgroundKey != "" && s.aead != nil {
		sealed, err := s.encrypt([]byte(user.BackgroundKey))
		if err != nil {
			return err
		}
		user.KeyCipher = sealed
	}
	if user.BackgroundKey != "" && s.aead == nil {
		// Keep the key in memory. Do not write it.
		user.KeyCipher = nil
	}
	s.disk.Users[id] = user
	return s.flush()
}

// ForgetUser drops the person, including their away-key.
func (s *Store) ForgetUser(id string) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.disk.Users, id)
	return s.flush()
}

// RememberSynced records a searchable file to follow.
func (s *Store) RememberSynced(file Synced) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.disk.Synced[:0]
	for _, row := range s.disk.Synced {
		if row.ResourceID != file.ResourceID || row.UserID != file.UserID {
			next = append(next, row)
		}
	}
	s.disk.Synced = append(next, file)
	return s.flush()
}

// ForgetSynced drops a searchable file.
func (s *Store) ForgetSynced(userID, resourceID string) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.disk.Synced[:0]
	for _, row := range s.disk.Synced {
		if row.ResourceID == resourceID && row.UserID == userID {
			continue
		}
		next = append(next, row)
	}
	s.disk.Synced = next
	return s.flush()
}

// Synced returns the searchable files being kept up to date.
func (s *Store) Synced() []Synced {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Synced, len(s.disk.Synced))
	copy(out, s.disk.Synced)
	return out
}

// RememberWatch stores a watch this service started.
func (s *Store) RememberWatch(w Watch) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.disk.Watches[:0]
	for _, row := range s.disk.Watches {
		if row.ID != w.ID {
			next = append(next, row)
		}
	}
	s.disk.Watches = append(next, w)
	return s.flush()
}

// Watches returns the watches stored here.
func (s *Store) Watches() []Watch {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Watch, len(s.disk.Watches))
	copy(out, s.disk.Watches)
	return out
}

// RememberOwnWrite marks a file this service wrote, for ten minutes.
func (s *Store) RememberOwnWrite(userID, resourceID string, now int64) {
	if s == nil || resourceID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cutoff := now - int64((10 * time.Minute).Seconds())
	next := s.disk.Owns[:0]
	for _, row := range s.disk.Owns {
		if row.At >= cutoff {
			next = append(next, row)
		}
	}
	s.disk.Owns = append(next, OwnWrite{UserID: userID, ResourceID: resourceID, At: now})
	_ = s.flush()
}

// Instance returns the stored platform instance, when registration
// saved one. The secret is empty when it could not be decrypted.
func (s *Store) Instance() (string, string) {
	if s == nil {
		return "", ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	secret := ""
	if len(s.disk.InstanceSecret) > 0 && s.aead != nil {
		plain, err := s.decrypt(s.disk.InstanceSecret)
		if err == nil {
			secret = string(plain)
		}
	}
	return s.disk.InstanceID, secret
}

// SetInstance stores the platform instance. The secret is encrypted.
func (s *Store) SetInstance(id, secret string) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.disk.InstanceID = id
	s.disk.InstanceSecret = nil
	if secret != "" && s.aead != nil {
		sealed, err := s.encrypt([]byte(secret))
		if err != nil {
			return err
		}
		s.disk.InstanceSecret = sealed
	}
	return s.flush()
}

// IsOwnWrite reports whether this service wrote the file in the last ten minutes.
func (s *Store) IsOwnWrite(userID, resourceID string, now int64) bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cutoff := now - int64((10 * time.Minute).Seconds())
	for _, row := range s.disk.Owns {
		if row.UserID == userID && row.ResourceID == resourceID && row.At >= cutoff {
			return true
		}
	}
	return false
}

func (s *Store) encrypt(plain []byte) ([]byte, error) {
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return s.aead.Seal(nonce, nonce, plain, nil), nil
}

func (s *Store) decrypt(sealed []byte) ([]byte, error) {
	ns := s.aead.NonceSize()
	if len(sealed) < ns {
		return nil, errors.New("state: short ciphertext")
	}
	return s.aead.Open(nil, sealed[:ns], sealed[ns:], nil)
}

func (s *Store) flush() error {
	if s.path == "" {
		return nil
	}
	// Never persist a plaintext key. KeyCipher is the encrypted form.
	safe := s.disk
	safe.Users = map[string]User{}
	for id, user := range s.disk.Users {
		stored := user
		if s.aead == nil {
			stored.KeyCipher = nil
		}
		safe.Users[id] = stored
	}
	raw, err := json.Marshal(safe)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}
