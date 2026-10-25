package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/Xwudao/weld/internal/scaffold"
	"github.com/Xwudao/weld/internal/template"
)

// newAddJobSubcommand builds `weld add job <name>`, which writes a
// project-owned scheduled job and its test under internal/jobs.
func newAddJobSubcommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "job <name>",
		Short: "add a scheduled cron job under internal/jobs",
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) != 1 {
				return fmt.Errorf("usage: %s add job <name> [--schedule \"m h dom mon dow\"] [--with-dependencies] [--dir dir] [--dry-run]", progName)
			}
			return nil
		},
		RunE: runAddJob,
	}
	cmd.Flags().String("schedule", scaffold.DefaultJobSchedule, "five-field cron expression the job runs on")
	cmd.Flags().Bool("with-dependencies", false, "install missing required capabilities instead of failing")
	return cmd
}

// runAddJob plans and applies `weld add job <name>`.
func runAddJob(cmd *cobra.Command, args []string) error {
	dir, err := cmd.Flags().GetString("dir")
	if err != nil {
		return err
	}
	dryRun, err := cmd.Flags().GetBool("dry-run")
	if err != nil {
		return err
	}
	schedule, err := cmd.Flags().GetString("schedule")
	if err != nil {
		return err
	}
	withDependencies, err := cmd.Flags().GetBool("with-dependencies")
	if err != nil {
		return err
	}
	result, err := scaffold.AddJob(scaffold.Request{Dir: dir, Catalog: template.Load(), WithDependencies: withDependencies}, args[0], schedule)
	if err != nil {
		return err
	}
	return applyAddResult(cmd.OutOrStdout(), result, dryRun)
}
