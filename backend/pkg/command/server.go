package command

import (
	"context"
	"fmt"
	stdhttp "net/http"
	"os"
	"os/signal"

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
	"github.com/metadist/synaplan-opencloud/internal/handler"
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

			var editor synaplanapi.RequestEditorFn
			if cfg.SynaplanAPIKey != "" {
				logger.Warn().Msg("synaplan-opencloud: using shared API key auth — all calls will appear to Synaplan as a single user")
				editor = synaplanauth.NewAPIKeyRequestEditor(cfg.SynaplanAPIKey)
			} else {
				exchanger := tokenexchange.New(
					cfg.OIDCTokenEndpoint,
					cfg.OIDCExchangeClientID,
					cfg.OIDCExchangeSecret,
					cfg.OIDCTargetAudience,
				)
				editor = synaplanauth.NewRequestEditor(exchanger)
			}

			h, err := handler.New(editor, cfg.SynaplanURL, cs3, handler.Settings{
				InstanceID:     cfg.InstanceID,
				InstanceSecret: cfg.InstanceSecret,
				AdminAPIKey:    cfg.AdminAPIKey,
				DeliveryURL:    cfg.DeliveryURL,
				PublicURL:      cfg.PublicURL,
				ServerID:       cfg.ServerID,
				OIDCClientID:   cfg.OIDCExchangeClientID,
				MachineAuthKey: cfg.MachineAuthKey,
				StatePath:      cfg.StatePath,
				StateKey:       cfg.StateKey,
				HoldInbound:    os.Getenv("SYNAPLAN_DEV_HOLD_INBOUND") == "1",
				AllowSelfWrite: os.Getenv("SYNAPLAN_DEV_ALLOW_SELF_WRITE") == "1",
			})
			if err != nil {
				return fmt.Errorf("could not create handler: %w", err)
			}
			h.EnsureRegistered(ctx)
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
			// Synaplan calls this with no OpenCloud session. The handler
			// checks the signature before it fetches anything.
			mux.Post("/api/synaplan/inbound", h.Inbound)

			mux.Group(func(r chi.Router) {
				r.Use(synaplanauth.Middleware)
				r.Get("/api/synaplan/me", h.Me)
				r.Post("/api/synaplan/translate", h.Translate)
				r.Post("/api/synaplan/summarize", h.Summarize)
				r.Post("/api/synaplan/knowledge", h.AddToKnowledge)
				r.Get("/api/synaplan/knowledge/groups", h.KnowledgeGroups)
				r.Get("/api/synaplan/knowledge/status", h.KnowledgeStatus)
				r.Delete("/api/synaplan/knowledge", h.RemoveFromKnowledge)
				r.Post("/api/synaplan/files", h.SaveToSpace)
				r.Post("/api/synaplan/chat", h.Chat)
				r.Get("/api/synaplan/models", h.Models)
				r.Post("/api/synaplan/media", h.Media)
				r.Post("/api/synaplan/media/save", h.SaveMedia)
				r.Post("/api/synaplan/cloud/status", h.CloudStatus)
				r.Get("/api/synaplan/cloud/files", h.CloudDetail)
				r.Get("/api/synaplan/watches/options", h.WatchOptions)
				r.Post("/api/synaplan/watches", h.WatchStart)
				r.Post("/api/synaplan/watches/reconcile", h.WatchReconcileNow)
				r.Get("/api/synaplan/from-synaplan", h.ListSynaplanFiles)
				r.Post("/api/synaplan/from-synaplan", h.CopyFromSynaplan)
				r.Get("/api/synaplan/preferences", h.Preferences)
				r.Put("/api/synaplan/preferences", h.SavePreferences)
				r.Get("/api/synaplan/connection", h.Connection)
				r.Post("/api/synaplan/connection/bind", h.Bind)
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
