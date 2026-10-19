package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Xwudao/weld/internal/testrun"
)

// newTestCommand builds `weld test`.
func newTestCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "test",
		Short: "run the project's Go tests, with the configured test database",
		Long: `Run the project's Go tests, pointing the generated PostgreSQL integration
tests at the test database configured in config.yml's database_test section (or
PG_TEST_DSN). weld test never starts a database.

  weld test                          go test ./...
  weld test --db=off                 ignore any configured test database
  weld test --db=require             fail when no test database is configured
  weld test --db-url 'postgres://...'
  weld test ./internal/data/... -- -run Integration -v

When a test database is configured, weld test sets PG_TEST_DSN and
WELD_REQUIRE_DB_TESTS=1 for the go test process, so a connection failure is an
error rather than a skip. The dsn is never printed. Pass go test flags after
"--".`,
		Args: cobra.ArbitraryArgs,
		RunE: runTest,
	}
	cmd.Flags().String("db", "auto", "test database policy: auto, off or require")
	cmd.Flags().String("db-url", "", "explicit test database dsn")
	cmd.Flags().String("config", "", "application config file (default: config.yml)")
	return cmd
}

// runTest runs the project's Go tests.
func runTest(cmd *cobra.Command, args []string) error {
	dir, err := cmd.Flags().GetString("dir")
	if err != nil {
		return err
	}
	mode, err := cmd.Flags().GetString("db")
	if err != nil {
		return err
	}
	dbURL, err := cmd.Flags().GetString("db-url")
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

	// pflag removes the "--" separator but keeps every positional argument in
	// order, so args is exactly the go test argv (packages then flags).
	result, err := testrun.Run(testrun.Request{
		Dir:        dir,
		Mode:       testrun.Mode(mode),
		DBURL:      dbURL,
		ConfigPath: configPath,
		Args:       args,
		Stdout:     cmd.OutOrStdout(),
		Stderr:     cmd.ErrOrStderr(),
		DryRun:     dryRun,
	})
	if err != nil {
		return err
	}
	if dryRun {
		fmt.Fprintf(cmd.OutOrStdout(), "Plan (go %s):\n", strings.Join(result.Args, " "))
		if result.DSN != "" {
			fmt.Fprintf(cmd.OutOrStdout(), "  test database: %s\n", result.Source)
		} else {
			fmt.Fprintf(cmd.OutOrStdout(), "  test database: none (integration tests skip)\n")
		}
	}
	return nil
}
