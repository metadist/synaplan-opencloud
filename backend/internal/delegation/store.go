package delegation

import (
	"context"
	"crypto/tls"
	"encoding/base32"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// ErrUnreadable means a value is kept but cannot be read with the keys at
// hand: written under a key that was dropped or that this replica does not
// know yet, or not written by this service. It is never deleted for that
// alone, so a rotation rolling through the replicas loses nobody; allowing
// again replaces it. Any other error is an outage.
var ErrUnreadable = errors.New("delegation: the kept offline token cannot be read")

var errEmptyToken = errors.New("delegation: refusing to store an empty offline token")

// Entry is a person's offline token and the revision it was read at.
// Rewrap asks for the token to be written again as it is, because the value
// was sealed with a key that is no longer current; the caller does that
// where it already holds the person's lease.
type Entry struct {
	Token    string
	Revision uint64
	Rewrap   bool
}

// Store keeps one offline token per person where every replica of this
// service can read it. Revision is the generation of that one key. A write
// or delete names the revision it read and does nothing when another replica
// wrote in between, so two replicas cannot drop each other's tokens.
//
// OpenCloud's go-micro nats-js-kv plugin is not used here: its Write is an
// unconditional put. A rotating refresh token has to be replaced only when
// it is still the one that was refreshed.
type Store interface {
	// Get returns the person's entry. ok is false when none is kept. A
	// value that is kept but cannot be read returns its revision, ok, and
	// an error matching ErrUnreadable, so it can be replaced or removed.
	Get(ctx context.Context, subject string) (entry Entry, ok bool, err error)
	// Write stores token when the kept revision is still match. match 0
	// stores it only when the person is not kept yet. ok is false when
	// the revision moved.
	Write(ctx context.Context, subject, token string, match uint64) (revision uint64, ok bool, err error)
	// Delete removes the token when its revision is still match. ok is
	// false when the revision moved, and true when it was already gone.
	Delete(ctx context.Context, subject string, match uint64) (ok bool, err error)
	// Lease holds subject exclusively across every replica, waiting while
	// another holder has it. Work under the lease uses held, which ends
	// before the lease could lapse; release gives the lease back.
	Lease(ctx context.Context, subject string) (held context.Context, release func(), err error)
	// Subjects lists every person a token is kept for, readable or not.
	Subjects(ctx context.Context) ([]string, error)
	// Close ends the connection to the store, if there is one. It takes no
	// context: it is called once, when the service stops.
	Close() error
}

// NATSConfig is how this service reaches the JetStream key-value store
// OpenCloud already runs for its own services.
type NATSConfig struct {
	Nodes     []string
	Bucket    string
	Replicas  int
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
	bucket jetstream.KeyValueConfig

	mu sync.Mutex
	kv jetstream.KeyValue
}

// leaseTTL is how long NATS keeps a lease whose holder never gave it back,
// because its replica died.
const leaseTTL = 2 * time.Minute

// leaseHold is how long work under a lease may take. It ends well before
// leaseTTL, so the lease cannot lapse while a refresh is still running.
const leaseHold = leaseTTL / 2

// leasePoll is how often a waiting replica tries a held lease again.
const leasePoll = 100 * time.Millisecond

// NewNATS prepares the connection to OpenCloud's NATS. Like OpenCloud's own
// services it does not wait for NATS: the connection is retried in the
// background and the bucket is opened, or created, on first use, so this
// service starts while NATS is still starting. JetStream file storage is the
// default, which is what survives a restart; tokens have no TTL, because a
// token stays until the person disconnects. Leases expire on their own,
// which needs NATS 2.11 or later.
func NewNATS(cfg NATSConfig) (*NATSStore, error) {
	var nodes []string
	for _, n := range cfg.Nodes {
		if trimmed := strings.TrimSpace(n); trimmed != "" {
			nodes = append(nodes, trimmed)
		}
	}
	if len(nodes) == 0 {
		return nil, errors.New("delegation: nats-js-kv needs the address of OpenCloud's NATS")
	}
	bucket := strings.TrimSpace(cfg.Bucket)
	if bucket == "" {
		return nil, errors.New("delegation: nats-js-kv needs a bucket")
	}
	if cfg.Replicas < 0 {
		return nil, errors.New("delegation: nats-js-kv replicas must not be negative")
	}

	options := nats.GetDefaultOptions()
	options.Name = "synaplan-opencloud"
	options.Servers = nodes
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
			InsecureSkipVerify: cfg.Insecure,
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
	return &NATSStore{conn: conn, js: js, bucket: jetstream.KeyValueConfig{
		Bucket:         bucket,
		Description:    "Offline tokens for Synaplan work while a person is away",
		History:        1,
		Storage:        jetstream.FileStorage,
		Replicas:       cfg.Replicas,
		LimitMarkerTTL: leaseTTL,
	}}, nil
}

// open returns the bucket, creating or updating it the first time NATS
// answers. No lock is held while NATS is asked, so callers waiting for a
// NATS that is down do not queue behind each other.
func (s *NATSStore) open(ctx context.Context) (jetstream.KeyValue, error) {
	s.mu.Lock()
	kv := s.kv
	s.mu.Unlock()
	if kv != nil {
		return kv, nil
	}
	kv, err := s.js.CreateOrUpdateKeyValue(ctx, s.bucket)
	if err != nil {
		return nil, fmt.Errorf("delegation: opening the %s bucket: %w", s.bucket.Bucket, err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.kv == nil {
		s.kv = kv
	}
	return s.kv, nil
}

// Get reads the person's token.
func (s *NATSStore) Get(ctx context.Context, subject string) (Entry, bool, error) {
	kv, err := s.open(ctx)
	if err != nil {
		return Entry{}, false, err
	}
	entry, err := kv.Get(ctx, natsKey(subject))
	if errors.Is(err, jetstream.ErrKeyNotFound) || errors.Is(err, jetstream.ErrKeyDeleted) {
		return Entry{}, false, nil
	}
	if err != nil {
		return Entry{}, false, fmt.Errorf("delegation: reading an offline token: %w", err)
	}
	return Entry{Token: string(entry.Value()), Revision: entry.Revision()}, true, nil
}

// Write stores the token when match is still current.
func (s *NATSStore) Write(ctx context.Context, subject, token string, match uint64) (uint64, bool, error) {
	if token == "" {
		return 0, false, errEmptyToken
	}
	kv, err := s.open(ctx)
	if err != nil {
		return 0, false, err
	}
	key := natsKey(subject)
	var rev uint64
	if match == 0 {
		rev, err = kv.Create(ctx, key, []byte(token))
		if keyExists(err) {
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

// Delete removes the token when match is still current. Purge with a
// bounded marker TTL wipes the sealed value immediately and lets NATS
// drop the delete marker too, so a disconnected person leaves no trace
// on disk.
func (s *NATSStore) Delete(ctx context.Context, subject string, match uint64) (bool, error) {
	kv, err := s.open(ctx)
	if err != nil {
		return false, err
	}
	err = kv.Purge(ctx, natsKey(subject), jetstream.LastRevision(match), jetstream.PurgeTTL(leaseTTL))
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

// Subjects lists the people with a kept token. Lease keys and the
// markers a purge leaves behind are not people.
func (s *NATSStore) Subjects(ctx context.Context) ([]string, error) {
	kv, err := s.open(ctx)
	if err != nil {
		return nil, err
	}
	keys, err := kv.Keys(ctx)
	if errors.Is(err, jetstream.ErrNoKeysFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("delegation: listing the offline tokens: %w", err)
	}
	subjects := make([]string, 0, len(keys))
	for _, key := range keys {
		if subject, ok := subjectOf(key); ok {
			subjects = append(subjects, subject)
		}
	}
	return subjects, nil
}

// Lease creates the person's lease key with a TTL of its own. NATS removes
// a lease that was never given back, so no replica compares clocks, and a
// released lease purges its marker with a TTL so lease keys do not pile up.
func (s *NATSStore) Lease(ctx context.Context, subject string) (context.Context, func(), error) {
	kv, err := s.open(ctx)
	if err != nil {
		return nil, nil, err
	}
	key := "lease." + natsKey(subject)
	for {
		rev, err := kv.Create(ctx, key, []byte("held"), jetstream.KeyTTL(leaseTTL))
		if err == nil {
			held, cancel := context.WithTimeout(ctx, leaseHold)
			return held, func() {
				cancel()
				release, done := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
				defer done()
				_ = kv.Purge(release, key, jetstream.LastRevision(rev), jetstream.PurgeTTL(leaseTTL))
			}, nil
		}
		if !keyExists(err) {
			return nil, nil, fmt.Errorf("delegation: taking a lease: %w", err)
		}
		select {
		case <-ctx.Done():
			return nil, nil, fmt.Errorf("delegation: waiting for a lease: %w", ctx.Err())
		case <-time.After(leasePoll):
		}
	}
}

// keyExists reports a Create that lost to a key someone else holds. Over a
// deleted key, which every given-back lease leaves, nats.go does not map a
// replicated bucket's answer (10164) to ErrKeyExists, so the code is read.
func keyExists(err error) bool {
	var apiErr *jetstream.APIError
	if errors.As(err, &apiErr) {
		return apiErr.ErrorCode == jetstream.JSErrCodeStreamWrongLastSequence ||
			apiErr.ErrorCode == jetstream.JSErrCodeStreamWrongLastSequenceConstant
	}
	return errors.Is(err, jetstream.ErrKeyExists)
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

// subjectOf reverses natsKey. A key with a dot is a lease, not a person.
func subjectOf(key string) (string, bool) {
	if strings.Contains(key, ".") {
		return "", false
	}
	subject, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(key)
	if err != nil || len(subject) == 0 {
		return "", false
	}
	return string(subject), true
}
