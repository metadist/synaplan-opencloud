package delegation

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// Store keeps the offline tokens across restarts.
type Store interface {
	// Load returns the offline tokens by subject; none when nothing was kept.
	Load() (map[string]string, error)
	// Save replaces what is kept with offline.
	Save(offline map[string]string) error
}

// FileStore keeps the offline tokens in one file, sealed with AES-256-GCM
// under a key derived from this service's client secret. A copy of the file
// alone acts for nobody; with a new secret it no longer opens and people
// allow again.
type FileStore struct {
	path string
	aead cipher.AEAD
}

const storeKeyLabel = "synaplan-opencloud delegation store v1"

// NewFileStore returns a store at path, keyed by secret.
func NewFileStore(path, secret string) (*FileStore, error) {
	if path == "" || secret == "" {
		return nil, errors.New("delegation: a store needs a path and the client secret")
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(storeKeyLabel))
	block, err := aes.NewCipher(mac.Sum(nil))
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &FileStore{path: path, aead: aead}, nil
}

// Load opens the file; a missing file keeps nobody.
func (s *FileStore) Load() (map[string]string, error) {
	sealed, err := os.ReadFile(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("delegation: reading %s: %w", s.path, err)
	}
	size := s.aead.NonceSize()
	if len(sealed) < size {
		return nil, fmt.Errorf("delegation: %s is not a token store", s.path)
	}
	plain, err := s.aead.Open(nil, sealed[:size], sealed[size:], []byte(storeKeyLabel))
	if err != nil {
		return nil, fmt.Errorf("delegation: %s does not open with this client secret", s.path)
	}
	offline := map[string]string{}
	if err := json.Unmarshal(plain, &offline); err != nil {
		return nil, fmt.Errorf("delegation: %s: %w", s.path, err)
	}
	return offline, nil
}

// Save writes the file anew and renames it into place, so a crash leaves
// either the old or the new tokens, never half of them.
func (s *FileStore) Save(offline map[string]string) error {
	plain, err := json.Marshal(offline)
	if err != nil {
		return err
	}
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return err
	}
	sealed := s.aead.Seal(nonce, nonce, plain, []byte(storeKeyLabel))
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("delegation: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".delegation-*")
	if err != nil {
		return fmt.Errorf("delegation: %w", err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(sealed); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("delegation: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("delegation: %w", err)
	}
	if err := os.Rename(tmp.Name(), s.path); err != nil {
		return fmt.Errorf("delegation: %w", err)
	}
	return nil
}
