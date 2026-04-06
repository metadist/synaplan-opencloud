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

// Validate validates the config. Exactly one outbound auth mode must
// be configured: either a shared Synaplan API key (SYNAPLAN_API_KEY)
// or the full set of OIDC token-exchange variables.
func Validate(cfg *config.Config) error {
	hasAPIKey := cfg.SynaplanAPIKey != ""
	hasTokenExchange := cfg.OIDCTokenEndpoint != "" &&
		cfg.OIDCExchangeClientID != "" &&
		cfg.OIDCExchangeSecret != "" &&
		cfg.OIDCTargetAudience != ""

	if !hasAPIKey && !hasTokenExchange {
		return errors.New("synaplan-opencloud: no auth mode configured — set either SYNAPLAN_API_KEY or the SYNAPLAN_OIDC_* token-exchange variables")
	}
	if hasAPIKey && !strings.HasPrefix(cfg.SynaplanAPIKey, "sk_") {
		return errors.New("synaplan-opencloud: SYNAPLAN_API_KEY must start with 'sk_' (Synaplan API key format)")
	}
	return nil
}
