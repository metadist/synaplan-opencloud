package parser

import (
	"strings"
	"testing"

	"github.com/metadist/synaplan-opencloud/pkg/config"
	"github.com/metadist/synaplan-opencloud/pkg/config/defaults"
)

// fullOIDCConfig returns a config with all four OIDC token-exchange
// fields populated. Helpers below clear specific fields to exercise
// the validator's branches.
func fullOIDCConfig() *config.Config {
	return &config.Config{
		SynaplanURL:          "http://synaplan.example",
		OIDCTokenEndpoint:    "https://kc.example/realms/x/protocol/openid-connect/token",
		OIDCExchangeClientID: "synaplan-opencloud",
		OIDCExchangeSecret:   "secret",
		OIDCTargetAudience:   "synaplan-app",
	}
}

func TestValidate_OIDCOnly(t *testing.T) {
	if err := Validate(fullOIDCConfig()); err != nil {
		t.Errorf("OIDC-only config should validate, got: %v", err)
	}
}

func TestValidate_APIKeyOnly(t *testing.T) {
	cfg := &config.Config{
		SynaplanURL:    "http://synaplan.example",
		SynaplanAPIKey: "sk_abc1234567890",
	}
	if err := Validate(cfg); err != nil {
		t.Errorf("API-key-only config should validate, got: %v", err)
	}
}

func TestValidate_BothModes(t *testing.T) {
	// Both configured is allowed — server.go gives API key precedence.
	cfg := fullOIDCConfig()
	cfg.SynaplanAPIKey = "sk_abc1234567890"
	if err := Validate(cfg); err != nil {
		t.Errorf("both modes should validate (API key wins), got: %v", err)
	}
}

func TestValidate_NeitherMode(t *testing.T) {
	// Empty config — no auth mode configured. Must fail closed.
	cfg := &config.Config{SynaplanURL: "http://synaplan.example"}
	err := Validate(cfg)
	if err == nil {
		t.Fatal("empty auth config should fail validation")
	}
	if !strings.Contains(err.Error(), "no auth mode configured") {
		t.Errorf("error should mention 'no auth mode configured', got: %v", err)
	}
}

func TestValidate_PartialOIDC(t *testing.T) {
	// One OIDC field missing — that's not a complete token-exchange
	// setup, and there's no API key fallback. Must fail.
	cfg := fullOIDCConfig()
	cfg.OIDCExchangeSecret = ""
	if err := Validate(cfg); err == nil {
		t.Error("partial OIDC config (missing client secret) should fail validation")
	}
}

func TestValidate_APIKeyBadFormat(t *testing.T) {
	cfg := &config.Config{
		SynaplanURL:    "http://synaplan.example",
		SynaplanAPIKey: "not-a-synaplan-key",
	}
	err := Validate(cfg)
	if err == nil {
		t.Fatal("API key without sk_ prefix should fail validation")
	}
	if !strings.Contains(err.Error(), "sk_") {
		t.Errorf("error should mention sk_ prefix, got: %v", err)
	}
}

func TestValidate_IssuerInsteadOfTokenEndpoint(t *testing.T) {
	cfg := fullOIDCConfig()
	cfg.OIDCTokenEndpoint = ""
	cfg.OIDCIssuer = "https://kc.example/realms/x"
	if err := Validate(cfg); err != nil {
		t.Fatalf("an issuer should stand in for the token endpoint, got: %v", err)
	}
}

func TestValidate_NoSynaplanURL(t *testing.T) {
	// No built-in default: a missing SYNAPLAN_URL stops the service instead
	// of pointing it at a development host.
	cfg := fullOIDCConfig()
	cfg.SynaplanURL = ""
	err := Validate(cfg)
	if err == nil || !strings.Contains(err.Error(), "SYNAPLAN_URL") {
		t.Fatalf("a config without SYNAPLAN_URL should fail naming it, got: %v", err)
	}
}

func TestValidate_StoreSettings(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*config.Config)
		wantErr string
	}{
		{
			name:   "default nats-js-kv validates",
			mutate: func(cfg *config.Config) { defaults.EnsureDefaults(cfg) },
		},
		{
			name:   "memory validates",
			mutate: func(cfg *config.Config) { cfg.Store.Store = "memory" },
		},
		{
			name:    "unsupported store fails naming OC_PERSISTENT_STORE",
			mutate:  func(cfg *config.Config) { cfg.Store.Store = "redis-sentinel" },
			wantErr: "OC_PERSISTENT_STORE",
		},
		{
			name: "empty nodes on nats-js-kv fails",
			mutate: func(cfg *config.Config) {
				defaults.EnsureDefaults(cfg)
				cfg.Store.Nodes = []string{" ", ""}
				defaults.Sanitize(cfg)
			},
			wantErr: "SYNAPLAN_STORE_NODES",
		},
		{
			name: "negative replicas fails",
			mutate: func(cfg *config.Config) {
				defaults.EnsureDefaults(cfg)
				cfg.Store.Replicas = -1
			},
			wantErr: "SYNAPLAN_STORE_REPLICAS",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := fullOIDCConfig()
			tc.mutate(cfg)
			err := Validate(cfg)
			if tc.wantErr == "" && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)) {
				t.Fatalf("err = %v, want mentioning %q", err, tc.wantErr)
			}
		})
	}
}
