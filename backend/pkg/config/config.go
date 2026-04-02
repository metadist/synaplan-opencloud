package config

import (
	"context"

	"github.com/opencloud-eu/opencloud/pkg/shared"
)

// Config combines all available configuration parts.
type Config struct {
	Commons *shared.Commons `yaml:"-"` // don't use this directly as configuration for a service

	Service Service `yaml:"-"`

	LogLevel string `yaml:"loglevel" env:"OC_LOG_LEVEL;SYNAPLAN_LOG_LEVEL" desc:"The log level. Valid values are: 'panic', 'fatal', 'error', 'warn', 'info', 'debug', 'trace'."`
	Debug    Debug  `yaml:"debug"`

	HTTP          HTTP                  `yaml:"http"`
	GRPCClientTLS *shared.GRPCClientTLS `yaml:"grpc_client_tls"`

	TokenManager *TokenManager `yaml:"token_manager"`

	RevaGateway string `yaml:"reva_gateway" env:"OC_REVA_GATEWAY" desc:"CS3 gateway used to look up user metadata"`
	Insecure    bool   `yaml:"insecure" env:"OC_INSECURE;SYNAPLAN_INSECURE" desc:"Run in insecure mode."`

	// Synaplan API configuration
	SynaplanURL string `yaml:"synaplan_url" env:"SYNAPLAN_URL" desc:"Base URL of the Synaplan instance."`

	// OIDC token exchange configuration
	OIDCTokenEndpoint    string `yaml:"oidc_token_endpoint" env:"SYNAPLAN_OIDC_TOKEN_ENDPOINT" desc:"Keycloak token endpoint for token exchange."`
	OIDCExchangeClientID string `yaml:"oidc_exchange_client_id" env:"SYNAPLAN_OIDC_EXCHANGE_CLIENT_ID" desc:"Client ID for the token exchange confidential client."`
	OIDCExchangeSecret   string `yaml:"oidc_exchange_client_secret" env:"SYNAPLAN_OIDC_EXCHANGE_CLIENT_SECRET" desc:"Client secret for the token exchange confidential client."`
	OIDCTargetAudience   string `yaml:"oidc_target_audience" env:"SYNAPLAN_OIDC_TARGET_AUDIENCE" desc:"Target audience (Synaplan client ID) for token exchange."`

	Context context.Context `yaml:"-"`
}

// Service defines the service name.
type Service struct {
	Name string
}

// Debug defines the available debug configuration.
type Debug struct {
	Addr   string `yaml:"addr" env:"SYNAPLAN_DEBUG_ADDR" desc:"Bind address of the debug server."`
	Token  string `yaml:"token" env:"SYNAPLAN_DEBUG_TOKEN" desc:"Token to secure the metrics endpoint."`
	Pprof  bool   `yaml:"pprof" env:"SYNAPLAN_DEBUG_PPROF" desc:"Enables pprof."`
	Zpages bool   `yaml:"zpages" env:"SYNAPLAN_DEBUG_ZPAGES" desc:"Enables zpages."`
}

// HTTP defines the available http configuration.
type HTTP struct {
	Addr      string                `yaml:"addr" env:"SYNAPLAN_HTTP_ADDR" desc:"The bind address of the HTTP service."`
	Namespace string                `yaml:"-"`
	Root      string                `yaml:"root" env:"SYNAPLAN_HTTP_ROOT" desc:"Subdirectory that serves as the root for this HTTP service."`
	CORS      CORS                  `yaml:"cors"`
	TLS       shared.HTTPServiceTLS `yaml:"tls"`
}

// CORS defines the available cors configuration.
type CORS struct {
	AllowedOrigins   []string `yaml:"allow_origins" env:"OC_CORS_ALLOW_ORIGINS;SYNAPLAN_CORS_ALLOW_ORIGINS"`
	AllowedMethods   []string `yaml:"allow_methods" env:"OC_CORS_ALLOW_METHODS;SYNAPLAN_CORS_ALLOW_METHODS"`
	AllowedHeaders   []string `yaml:"allow_headers" env:"OC_CORS_ALLOW_HEADERS;SYNAPLAN_CORS_ALLOW_HEADERS"`
	AllowCredentials bool     `yaml:"allow_credentials" env:"OC_CORS_ALLOW_CREDENTIALS;SYNAPLAN_CORS_ALLOW_CREDENTIALS"`
}

// TokenManager is the config for using the reva token manager.
type TokenManager struct {
	JWTSecret string `yaml:"jwt_secret" env:"OC_JWT_SECRET;SYNAPLAN_JWT_SECRET" desc:"The secret to mint and validate jwt tokens."`
}
