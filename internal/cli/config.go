package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/Xwudao/weld/internal/projectcfg"
)

// newConfigCommand builds `weld config`, a namespace for project-configuration
// helpers: the weld.yml starter and the read-only configuration check.
func newConfigCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "create weld.yml and check the project's configuration files",
		Args:  cobra.NoArgs,
	}
	cmd.AddCommand(newConfigExampleCommand(), newConfigInitCommand(), newConfigCheckCommand())
	return cmd
}

// newConfigExampleCommand builds `weld config example`, which prints the starter
// to stdout and writes nothing.
func newConfigExampleCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "example",
		Short: "print a commented weld.yml example",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Fprint(cmd.OutOrStdout(), projectcfg.Example())
		},
	}
}

// newConfigInitCommand builds `weld config init`, which writes the starter into
// the project. It is idempotent: an existing weld.yml is left untouched unless
// --force is given, so the command never clobbers hand-edited settings.
func newConfigInitCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "init",
		Short: "write a commented weld.yml to the project if it has none",
		Args:  cobra.NoArgs,
		RunE:  runConfigInit,
	}
	cmd.Flags().Bool("force", false, "overwrite an existing weld.yml")
	return cmd
}

// runConfigInit plans and applies `weld config init`.
func runConfigInit(cmd *cobra.Command, args []string) error {
	stdout := cmd.OutOrStdout()
	dir, err := cmd.Flags().GetString("dir")
	if err != nil {
		return err
	}
	dryRun, err := cmd.Flags().GetBool("dry-run")
	if err != nil {
		return err
	}
	force, err := cmd.Flags().GetBool("force")
	if err != nil {
		return err
	}

	// Resolve the existing-file case up front so a dry run and a real run
	// report the same thing and an existing file is never overwritten without
	// --force.
	path := projectcfg.Path(dir)
	if !force {
		if _, statErr := os.Stat(path); statErr == nil {
			fmt.Fprintf(stdout, "%s already exists; left unchanged (use --force to overwrite).\n", path)
			return nil
		}
	}
	if dryRun {
		fmt.Fprintf(stdout, "Plan (1 change(s), dry run):\n  add %s\n", path)
		return nil
	}
	if _, _, err := projectcfg.WriteExample(dir, force); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Applied 1 change(s):\n  add %s\n", path)
	return nil
}
