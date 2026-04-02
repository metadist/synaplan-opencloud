package command

import (
	"fmt"

	"github.com/metadist/synaplan-opencloud/pkg/config"
	"github.com/opencloud-eu/opencloud/pkg/version"
	"github.com/spf13/cobra"
)

// Version prints the service version.
func Version(_ *config.Config) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "print the version of this binary",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Println("synaplan-opencloud " + version.GetString())
			return nil
		},
	}
}
