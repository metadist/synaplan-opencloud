package delegation

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// KeyWrapper encrypts and decrypts the data key of one stored value. The
// local implementation derives its keys from secrets in the configuration;
// a key held in an HSM or a KMS fits behind the same interface without
// changing the stored format.
type KeyWrapper interface {
	// Wrap encrypts dek with the current key and names it.
	Wrap(ctx context.Context, dek, aad []byte) (keyID string, wrapped []byte, err error)
	// Unwrap decrypts dek with the key named keyID. current is false when
	// that key is no longer the one Wrap uses. An error matching
	// ErrUnreadable means the value cannot be decrypted with the keys at
	// hand; any other error is an outage and is reported as such.
	Unwrap(ctx context.Context, keyID string, wrapped, aad []byte) (dek []byte, current bool, err error)
}

// SealedStore encrypts every token before it reaches the store underneath,
// so the store, its disk and its backups hold nothing usable. Each value
// gets its own data key, wrapped by a KeyWrapper (envelope encryption), and
// both layers are bound to the person, so a value copied to another person's
// key does not decrypt. Revisions and leases pass through. A value read
// under a key that is no longer current comes back with Rewrap set, so the
// caller writes it again, sealed with the current key, under its lease.
type SealedStore struct {
	inner Store
	keys  KeyWrapper
}

const sealedVersion = "v1"

// NewSealedStore seals the tokens of inner with keys.
func NewSealedStore(inner Store, keys KeyWrapper) *SealedStore {
	return &SealedStore{inner: inner, keys: keys}
}

// Get reads and decrypts the person's token.
func (s *SealedStore) Get(ctx context.Context, subject string) (Entry, bool, error) {
	kept, ok, err := s.inner.Get(ctx, subject)
	if err != nil || !ok {
		return kept, ok, err
	}
	token, current, err := s.unseal(ctx, subject, kept.Token)
	if errors.Is(err, ErrUnreadable) {
		return Entry{Revision: kept.Revision}, true, err
	}
	if err != nil {
		return Entry{}, false, err
	}
	return Entry{Token: token, Revision: kept.Revision, Rewrap: !current}, true, nil
}

// Write encrypts the token and stores it when match is still current.
func (s *SealedStore) Write(ctx context.Context, subject, token string, match uint64) (uint64, bool, error) {
	if token == "" {
		return 0, false, errEmptyToken
	}
	value, err := s.seal(ctx, subject, token)
	if err != nil {
		return 0, false, err
	}
	return s.inner.Write(ctx, subject, value, match)
}

// Delete removes the token when match is still current.
func (s *SealedStore) Delete(ctx context.Context, subject string, match uint64) (bool, error) {
	return s.inner.Delete(ctx, subject, match)
}

// Lease takes the lease of the store underneath.
func (s *SealedStore) Lease(ctx context.Context, subject string) (context.Context, func(), error) {
	return s.inner.Lease(ctx, subject)
}

// Close closes the store underneath.
func (s *SealedStore) Close() error { return s.inner.Close() }

// sealedAAD binds a value to its format version and to the person, so a
// value moved to another person's key, or read as another format, does not
// decrypt.
func sealedAAD(subject string) []byte {
	return []byte("synaplan-opencloud/delegation/" + sealedVersion + "\x00" + subject)
}

// seal is "v1.<key id>.<wrapped data key>.<nonce and ciphertext>".
func (s *SealedStore) seal(ctx context.Context, subject, token string) (string, error) {
	aad := sealedAAD(subject)
	dek := make([]byte, 32)
	if _, err := rand.Read(dek); err != nil {
		return "", fmt.Errorf("delegation: data key: %w", err)
	}
	keyID, wrapped, err := s.keys.Wrap(ctx, dek, aad)
	if err != nil {
		return "", fmt.Errorf("delegation: wrapping the data key: %w", err)
	}
	sealed, err := gcmSeal(dek, []byte(token), aad)
	if err != nil {
		return "", err
	}
	enc := base64.RawURLEncoding
	return strings.Join([]string{sealedVersion, keyID, enc.EncodeToString(wrapped), enc.EncodeToString(sealed)}, "."), nil
}

// unseal decrypts value for subject. current reports whether it was sealed
// with the key Wrap uses now.
func (s *SealedStore) unseal(ctx context.Context, subject, value string) (string, bool, error) {
	parts := strings.Split(value, ".")
	if len(parts) != 4 || parts[0] != sealedVersion || parts[1] == "" || len(parts[1]) > 64 {
		return "", false, fmt.Errorf("%w: not a sealed value", ErrUnreadable)
	}
	enc := base64.RawURLEncoding
	wrapped, err := enc.DecodeString(parts[2])
	if err != nil {
		return "", false, fmt.Errorf("%w: wrapped data key: %w", ErrUnreadable, err)
	}
	sealed, err := enc.DecodeString(parts[3])
	if err != nil {
		return "", false, fmt.Errorf("%w: sealed token: %w", ErrUnreadable, err)
	}
	aad := sealedAAD(subject)
	dek, current, err := s.keys.Unwrap(ctx, parts[1], wrapped, aad)
	if err != nil {
		return "", false, err
	}
	token, err := gcmOpen(dek, sealed, aad)
	if err != nil {
		return "", false, err
	}
	return string(token), current, nil
}

// LocalKeys wraps data keys with AES-256-GCM under keys derived from
// secrets with HKDF-SHA256. The first secret wraps new values; the others
// only unwrap, so a secret can be rotated while older values are still read.
type LocalKeys struct {
	current string
	keys    map[string][]byte
}

// NewLocalKeys derives one key per secret. The first secret is the current
// one.
func NewLocalKeys(secrets ...string) (*LocalKeys, error) {
	l := &LocalKeys{keys: map[string][]byte{}}
	for _, secret := range secrets {
		if secret == "" {
			continue
		}
		key, err := hkdf.Key(sha256.New, []byte(secret), nil, "synaplan-opencloud delegation store", 32)
		if err != nil {
			return nil, fmt.Errorf("delegation: deriving a key: %w", err)
		}
		id := sha256.Sum256(key)
		keyID := hex.EncodeToString(id[:4])
		if l.current == "" {
			l.current = keyID
		}
		l.keys[keyID] = key
	}
	if l.current == "" {
		return nil, errors.New("delegation: no secret to derive the store key from")
	}
	return l, nil
}

// Wrap encrypts dek with the current key.
func (l *LocalKeys) Wrap(_ context.Context, dek, aad []byte) (string, []byte, error) {
	wrapped, err := gcmSeal(l.keys[l.current], dek, aad)
	return l.current, wrapped, err
}

// Unwrap decrypts dek with the key named keyID.
func (l *LocalKeys) Unwrap(_ context.Context, keyID string, wrapped, aad []byte) ([]byte, bool, error) {
	key, ok := l.keys[keyID]
	if !ok {
		return nil, false, fmt.Errorf("%w: no key %s", ErrUnreadable, keyID)
	}
	dek, err := gcmOpen(key, wrapped, aad)
	return dek, keyID == l.current, err
}

func gcmSeal(key, plaintext, aad []byte) ([]byte, error) {
	aead, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("delegation: nonce: %w", err)
	}
	return aead.Seal(nonce, nonce, plaintext, aad), nil
}

func gcmOpen(key, sealed, aad []byte) ([]byte, error) {
	aead, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	if len(sealed) < aead.NonceSize() {
		return nil, fmt.Errorf("%w: sealed value too short", ErrUnreadable)
	}
	plain, err := aead.Open(nil, sealed[:aead.NonceSize()], sealed[aead.NonceSize():], aad)
	if err != nil {
		return nil, fmt.Errorf("%w: sealed value does not decrypt", ErrUnreadable)
	}
	return plain, nil
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("delegation: cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("delegation: GCM: %w", err)
	}
	return aead, nil
}
