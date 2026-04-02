package defaults

import (
	"strings"

	"github.com/metadist/synaplan-opencloud/pkg/config"
	"github.com/opencloud-eu/opencloud/pkg/shared"
)

// FullDefaultConfig returns the full default config.
func FullDefaultConfig() *config.Config {
	cfg := DefaultConfig()
	EnsureDefaults(cfg)
	Sanitize(cfg)
	return cfg
}

// DefaultConfig returns the default configuration.
func DefaultConfig() *config.Config {
	return &config.Config{
		Commons: &shared.Commons{
			Log: &shared.Log{},
		},
		Debug: config.Debug{
			Addr: "127.0.0.1:9267",
		},
		Service: config.Service{
			Name: "synaplan",
		},
		RevaGateway: shared.DefaultRevaConfig().Address,
		HTTP: config.HTTP{
			Addr:      "127.0.0.1:9106",
			Root:      "/api/synaplan",
			Namespace: "eu.opencloud.synaplan",
			CORS: config.CORS{
				AllowedOrigins:   []string{"*"},
				AllowedMethods:   []string{"GET", "POST"},
				AllowedHeaders:   []string{"Authorization", "Origin", "Content-Type", "Accept", "X-Requested-With", "X-Request-Id"},
				AllowCredentials: true,
			},
		},
		GRPCClientTLS: &shared.GRPCClientTLS{
			Mode: "off",
		},
		SynaplanURL:          "http://host.docker.internal:8000",
		OIDCTokenEndpoint:    "http://host.docker.internal:8080/realms/synaplan/protocol/openid-connect/token",
		OIDCExchangeClientID: "synaplan-opencloud",
		OIDCExchangeSecret:   "synaplan-opencloud-secret",
		OIDCTargetAudience:   "synaplan-app",
	}
}

// EnsureDefaults ensures the config contains default values.
func EnsureDefaults(cfg *config.Config) {
	if cfg.TokenManager == nil {
		cfg.TokenManager = &config.TokenManager{}
	}
}

// Sanitize sanitizes the config.
func Sanitize(cfg *config.Config) {
	if cfg.HTTP.Root != "/" {
		cfg.HTTP.Root = strings.TrimSuffix(cfg.HTTP.Root, "/")
	}
}
