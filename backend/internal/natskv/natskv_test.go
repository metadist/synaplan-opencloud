package natskv

import (
	"testing"

	"github.com/nats-io/nats.go/jetstream"
)

func TestNewNeedsAnAddressAndABucket(t *testing.T) {
	tests := []struct {
		name   string
		cfg    Config
		bucket jetstream.KeyValueConfig
	}{
		{"nothing", Config{}, jetstream.KeyValueConfig{}},
		{"no bucket", Config{Nodes: []string{"nats://127.0.0.1:1"}}, jetstream.KeyValueConfig{}},
		{"blank nodes", Config{Nodes: []string{"  ", ""}}, jetstream.KeyValueConfig{Bucket: "synaplan-settings"}},
		{"blank bucket", Config{Nodes: []string{"nats://127.0.0.1:1"}}, jetstream.KeyValueConfig{Bucket: "   "}},
		{"negative replicas", Config{Nodes: []string{"nats://127.0.0.1:1"}}, jetstream.KeyValueConfig{Bucket: "synaplan-settings", Replicas: -1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := New(tt.cfg, tt.bucket); err == nil {
				t.Fatalf("New accepted %+v %+v", tt.cfg, tt.bucket)
			}
		})
	}
}
