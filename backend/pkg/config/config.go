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

	// MaxCopyBytes caps a file copied from Synaplan into OpenCloud.
	// The default matches Cloudflare's 100 MiB body limit. A film does
	// not fit; raise it only where a larger body can pass, and give
	// the process enough memory to buffer the file.
	MaxCopyBytes int64 `yaml:"max_copy_bytes" env:"SYNAPLAN_MAX_COPY_BYTES" desc:"Largest file, in bytes, copied from Synaplan into OpenCloud. Defaults to 104857600 (100 MiB), the body size Cloudflare accepts. A film does not fit in that; raise it where a larger body is allowed. The file is buffered in this service, so the process needs the memory."`

	// OIDC token exchange configuration
	OIDCIssuer                string `yaml:"oidc_issuer" env:"OC_OIDC_ISSUER;SYNAPLAN_OIDC_ISSUER" desc:"Issuer of the identity provider OpenCloud and Synaplan share. The endpoints below are read from its OpenID Connect discovery document unless they are set."`
	OIDCAuthorizationEndpoint string `yaml:"oidc_authorization_endpoint" env:"SYNAPLAN_OIDC_AUTHORIZATION_ENDPOINT" desc:"Optional. Authorization endpoint the person's browser asks for a code, instead of the published one."`
	OIDCTokenEndpoint         string `yaml:"oidc_token_endpoint" env:"SYNAPLAN_OIDC_TOKEN_ENDPOINT" desc:"Optional. Token endpoint as this service reaches it, instead of the published one (an internal address)."`
	OIDCRevocationEndpoint    string `yaml:"oidc_revocation_endpoint" env:"SYNAPLAN_OIDC_REVOCATION_ENDPOINT" desc:"Optional. Revocation endpoint (RFC 7009) as this service reaches it, instead of the published one."`
	OIDCExchangeClientID      string `yaml:"oidc_exchange_client_id" env:"SYNAPLAN_OIDC_EXCHANGE_CLIENT_ID" desc:"Client ID for the token exchange confidential client."`
	OIDCExchangeSecret        string `yaml:"oidc_exchange_client_secret" env:"SYNAPLAN_OIDC_EXCHANGE_CLIENT_SECRET" desc:"Client secret for the token exchange confidential client."`
	OIDCTargetAudience        string `yaml:"oidc_target_audience" env:"SYNAPLAN_OIDC_TARGET_AUDIENCE" desc:"Target audience (Synaplan client ID) for token exchange."`

	// Store keeps the offline tokens of people who allowed work while they
	// are away, and each person's own settings, in the key-value store
	// OpenCloud already runs.
	Store PersistentStore `yaml:"store"`

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

// PersistentStore is OpenCloud's shared store for the offline tokens and
// each person's own settings.
// The variable names are the ones OpenCloud services use for their own
// persistent store, plus a SYNAPLAN_ alias.
type PersistentStore struct {
	Store                     string   `yaml:"store" env:"OC_PERSISTENT_STORE;SYNAPLAN_STORE" desc:"Where the offline tokens of people who allowed work while away, and each person's own settings, are kept. 'nats-js-kv' is OpenCloud's JetStream key-value store, shared by every replica of this service. 'memory' keeps them in this process: a restart forgets them and other replicas never see them. Other values OpenCloud knows, such as redis-sentinel, are not supported here."`
	Nodes                     []string `yaml:"nodes" env:"OC_PERSISTENT_STORE_NODES;SYNAPLAN_STORE_NODES" desc:"Addresses of OpenCloud's NATS, as host:port. A comma-separated list when there is more than one. Only applies when the store is nats-js-kv. NATS must be 2.11 or later."`
	Database                  string   `yaml:"database" env:"SYNAPLAN_STORE_DATABASE" desc:"JetStream key-value bucket for the offline tokens."`
	PreferencesDatabase       string   `yaml:"preferences_database" env:"SYNAPLAN_STORE_PREFERENCES_DATABASE" desc:"JetStream key-value bucket for each person's own Synaplan settings, such as what Synaplan may read. Kept in the same store as the offline tokens."`
	Replicas                  int      `yaml:"replicas" env:"SYNAPLAN_STORE_REPLICAS" desc:"How many NATS servers keep a copy of the bucket, in a NATS cluster. The bucket's settings are applied at every start, so they replace changes made to the bucket by hand."`
	AuthUsername              string   `yaml:"username" env:"OC_PERSISTENT_STORE_AUTH_USERNAME;SYNAPLAN_STORE_AUTH_USERNAME" desc:"Username for OpenCloud's NATS. Only applies when the store is nats-js-kv."`
	AuthPassword              string   `yaml:"password" env:"OC_PERSISTENT_STORE_AUTH_PASSWORD;SYNAPLAN_STORE_AUTH_PASSWORD" desc:"Password for OpenCloud's NATS. Only applies when the store is nats-js-kv."`
	EnableTLS                 bool     `yaml:"enable_tls" env:"OC_PERSISTENT_STORE_ENABLE_TLS;SYNAPLAN_STORE_ENABLE_TLS" desc:"Use TLS to reach OpenCloud's NATS. Only applies when the store is nats-js-kv."`
	TLSInsecure               bool     `yaml:"tls_insecure" env:"OC_INSECURE;OC_PERSISTENT_STORE_TLS_INSECURE;SYNAPLAN_STORE_TLS_INSECURE" desc:"Skip verification of NATS's certificate. Only applies when TLS is enabled and no root CA is set."`
	TLSRootCACertificate      string   `yaml:"tls_root_ca_certificate" env:"OC_PERSISTENT_STORE_TLS_ROOT_CA_CERTIFICATE;SYNAPLAN_STORE_TLS_ROOT_CA_CERTIFICATE" desc:"Root CA used to verify NATS's certificate. When set, insecure verification is ignored."`
	EncryptionSecret          string   `yaml:"encryption_secret" env:"SYNAPLAN_STORE_ENCRYPTION_SECRET" desc:"Secret the key that encrypts the offline tokens is derived from. Keep it like any other secret; anyone with it and a copy of the store can read the tokens. Defaults to the exchange client secret, so changing that secret then makes everyone allow again."`
	EncryptionPreviousSecrets []string `yaml:"encryption_previous_secrets" env:"SYNAPLAN_STORE_ENCRYPTION_PREVIOUS_SECRETS" desc:"Earlier encryption secrets, still used to read tokens written before a rotation. A token moves to the current secret the next time it is used. A comma-separated list."`
}
