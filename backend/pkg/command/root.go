package command

import (
	"os"

	"github.com/metadist/synaplan-opencloud/pkg/config"
	"github.com/opencloud-eu/opencloud/pkg/clihelper"
	"github.com/spf13/cobra"
)

// GetCommands provides all commands for this service.
func GetCommands(cfg *config.Config) []*cobra.Command {
	return []*cobra.Command{
		Server(cfg),
		Health(cfg),
		Version(cfg),
	}
}

// Execute is the entry point for the synaplan command.
func Execute(cfg *config.Config) error {
	app := clihelper.DefaultApp(&cobra.Command{
		Use:   "synaplan",
		Short: "starts the synaplan-opencloud service",
	})
	app.AddCommand(GetCommands(cfg)...)
	app.SetArgs(os.Args[1:])

	return app.ExecuteContext(cfg.Context)
}
