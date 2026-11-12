package delegation

import (
	"context"
	"crypto/tls"
	"encoding/base32"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
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
	Get(ctx context.Context, subject string) (token string, revision uint64, ok bool, err error)
	// Write stores token when the kept revision is still match. match 0
	// stores it only when the person is not kept yet. ok is false when
	// the revision moved.
	Write(ctx context.Context, subject, token string, match uint64) (revision uint64, ok bool, err error)
	// Delete removes the token when its revision is still match. ok is
	// false when the revision moved, and true when it was already gone.
	Delete(ctx context.Context, subject string, match uint64) (ok bool, err error)
	// Close ends the connection to the store, if there is one.
	Close() error
}

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
	conn   *nats.Conn
	js     jetstream.JetStream
	bucket string

	mu sync.Mutex
	kv jetstream.KeyValue
}

// NewNATS prepares the connection to OpenCloud's NATS. Like OpenCloud's own
// services it does not wait for NATS: the connection is retried in the
// background and the bucket is opened, or created, on first use, so this
// service starts while NATS is still starting. JetStream file storage is the
// default, which is what survives a restart; the bucket has no TTL, because
// a token stays until the person disconnects.
func NewNATS(cfg NATSConfig) (*NATSStore, error) {
	if len(cfg.Nodes) == 0 {
		return nil, errors.New("delegation: nats-js-kv needs the address of OpenCloud's NATS")
	}
	if cfg.Bucket == "" {
		return nil, errors.New("delegation: nats-js-kv needs a bucket")
	}

	options := nats.GetDefaultOptions()
	options.Name = "synaplan-opencloud"
	options.Servers = cfg.Nodes
	options.Timeout = 5 * time.Second
	options.MaxReconnect = -1
	options.ReconnectWait = time.Second
	options.RetryOnFailedConnect = true
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
	js, err := jetstream.New(conn)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("delegation: JetStream: %w", err)
	}
	return &NATSStore{conn: conn, js: js, bucket: cfg.Bucket}, nil
}

// open returns the bucket, opening or creating it the first time NATS
// answers.
func (s *NATSStore) open(ctx context.Context) (jetstream.KeyValue, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.kv != nil {
		return s.kv, nil
	}
	kv, err := s.js.KeyValue(ctx, s.bucket)
	if errors.Is(err, jetstream.ErrBucketNotFound) {
		kv, err = s.js.CreateKeyValue(ctx, jetstream.KeyValueConfig{
			Bucket:      s.bucket,
			Description: "Offline tokens for Synaplan work while a person is away",
			Storage:     jetstream.FileStorage,
		})
	}
	if err != nil {
		return nil, fmt.Errorf("delegation: opening the %s bucket: %w", s.bucket, err)
	}
	s.kv = kv
	return kv, nil
}

// Get reads the person's token.
func (s *NATSStore) Get(ctx context.Context, subject string) (string, uint64, bool, error) {
	kv, err := s.open(ctx)
	if err != nil {
		return "", 0, false, err
	}
	entry, err := kv.Get(ctx, natsKey(subject))
	if errors.Is(err, jetstream.ErrKeyNotFound) || errors.Is(err, jetstream.ErrKeyDeleted) {
		return "", 0, false, nil
	}
	if err != nil {
		return "", 0, false, fmt.Errorf("delegation: reading an offline token: %w", err)
	}
	return string(entry.Value()), entry.Revision(), true, nil
}

// Write stores the token when match is still current.
func (s *NATSStore) Write(ctx context.Context, subject, token string, match uint64) (uint64, bool, error) {
	if token == "" {
		return 0, false, errors.New("delegation: refusing to store an empty offline token")
	}
	kv, err := s.open(ctx)
	if err != nil {
		return 0, false, err
	}
	key := natsKey(subject)
	var rev uint64
	if match == 0 {
		rev, err = kv.Create(ctx, key, []byte(token))
		if errors.Is(err, jetstream.ErrKeyExists) {
			return 0, false, nil
		}
	} else {
		rev, err = kv.Update(ctx, key, []byte(token), match)
		if errors.Is(err, jetstream.ErrKeyRevisionMismatch) || errors.Is(err, jetstream.ErrKeyNotFound) {
			return 0, false, nil
		}
	}
	if err != nil {
		return 0, false, fmt.Errorf("delegation: writing an offline token: %w", err)
	}
	return rev, true, nil
}

// Delete removes the token when match is still current.
func (s *NATSStore) Delete(ctx context.Context, subject string, match uint64) (bool, error) {
	kv, err := s.open(ctx)
	if err != nil {
		return false, err
	}
	err = kv.Delete(ctx, natsKey(subject), jetstream.LastRevision(match))
	if errors.Is(err, jetstream.ErrKeyRevisionMismatch) {
		return false, nil
	}
	if errors.Is(err, jetstream.ErrKeyNotFound) || errors.Is(err, jetstream.ErrKeyDeleted) {
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
