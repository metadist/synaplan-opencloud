package parser

import (
	"strings"
	"testing"

	"github.com/metadist/synaplan-opencloud/pkg/config"
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
