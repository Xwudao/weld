package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/Xwudao/weld/internal/scaffold"
	"github.com/Xwudao/weld/internal/template"
)

// newAddMiddlewareSubcommand builds `weld add middleware <name>`, which writes a
// project-owned HTTP middleware and its test under internal/middleware.
func newAddMiddlewareSubcommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "middleware <name>",
		Short: "add an HTTP middleware under internal/middleware",
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) != 1 {
				return fmt.Errorf("usage: %s add middleware <name> [--with-dependencies] [--dir dir] [--dry-run]", progName)
			}
			return nil
		},
		RunE: runAddMiddleware,
	}
	cmd.Flags().Bool("with-dependencies", false, "install missing required capabilities instead of failing")
	return cmd
}

// runAddMiddleware plans and applies `weld add middleware <name>`.
func runAddMiddleware(cmd *cobra.Command, args []string) error {
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
	result, err := scaffold.AddMiddleware(scaffold.Request{Dir: dir, Catalog: template.Load(), WithDependencies: withDependencies}, args[0])
	if err != nil {
		return err
	}
	return applyAddResult(cmd.OutOrStdout(), result, dryRun)
}
