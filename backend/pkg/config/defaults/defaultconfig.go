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
		Store: config.PersistentStore{
			Store:    "nats-js-kv",
			Nodes:    []string{"127.0.0.1:9233"},
			Database: "synaplan-delegation",
			Replicas: 1,
		},
		HTTP: config.HTTP{
			Addr:      "127.0.0.1:9106",
			Root:      "/api/synaplan",
			Namespace: "eu.opencloud.synaplan",
			CORS: config.CORS{
				AllowedOrigins:   []string{"*"},
				AllowedMethods:   []string{"GET", "POST", "DELETE"},
				AllowedHeaders:   []string{"Authorization", "Origin", "Content-Type", "Accept", "X-Requested-With", "X-Request-Id"},
				AllowCredentials: true,
			},
		},
		GRPCClientTLS: &shared.GRPCClientTLS{
			Mode: "off",
		},
	}
}

// EnsureDefaults ensures the config contains default values.
func EnsureDefaults(cfg *config.Config) {
	if cfg.TokenManager == nil {
		cfg.TokenManager = &config.TokenManager{}
	}
	if cfg.Store.Store == "" {
		cfg.Store.Store = "nats-js-kv"
	}
	if cfg.Store.Store == "nats-js-kv" && len(cfg.Store.Nodes) == 0 {
		cfg.Store.Nodes = []string{"127.0.0.1:9233"}
	}
	if cfg.Store.Database == "" {
		cfg.Store.Database = "synaplan-delegation"
	}
	if cfg.Store.Replicas == 0 {
		cfg.Store.Replicas = 1
	}
}

// Sanitize sanitizes the config.
func Sanitize(cfg *config.Config) {
	if cfg.HTTP.Root != "/" {
		cfg.HTTP.Root = strings.TrimSuffix(cfg.HTTP.Root, "/")
	}
	cfg.Store.Store = strings.TrimSpace(cfg.Store.Store)
	cfg.Store.Database = strings.TrimSpace(cfg.Store.Database)
	cfg.Store.Nodes = cleanList(cfg.Store.Nodes)
	cfg.Store.EncryptionPreviousSecrets = cleanList(cfg.Store.EncryptionPreviousSecrets)
}

func cleanList(items []string) []string {
	if len(items) == 0 {
		return nil
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		if trimmed := strings.TrimSpace(item); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}
