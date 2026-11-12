package delegation

import (
	"crypto/tls"
	"encoding/base32"
	"errors"
	"fmt"
	"time"

	"github.com/nats-io/nats.go"
)

// Store keeps one offline token per person where every replica of this
// service can read it. Revision is the generation of that one key. A write
// or delete names the revision it read and does nothing when another replica
// wrote in between, so two replicas cannot drop each other's tokens.
//
// OpenCloud's go-micro nats-js-kv plugin is not used here: its Write is an
// unconditional put. A rotating refresh token has to be replaced only when
// it is still the one that was refreshed.
type Store interface {
	// Get returns the offline token and its revision. ok is false when
	// none is kept.
	Get(subject string) (token string, revision uint64, ok bool, err error)
	// Write stores token when the kept revision is still match. match 0
	// stores it only when the person is not kept yet. ok is false when
	// the revision moved.
	Write(subject, token string, match uint64) (revision uint64, ok bool, err error)
	// Delete removes the token when its revision is still match. ok is
	// false when the revision moved, and true when it was already gone.
	Delete(subject string, match uint64) (ok bool, err error)
	Close() error
}

const defaultBucket = "synaplan-delegation"

// NATSConfig is how this service reaches the JetStream key-value store
// OpenCloud already runs for its own services.
type NATSConfig struct {
	Nodes     []string
	Bucket    string
	Username  string
	Password  string
	EnableTLS bool
	// Insecure skips certificate verification. Ignored when RootCAs is set.
	Insecure bool
	RootCAs  string
}

// NATSStore is the OpenCloud NATS JetStream key-value bucket that keeps
// the offline tokens. The bucket lives on NATS's own disk, so it survives
// a restart of this service and of NATS.
type NATSStore struct {
	conn *nats.Conn
	kv   nats.KeyValue
}

// NewNATS connects to OpenCloud's NATS and opens the bucket, creating it
// on first use. JetStream file storage is the default, which is what
// survives a restart; the bucket has no TTL, because a token stays until
// the person disconnects.
func NewNATS(cfg NATSConfig) (*NATSStore, error) {
	if len(cfg.Nodes) == 0 {
		return nil, errors.New("delegation: nats-js-kv needs the address of OpenCloud's NATS")
	}
	if cfg.Bucket == "" {
		cfg.Bucket = defaultBucket
	}

	options := nats.GetDefaultOptions()
	options.Name = "synaplan-delegation"
	options.Servers = cfg.Nodes
	options.Timeout = 5 * time.Second
	options.MaxReconnect = -1
	options.ReconnectWait = time.Second
	if cfg.Username != "" {
		options.User = cfg.Username
		options.Password = cfg.Password
	}
	if cfg.EnableTLS {
		if cfg.RootCAs != "" {
			if err := nats.RootCAs(cfg.RootCAs)(&options); err != nil {
				return nil, fmt.Errorf("delegation: NATS root CA: %w", err)
			}
		} else if err := (nats.Secure(&tls.Config{
			MinVersion:         tls.VersionTLS12,
			InsecureSkipVerify: cfg.Insecure, //nolint:gosec // explicit opt-in, as OC_INSECURE is elsewhere
		}))(&options); err != nil {
			return nil, fmt.Errorf("delegation: NATS TLS: %w", err)
		}
	}

	conn, err := options.Connect()
	if err != nil {
		return nil, fmt.Errorf("delegation: connecting to OpenCloud's NATS: %w", err)
	}
	js, err := conn.JetStream()
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("delegation: JetStream: %w", err)
	}
	kv, err := js.KeyValue(cfg.Bucket)
	if errors.Is(err, nats.ErrBucketNotFound) {
		kv, err = js.CreateKeyValue(&nats.KeyValueConfig{
			Bucket:      cfg.Bucket,
			Description: "Offline tokens for Synaplan work while a person is away",
			Storage:     nats.FileStorage,
		})
	}
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("delegation: opening the %s bucket: %w", cfg.Bucket, err)
	}
	return &NATSStore{conn: conn, kv: kv}, nil
}

// Get reads the person's token.
func (s *NATSStore) Get(subject string) (string, uint64, bool, error) {
	entry, err := s.kv.Get(natsKey(subject))
	if errors.Is(err, nats.ErrKeyNotFound) || errors.Is(err, nats.ErrKeyDeleted) {
		return "", 0, false, nil
	}
	if err != nil {
		return "", 0, false, fmt.Errorf("delegation: reading an offline token: %w", err)
	}
	return string(entry.Value()), entry.Revision(), true, nil
}

// Write stores the token when match is still current.
func (s *NATSStore) Write(subject, token string, match uint64) (uint64, bool, error) {
	if token == "" {
		return 0, false, errors.New("delegation: refusing to store an empty offline token")
	}
	key := natsKey(subject)
	var (
		rev uint64
		err error
	)
	if match == 0 {
		rev, err = s.kv.Create(key, []byte(token))
		if errors.Is(err, nats.ErrKeyExists) {
			return 0, false, nil
		}
	} else {
		rev, err = s.kv.Update(key, []byte(token), match)
		if errors.Is(err, nats.ErrKeyRevisionMismatch) || errors.Is(err, nats.ErrKeyNotFound) {
			return 0, false, nil
		}
	}
	if err != nil {
		return 0, false, fmt.Errorf("delegation: writing an offline token: %w", err)
	}
	return rev, true, nil
}

// Delete removes the token when match is still current.
func (s *NATSStore) Delete(subject string, match uint64) (bool, error) {
	err := s.kv.Delete(natsKey(subject), nats.LastRevision(match))
	if errors.Is(err, nats.ErrKeyRevisionMismatch) {
		return false, nil
	}
	if errors.Is(err, nats.ErrKeyNotFound) || errors.Is(err, nats.ErrKeyDeleted) {
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("delegation: deleting an offline token: %w", err)
	}
	return true, nil
}

// Close disconnects from NATS.
func (s *NATSStore) Close() error {
	if s == nil || s.conn == nil {
		return nil
	}
	s.conn.Close()
	return nil
}

// natsKey encodes subject so it is one NATS key. A subject may contain
// characters NATS rejects in a key, such as a dot or an at-sign.
func natsKey(subject string) string {
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString([]byte(subject))
}
