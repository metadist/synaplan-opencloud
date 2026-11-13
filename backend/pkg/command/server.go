package command

import (
	"context"
	"crypto/tls"
	"fmt"
	stdhttp "net/http"
	"os/signal"
	"time"

	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"
	"github.com/opencloud-eu/opencloud/pkg/account"
	"github.com/opencloud-eu/opencloud/pkg/config/configlog"
	"github.com/opencloud-eu/opencloud/pkg/cors"
	"github.com/opencloud-eu/opencloud/pkg/log"
	"github.com/opencloud-eu/opencloud/pkg/middleware"
	"github.com/opencloud-eu/opencloud/pkg/runner"
	"github.com/opencloud-eu/opencloud/pkg/version"
	"github.com/opencloud-eu/reva/v2/pkg/rgrpc/todo/pool"

	"github.com/metadist/synaplan-opencloud/internal/cs3reader"
	"github.com/metadist/synaplan-opencloud/internal/delegation"
	"github.com/metadist/synaplan-opencloud/internal/handler"
	"github.com/metadist/synaplan-opencloud/internal/idp"
	"github.com/metadist/synaplan-opencloud/internal/synaplanapi"
	"github.com/metadist/synaplan-opencloud/internal/synaplanauth"
	"github.com/metadist/synaplan-opencloud/internal/tokenexchange"
	"github.com/metadist/synaplan-opencloud/pkg/config"
	"github.com/metadist/synaplan-opencloud/pkg/config/parser"
	"github.com/spf13/cobra"
)

// Server is the entrypoint for the server command.
func Server(cfg *config.Config) *cobra.Command {
	return &cobra.Command{
		Use:   "server",
		Short: fmt.Sprintf("start the %s service without runtime (unsupervised mode)", cfg.Service.Name),
		PreRunE: func(cmd *cobra.Command, args []string) error {
			return configlog.ReturnFatal(parser.ParseConfig(cfg))
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			logger := log.Configure(cfg.Service.Name, cfg.Commons, cfg.LogLevel)

			var cancel context.CancelFunc
			if cfg.Context == nil {
				cfg.Context, cancel = signal.NotifyContext(context.Background(), runner.StopSignals...)
				defer cancel()
			}
			ctx := cfg.Context

			tm, err := pool.StringToTLSMode(cfg.GRPCClientTLS.Mode)
			if err != nil {
				return err
			}
			gws, err := pool.GatewaySelector(
				cfg.RevaGateway,
				pool.WithTLSMode(tm),
				pool.WithTLSCACert(cfg.GRPCClientTLS.CACert),
			)
			if err != nil {
				return fmt.Errorf("could not get reva client selector: %s", err)
			}

			cs3 := cs3reader.New(gws, cfg.Insecure)
			cs3.SetMachineAuth(cfg.MachineAuthAPIKey)

			var editor synaplanapi.RequestEditorFn
			var delegated *handler.Delegated
			if cfg.SynaplanAPIKey != "" {
				logger.Warn().Msg("synaplan-opencloud: using shared API key auth — all calls will appear to Synaplan as a single user")
				editor = synaplanauth.NewAPIKeyRequestEditor(cfg.SynaplanAPIKey)
			} else {
				provider := idp.New(cfg.OIDCIssuer, idp.Endpoints{
					Authorization: cfg.OIDCAuthorizationEndpoint,
					Token:         cfg.OIDCTokenEndpoint,
					Revocation:    cfg.OIDCRevocationEndpoint,
				}, identityProviderClient(cfg.Insecure))
				exchanger := tokenexchange.New(
					provider.TokenEndpoint,
					cfg.OIDCExchangeClientID,
					cfg.OIDCExchangeSecret,
					cfg.OIDCTargetAudience,
				)
				editor = synaplanauth.NewRequestEditor(exchanger)
				inner, err := delegationStore(cfg, logger)
				if err != nil {
					return err
				}
				defer func() { _ = inner.Close() }()
				secret := cfg.Store.EncryptionSecret
				if secret == "" {
					logger.Warn().Msg("synaplan-opencloud: SYNAPLAN_STORE_ENCRYPTION_SECRET is not set; the offline tokens are encrypted with a key derived from the exchange client secret")
					secret = cfg.OIDCExchangeSecret
				}
				keys, err := delegation.NewLocalKeys(append([]string{secret}, cfg.Store.EncryptionPreviousSecrets...)...)
				if err != nil {
					return fmt.Errorf("could not set up the delegation store key: %w", err)
				}
				store := delegation.NewSealedStore(inner, keys)
				delegated = &handler.Delegated{
					Tokens: delegation.New(delegation.Config{
						Provider:     provider,
						ClientID:     cfg.OIDCExchangeClientID,
						ClientSecret: cfg.OIDCExchangeSecret,
						HTTPClient:   identityProviderClient(cfg.Insecure),
						Store:        store,
						StoreFailed: func(err error) {
							logger.Warn().Err(err).Msg("synaplan-opencloud: could not use the token store")
						},
					}),
					Exchanger:     exchanger,
					Audience:      cfg.OIDCTargetAudience,
					UserClaim:     cfg.UserOIDCClaim,
					UserAttribute: cfg.UserCS3Claim,
				}
			}

			h, err := handler.New(editor, cfg.SynaplanURL, cs3, delegated)
			if err != nil {
				return fmt.Errorf("could not create handler: %w", err)
			}
			if delegated != nil && cfg.MachineAuthAPIKey == "" {
				logger.Info().Msg("synaplan-opencloud: OC_MACHINE_AUTH_API_KEY is not set; watched files are only sent when a person checks a watch themselves")
			}
			go h.RunReconciler(ctx)

			mux := chi.NewMux()
			mux.Use(
				chimiddleware.RequestID,
				middleware.Version(
					cfg.Service.Name,
					version.GetString(),
				),
				middleware.Logger(logger),
				middleware.ExtractAccountUUID(
					account.Logger(logger),
					account.JWTSecret(cfg.TokenManager.JWTSecret),
				),
				middleware.Cors(
					cors.Logger(logger),
					cors.AllowedOrigins(cfg.HTTP.CORS.AllowedOrigins),
					cors.AllowedMethods(cfg.HTTP.CORS.AllowedMethods),
					cors.AllowedHeaders(cfg.HTTP.CORS.AllowedHeaders),
					cors.AllowCredentials(cfg.HTTP.CORS.AllowCredentials),
				),
			)

			// Public — <img> can't send a Bearer header.
			mux.Get("/api/synaplan/assets/brand-icon", h.BrandIcon)
			mux.Get("/api/synaplan/assets/{name}", h.Asset)
			// Public: Synaplan calls it, and a notification is a signed token.
			mux.Post("/api/synaplan/inbound", h.Inbound)

			mux.Group(func(r chi.Router) {
				r.Use(synaplanauth.Middleware)
				r.Get("/api/synaplan/me", h.Me)
				r.Post("/api/synaplan/translate", h.Translate)
				r.Post("/api/synaplan/summarize", h.Summarize)
				r.Post("/api/synaplan/knowledge", h.AddToKnowledge)
				r.Get("/api/synaplan/knowledge/groups", h.KnowledgeGroups)
				r.Get("/api/synaplan/knowledge/status", h.KnowledgeStatus)
				r.Post("/api/synaplan/cloud/status", h.CloudStatus)
				r.Delete("/api/synaplan/knowledge", h.RemoveFromKnowledge)
				r.Post("/api/synaplan/files", h.SaveToSpace)
				r.Get("/api/synaplan/delegation", h.DelegationStatus)
				r.Post("/api/synaplan/delegation", h.DelegationConnect)
				r.Delete("/api/synaplan/delegation", h.DelegationDisconnect)
				r.Get("/api/synaplan/watches/options", h.WatchOptions)
				r.Post("/api/synaplan/watches", h.WatchStart)
				r.Delete("/api/synaplan/watches", h.WatchStop)
				r.Post("/api/synaplan/watches/reconcile", h.WatchReconcileNow)
			})

			server := &stdhttp.Server{
				Addr:    cfg.HTTP.Addr,
				Handler: mux,
			}

			gr := runner.NewGroup()
			gr.Add(runner.NewGolangHttpServerRunner(cfg.Service.Name+".http", server))

			logger.Info().
				Str("addr", cfg.HTTP.Addr).
				Str("synaplan_url", cfg.SynaplanURL).
				Msg("synaplan-opencloud backend listening")

			grResults := gr.Run(ctx)

			for _, grResult := range grResults {
				if grResult.RunnerError != nil {
					return grResult.RunnerError
				}
			}
			return nil
		},
	}
}

// identityProviderClient talks to the identity provider. insecure accepts
// a self-signed certificate, as OC_INSECURE does for the rest of OpenCloud
// (development only).
func identityProviderClient(insecure bool) *stdhttp.Client {
	transport := stdhttp.DefaultTransport.(*stdhttp.Transport).Clone()
	if insecure {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // OC_INSECURE, development only
	}
	return &stdhttp.Client{Timeout: 15 * time.Second, Transport: transport}
}

// delegationStore is the store the configuration names. There is no
// fallback: a store that is not supported stops the service.
func delegationStore(cfg *config.Config, logger log.Logger) (delegation.Store, error) {
	switch cfg.Store.Store {
	case "memory":
		logger.Warn().Msg("synaplan-opencloud: offline tokens are kept in memory; a restart forgets them and other replicas never see them")
		return delegation.NewMemoryStore(), nil
	case "nats-js-kv":
		store, err := delegation.NewNATS(delegation.NATSConfig{
			Nodes:     cfg.Store.Nodes,
			Bucket:    cfg.Store.Database,
			Replicas:  cfg.Store.Replicas,
			Username:  cfg.Store.AuthUsername,
			Password:  cfg.Store.AuthPassword,
			EnableTLS: cfg.Store.EnableTLS,
			Insecure:  cfg.Store.TLSInsecure,
			RootCAs:   cfg.Store.TLSRootCACertificate,
		})
		if err != nil {
			return nil, fmt.Errorf("could not open the delegation store: %w", err)
		}
		return store, nil
	default:
		return nil, fmt.Errorf("synaplan-opencloud: store %q is not supported; set SYNAPLAN_STORE to nats-js-kv or memory (it may be inherited from OC_PERSISTENT_STORE)", cfg.Store.Store)
	}
}
