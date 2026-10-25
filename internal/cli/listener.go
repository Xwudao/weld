package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/Xwudao/weld/internal/scaffold"
	"github.com/Xwudao/weld/internal/template"
)

// newAddListenerSubcommand builds `weld add listener <name>`, which writes a
// project-owned event type, its listener and a test under internal/listeners.
func newAddListenerSubcommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "listener <name>",
		Short: "add an event and its listener under internal/listeners",
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) != 1 {
				return fmt.Errorf("usage: %s add listener <name> [--with-dependencies] [--dir dir] [--dry-run]", progName)
			}
			return nil
		},
		RunE: runAddListener,
	}
	cmd.Flags().Bool("with-dependencies", false, "install missing required capabilities instead of failing")
	return cmd
}

// runAddListener plans and applies `weld add listener <name>`.
func runAddListener(cmd *cobra.Command, args []string) error {
	dir, err := cmd.Flags().GetString("dir")
	if err != nil {
		return err
	}
	dryRun, err := cmd.Flags().GetBool("dry-run")
	if err != nil {
		return err
	}
	withDependencies, err := cmd.Flags().GetBool("with-dependencies")
	if err != nil {
		return err
	}
	result, err := scaffold.AddListener(scaffold.Request{Dir: dir, Catalog: template.Load(), WithDependencies: withDependencies}, args[0])
	if err != nil {
		return err
	}
	return applyAddResult(cmd.OutOrStdout(), result, dryRun)
}
