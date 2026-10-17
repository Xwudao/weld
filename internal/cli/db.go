package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/Xwudao/weld/internal/db"
)

// newDBCommand builds `weld db`, the namespace for the project's database
// migration commands.
func newDBCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "db",
		Short: "run the project's database migrations",
		Long:  dbHelp(),
		Args:  cobra.NoArgs,
		RunE:  func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	cmd.PersistentFlags().String("config", "", "application config file (default: config.yml)")

	migrate := &cobra.Command{
		Use:   "migrate",
		Short: "apply, inspect or revert the project's SQL migrations",
		Args:  cobra.NoArgs,
		RunE:  func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	migrate.AddCommand(
		newDBMigrateCommand("up", "apply every pending migration", false),
		newDBMigrateCommand("status", "show which migrations are applied", false),
		newDBMigrateCommand("down", "revert exactly one migration (requires --yes)", true),
	)

	create := &cobra.Command{
		Use:   "create <name>",
		Short: "write a timestamped goose migration skeleton",
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) != 1 {
				return fmt.Errorf("usage: %s db create <name> [--dir dir] [--dry-run]", progName)
			}
			return nil
		},
		RunE: runDBCreate,
	}

	cmd.AddCommand(migrate, create)
	return cmd
}

// newDBMigrateCommand builds one `weld db migrate <action>` leaf.
func newDBMigrateCommand(action, short string, destructive bool) *cobra.Command {
	cmd := &cobra.Command{
		Use:   action,
		Short: short,
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runDBMigrate(cmd, db.Action(action), destructive)
		},
	}
	if destructive {
		cmd.Flags().Bool("yes", false, "confirm reverting the latest migration")
	}
	return cmd
}

// runDBMigrate runs one migration action through the pinned goose tool.
func runDBMigrate(cmd *cobra.Command, action db.Action, destructive bool) error {
	stdout := cmd.OutOrStdout()
	dir, err := cmd.Flags().GetString("dir")
	if err != nil {
		return err
	}
	configPath, err := cmd.Flags().GetString("config")
	if err != nil {
		return err
	}
	dryRun, err := cmd.Flags().GetBool("dry-run")
	if err != nil {
		return err
	}
	confirm := false
	if destructive {
		confirm, err = cmd.Flags().GetBool("yes")
		if err != nil {
			return err
		}
		if !confirm && os.Getenv("CONFIRM") == "1" {
			confirm = true
		}
	}

	result, err := db.Migrate(db.Request{
		Dir:        dir,
		ConfigPath: configPath,
		Action:     action,
		Confirm:    confirm,
		DryRun:     dryRun,
		Stdout:     stdout,
		Stderr:     cmd.ErrOrStderr(),
	})
	if err != nil {
		return err
	}
	if dryRun {
		fmt.Fprintf(stdout, "Plan (1 step, dry run):\n  %s\n  dsn: %s\n", result.Step, result.Source)
		return nil
	}
	verb := "Applied"
	if action == db.ActionStatus {
		verb = "Ran"
	}
	fmt.Fprintf(stdout, "%s 1 step (%s):\n  %s\n", verb, action, result.Step)
	return nil
}

// runDBCreate writes a timestamped migration skeleton.
func runDBCreate(cmd *cobra.Command, args []string) error {
	stdout := cmd.OutOrStdout()
	dir, err := cmd.Flags().GetString("dir")
	if err != nil {
		return err
	}
	dryRun, err := cmd.Flags().GetBool("dry-run")
	if err != nil {
		return err
	}
	result, err := db.Create(db.CreateRequest{Dir: dir, Name: args[0], DryRun: dryRun})
	if err != nil {
		return err
	}
	if dryRun {
		fmt.Fprintf(stdout, "Plan (1 change, dry run):\n  add %s\n", result.Path)
		return nil
	}
	fmt.Fprintf(stdout, "Created %s\n", result.Path)
	return nil
}

// dbHelp explains the database commands and where the dsn comes from.
func dbHelp() string {
	return `Run the project's SQL migrations with the goose tool pinned in db/tools.

  weld db migrate up        apply every pending migration
  weld db migrate status    show which migrations are applied
  weld db migrate down      revert exactly one migration (requires --yes or CONFIRM=1)
  weld db create <name>     write db/migrations/<timestamp>_<name>.sql

The project must have the db capability installed. The dsn is read from the
application's config.yml (database.dsn or the host/port/user/password/name
fields, with DB_HOST/DB_PORT/DB_USER/DB_PASSWORD/DB_NAME overrides), or from
DATABASE_URL, which takes precedence. An empty DATABASE_URL is ignored. The dsn
is passed to goose but never printed: dry runs show $DATABASE_URL instead.

--config names a different application configuration file, relative to --dir.
down reverts one migration and refuses to run without an explicit confirmation.`
}
