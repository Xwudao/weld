package cli

import (
	"github.com/Xwudao/weld/internal/scaffold"
	"github.com/Xwudao/weld/internal/template"
	"github.com/spf13/cobra"
)

func newRemoveCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "remove", Short: "remove installed business modules"}
	module := &cobra.Command{
		Use:   "module <name>",
		Short: "remove a business module and its linked command",
		Long: "Remove a business module and its linked command without uninstalling shared capabilities or changing the database.\n" +
			"Use --delete-source to include edited and untracked files in the module directory and edited files owned by its linked command. Preview with --dry-run.",
		Args: cobra.ExactArgs(1),
		RunE: runRemoveModule,
	}
	module.Flags().Bool("delete-source", false, "also delete edited and untracked module files and edited linked command files")
	cmd.AddCommand(module)
	return cmd
}

func runRemoveModule(cmd *cobra.Command, args []string) error {
	dir, err := cmd.Flags().GetString("dir")
	if err != nil {
		return err
	}
	dryRun, err := cmd.Flags().GetBool("dry-run")
	if err != nil {
		return err
	}
	deleteSource, err := cmd.Flags().GetBool("delete-source")
	if err != nil {
		return err
	}
	result, err := scaffold.RemoveModule(scaffold.Request{Dir: dir, Catalog: template.Load()}, args[0], deleteSource)
	if err != nil {
		return err
	}
	if !dryRun {
		if err := result.Apply(); err != nil {
			return err
		}
	}
	printResult(cmd.OutOrStdout(), result, dryRun)
	return nil
}
