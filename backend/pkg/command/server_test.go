package command

import (
	"strings"
	"testing"

	"github.com/opencloud-eu/opencloud/pkg/log"

	"github.com/metadist/synaplan-opencloud/pkg/config"
)

func TestDelegationStore(t *testing.T) {
	cases := []struct {
		name    string
		store   config.PersistentStore
		wantErr string
	}{
		{name: "memory", store: config.PersistentStore{Store: "memory"}},
		{name: "nats-js-kv without nodes", store: config.PersistentStore{Store: "nats-js-kv", Database: "synaplan-delegation"}, wantErr: "address"},
		{name: "nats-js-kv without a bucket", store: config.PersistentStore{Store: "nats-js-kv", Nodes: []string{"127.0.0.1:1"}}, wantErr: "bucket"},
		{name: "nats-js-kv with negative replicas", store: config.PersistentStore{Store: "nats-js-kv", Nodes: []string{"127.0.0.1:1"}, Database: "synaplan-delegation", Replicas: -1}, wantErr: "replicas"},
		{name: "a store OpenCloud has but this service does not", store: config.PersistentStore{Store: "redis-sentinel"}, wantErr: "OC_PERSISTENT_STORE"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store, err := delegationStore(&config.Config{Store: tc.store}, log.NopLogger())
			if tc.wantErr == "" {
				if err != nil {
					t.Fatal(err)
				}
				_ = store.Close()
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err %v, want one naming %q", err, tc.wantErr)
			}
		})
	}
}
