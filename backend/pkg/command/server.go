package command

import (
	"context"
	"fmt"
	stdhttp "net/http"
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

	"github.com/metadist/synaplan-opencloud/internal/handler"
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
			_, err = pool.GatewaySelector(
				cfg.RevaGateway,
				pool.WithTLSMode(tm),
				pool.WithTLSCACert(cfg.GRPCClientTLS.CACert),
			)
			if err != nil {
				return fmt.Errorf("could not get reva client selector: %s", err)
			}

			exchanger := tokenexchange.New(
				cfg.OIDCTokenEndpoint,
				cfg.OIDCExchangeClientID,
				cfg.OIDCExchangeSecret,
				cfg.OIDCTargetAudience,
			)

			h := handler.New(exchanger, cfg.SynaplanURL)

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

			mux.Get("/api/synaplan/me", h.Me)

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
