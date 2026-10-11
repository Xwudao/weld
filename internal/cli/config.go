package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/Xwudao/weld/internal/projectcfg"
)

// newConfigCommand builds `weld config`, a namespace for project-configuration
// helpers. It currently exposes only `example`, which prints the user-owned
// weld.yml starter.
func newConfigCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "print and explain the user-owned weld.yml project configuration",
		Args:  cobra.NoArgs,
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "example",
		Short: "print a commented weld.yml example",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Fprint(cmd.OutOrStdout(), projectcfg.Example())
		},
	})
	return cmd
}
