package parser

import (
	"errors"
	"strings"

	occfg "github.com/opencloud-eu/opencloud/pkg/config"
	"github.com/opencloud-eu/opencloud/pkg/config/envdecode"

	"github.com/metadist/synaplan-opencloud/pkg/config"
	"github.com/metadist/synaplan-opencloud/pkg/config/defaults"
)

// ParseConfig loads configuration from known paths.
func ParseConfig(cfg *config.Config) error {
	err := occfg.BindSourcesToStructs(cfg.Service.Name, cfg)
	if err != nil {
		return err
	}

	defaults.EnsureDefaults(cfg)

	// load all env variables relevant to the config in the current context.
	if err := envdecode.Decode(cfg); err != nil {
		// no environment variable set for this config is an expected "error"
		if !errors.Is(err, envdecode.ErrNoTargetFieldsAreSet) {
			return err
		}
	}

	defaults.Sanitize(cfg)

	return Validate(cfg)
}

// Validate validates the config. SYNAPLAN_URL is required, and exactly
// one outbound auth mode must be configured: either a shared Synaplan API
// key (SYNAPLAN_API_KEY) or the full set of OIDC token-exchange variables.
// Nothing has a built-in default: a missing setting stops the service
// instead of pointing it at a development host.
func Validate(cfg *config.Config) error {
	if cfg.SynaplanURL == "" {
		return errors.New("synaplan-opencloud: SYNAPLAN_URL is not set")
	}
	hasAPIKey := cfg.SynaplanAPIKey != ""
	hasTokenExchange := (cfg.OIDCIssuer != "" || cfg.OIDCTokenEndpoint != "") &&
		cfg.OIDCExchangeClientID != "" &&
		cfg.OIDCExchangeSecret != "" &&
		cfg.OIDCTargetAudience != ""

	if !hasAPIKey && !hasTokenExchange {
		return errors.New("synaplan-opencloud: no auth mode configured — set either SYNAPLAN_API_KEY or OC_OIDC_ISSUER (or SYNAPLAN_OIDC_TOKEN_ENDPOINT) and the SYNAPLAN_OIDC_* token-exchange variables")
	}
	if hasAPIKey && !strings.HasPrefix(cfg.SynaplanAPIKey, "sk_") {
		return errors.New("synaplan-opencloud: SYNAPLAN_API_KEY must start with 'sk_' (Synaplan API key format)")
	}
	if !hasAPIKey && cfg.Store.Store != "" {
		switch cfg.Store.Store {
		case "nats-js-kv":
			if len(cfg.Store.Nodes) == 0 {
				return errors.New("synaplan-opencloud: SYNAPLAN_STORE_NODES must name at least one NATS address when SYNAPLAN_STORE is nats-js-kv")
			}
			if cfg.Store.Database == "" {
				return errors.New("synaplan-opencloud: SYNAPLAN_STORE_DATABASE must not be empty when SYNAPLAN_STORE is nats-js-kv")
			}
		case "memory":
		default:
			return errors.New("synaplan-opencloud: store " + `"` + cfg.Store.Store + `"` + " is not supported; set SYNAPLAN_STORE to nats-js-kv or memory (it may be inherited from OC_PERSISTENT_STORE)")
		}
		if cfg.Store.Replicas < 0 {
			return errors.New("synaplan-opencloud: SYNAPLAN_STORE_REPLICAS must not be negative")
		}
	}
	return nil
}
