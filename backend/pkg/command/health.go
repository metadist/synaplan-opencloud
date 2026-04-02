package command

import (
	"github.com/metadist/synaplan-opencloud/pkg/config"
	"github.com/spf13/cobra"
)

// Health is the entrypoint for the health command.
func Health(_ *config.Config) *cobra.Command {
	return &cobra.Command{
		Use:   "health",
		Short: "Check health status",
		RunE: func(cmd *cobra.Command, args []string) error {
			// Not implemented
			return nil
		},
	}
}
