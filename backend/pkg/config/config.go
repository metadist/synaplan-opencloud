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
	SynaplanURL    string `yaml:"synaplan_url" env:"SYNAPLAN_URL" desc:"Base URL of the Synaplan instance."`
	SynaplanAPIKey string `yaml:"synaplan_api_key" env:"SYNAPLAN_API_KEY" desc:"Shared Synaplan API key (sk_...). When set, the backend uses this for every outbound call and skips OIDC token exchange. Discouraged: all calls appear to Synaplan as a single user with no per-user identity."`

	// OIDC token exchange configuration
	OIDCTokenEndpoint    string `yaml:"oidc_token_endpoint" env:"SYNAPLAN_OIDC_TOKEN_ENDPOINT" desc:"Keycloak token endpoint for token exchange."`
	OIDCExchangeClientID string `yaml:"oidc_exchange_client_id" env:"SYNAPLAN_OIDC_EXCHANGE_CLIENT_ID" desc:"Client ID for the token exchange confidential client."`
	OIDCExchangeSecret   string `yaml:"oidc_exchange_client_secret" env:"SYNAPLAN_OIDC_EXCHANGE_CLIENT_SECRET" desc:"Client secret for the token exchange confidential client."`
	OIDCTargetAudience   string `yaml:"oidc_target_audience" env:"SYNAPLAN_OIDC_TARGET_AUDIENCE" desc:"Target audience (Synaplan client ID) for token exchange."`

	// Instance registration. Empty until an administrator registers this
	// OpenCloud, or SYNAPLAN_ADMIN_API_KEY is set and the service registers
	// itself on startup.
	InstanceID     string `yaml:"instance_id" env:"SYNAPLAN_INSTANCE_ID" desc:"Platform instance id Synaplan assigned to this OpenCloud."`
	InstanceSecret string `yaml:"instance_secret" env:"SYNAPLAN_INSTANCE_SECRET" desc:"Instance secret. Used for copies, folder reads and watches. Never logged."`
	AdminAPIKey    string `yaml:"admin_api_key" env:"SYNAPLAN_ADMIN_API_KEY" desc:"Synaplan admin API key. When set, and no instance id is stored yet, the service registers this OpenCloud on startup. Never logged."`
	DeliveryURL    string `yaml:"delivery_url" env:"SYNAPLAN_DELIVERY_URL" desc:"Address Synaplan calls to deliver a copy or ask for a folder read. Example: http://synaplan-backend:9106/api/synaplan/inbound."`
	PublicURL      string `yaml:"public_url" env:"SYNAPLAN_PUBLIC_URL" desc:"Address a person opens in the browser, used in links back to a file."`
	ServerID       string `yaml:"server_id" env:"SYNAPLAN_SERVER_ID" desc:"Stable id of this OpenCloud server, sent when the instance is registered."`
	MachineAuthKey string `yaml:"machine_auth_api_key" env:"OC_MACHINE_AUTH_API_KEY;SYNAPLAN_MACHINE_AUTH_API_KEY" desc:"OpenCloud machine-auth key. Copies and watches use it to read and write a person's files without their browser session."`
	StatePath      string `yaml:"state_path" env:"SYNAPLAN_STATE_PATH" desc:"File that stores read settings and the key used while a person is away. Empty keeps that state in memory only."`
	StateKey       string `yaml:"state_key" env:"SYNAPLAN_STATE_KEY;OC_JWT_SECRET" desc:"Secret used to encrypt keys in the state file. Falls back to the OpenCloud JWT secret. Never logged."`

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
