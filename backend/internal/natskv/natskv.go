// Package natskv reaches the NATS JetStream key-value store OpenCloud
// already runs for its own services. The offline tokens and the
// administrator's settings each keep a bucket there.
package natskv

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// Config is how this service reaches OpenCloud's NATS.
type Config struct {
	Nodes     []string
	Username  string
	Password  string
	EnableTLS bool
	// Insecure skips certificate verification. Ignored when RootCAs is set.
	Insecure bool
	RootCAs  string
}

// Bucket is one key-value bucket. Like OpenCloud's own services it does
// not wait for NATS: the connection is retried in the background and the
// bucket is opened, or created, on first use, so this service starts while
// NATS is still starting.
type Bucket struct {
	conn   *nats.Conn
	js     jetstream.JetStream
	config jetstream.KeyValueConfig

	mu sync.Mutex
	kv jetstream.KeyValue
}

// New prepares the connection for the bucket bucket.Bucket names.
func New(cfg Config, bucket jetstream.KeyValueConfig) (*Bucket, error) {
	var nodes []string
	for _, n := range cfg.Nodes {
		if trimmed := strings.TrimSpace(n); trimmed != "" {
			nodes = append(nodes, trimmed)
		}
	}
	if len(nodes) == 0 {
		return nil, errors.New("nats-js-kv needs the address of OpenCloud's NATS")
	}
	bucket.Bucket = strings.TrimSpace(bucket.Bucket)
	if bucket.Bucket == "" {
		return nil, errors.New("nats-js-kv needs a bucket")
	}
	if bucket.Replicas < 0 {
		return nil, errors.New("nats-js-kv replicas must not be negative")
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
				return nil, fmt.Errorf("NATS root CA: %w", err)
			}
		} else if err := (nats.Secure(&tls.Config{
			MinVersion:         tls.VersionTLS12,
			InsecureSkipVerify: cfg.Insecure,
		}))(&options); err != nil {
			return nil, fmt.Errorf("NATS TLS: %w", err)
		}
	}

	conn, err := options.Connect()
	if err != nil {
		return nil, fmt.Errorf("connecting to OpenCloud's NATS: %w", err)
	}
	js, err := jetstream.New(conn)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("JetStream: %w", err)
	}
	return &Bucket{conn: conn, js: js, config: bucket}, nil
}

// Open returns the bucket, creating or updating it the first time NATS
// answers. No lock is held while NATS is asked, so callers waiting for a
// NATS that is down do not queue behind each other.
func (b *Bucket) Open(ctx context.Context) (jetstream.KeyValue, error) {
	b.mu.Lock()
	kv := b.kv
	b.mu.Unlock()
	if kv != nil {
		return kv, nil
	}
	kv, err := b.js.CreateOrUpdateKeyValue(ctx, b.config)
	if err != nil {
		return nil, fmt.Errorf("opening the %s bucket: %w", b.config.Bucket, err)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.kv == nil {
		b.kv = kv
	}
	return b.kv, nil
}

// Close disconnects from NATS.
func (b *Bucket) Close() error {
	if b == nil || b.conn == nil {
		return nil
	}
	b.conn.Close()
	return nil
}
