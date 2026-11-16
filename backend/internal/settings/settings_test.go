package settings

import (
	"context"
	"testing"

	"github.com/metadist/synaplan-opencloud/internal/natskv"
)

func TestMemoryStore(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name   string
		writes []Values
		want   Values
		ok     bool
	}{
		{name: "nothing saved"},
		{name: "one save", writes: []Values{{DefaultAnswerLanguage: "de", UseUserLanguage: true}}, want: Values{DefaultAnswerLanguage: "de", UseUserLanguage: true}, ok: true},
		{name: "the last save wins", writes: []Values{{DefaultAnswerLanguage: "de", UseUserLanguage: true}, {DefaultAnswerLanguage: "fr"}}, want: Values{DefaultAnswerLanguage: "fr"}, ok: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := NewMemoryStore()
			for _, v := range tt.writes {
				if err := s.Put(ctx, v); err != nil {
					t.Fatalf("Put: %v", err)
				}
			}
			got, ok, err := s.Get(ctx)
			if err != nil || ok != tt.ok || got != tt.want {
				t.Fatalf("Get = %+v, %v, %v; want %+v, %v", got, ok, err, tt.want, tt.ok)
			}
		})
	}
}

func TestNewNATSNeedsAnAddressAndABucket(t *testing.T) {
	tests := []struct {
		name   string
		cfg    natskv.Config
		bucket string
	}{
		{"no address", natskv.Config{}, "synaplan-settings"},
		{"no bucket", natskv.Config{Nodes: []string{"nats://127.0.0.1:1"}}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := NewNATS(tt.cfg, tt.bucket, 1); err == nil {
				t.Fatal("NewNATS accepted it")
			}
		})
	}
}
