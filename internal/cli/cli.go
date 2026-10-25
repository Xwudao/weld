// Package cli implements the weld command line.
//
// The command tree is built with Cobra so the dispatch, help and flag handling
// match the generated applications' command trees. Run keeps the tool's public
// shape: it takes the argument vector and the output writers, returns a non-nil
// error on failure, and never prints an error itself, so the caller reports it
// exactly once.
package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Xwudao/weld/internal/db"
	"github.com/Xwudao/weld/internal/loomgen"
	"github.com/Xwudao/weld/internal/project"
	"github.com/Xwudao/weld/internal/scaffold"
	"github.com/Xwudao/weld/internal/semver"
	"github.com/Xwudao/weld/internal/skills"
	"github.com/Xwudao/weld/internal/template"
	weldtemplate "github.com/Xwudao/weld/internal/weldtemplate"
)

const (
	progName = "weld"
	version  = "0.2.1"
)

// Run executes a weld invocation and returns a non-nil error on failure. It
// neither prints the error nor exits: main reports a returned error once and
// sets the exit status, so the command tree stays silent here.
func Run(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	root := newRootCommand()
	root.SetArgs(args)
	root.SetIn(stdin)
	root.SetOut(stdout)
	root.SetErr(stderr)
	return root.Execute()
}

// newRootCommand builds the root command with every weld command attached.
func newRootCommand() *cobra.Command {
	root := &cobra.Command{
		Use:   progName,
		Short: "progressive Go scaffold",
		Long:  usageLong,
		// main reports a failed command once, through its own error writer, so
		// the command tree stays silent here: no "Error:" line and no usage
		// dump on a bad flag or a missing argument.
		SilenceErrors: true,
		SilenceUsage:  true,
		// A bare invocation (no subcommand) shows help. Anything else that is
		// not a known command is an unknown-command error, matching the tool's
		// earlier dispatcher.
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return nil
			}
			return fmt.Errorf("unknown command %q (run: %s help)", args[0], progName)
		},
		RunE: func(cmd *cobra.Command, args []string) error { return cmd.Help() },
	}
	root.Version = fmt.Sprintf("%s (templates %s)", version, weldtemplate.Version)
	root.SetVersionTemplate(progName + " {{.Version}}\n")
	// weld ships no shell-completion generator; keep the command surface to the
	// commands the tool actually contributes.
	root.CompletionOptions.DisableDefaultCmd = true
	// --dir and --dry-run are shared by every project-aware command, so they
	// are persistent flags defined once here.
	root.PersistentFlags().String("dir", ".", "project directory")
	root.PersistentFlags().Bool("dry-run", false, "print the plan without writing")
	root.PersistentFlags().Bool("no-hooks", false, "skip the weld.yml hooks for this run")
	root.AddCommand(
		newNewCommand(),
		newAddCommand(),
		newUpgradeCommand(),
		newGenCommand(),
		newListCommand(),
		newExplainCommand(),
		newSkillsCommand(),
		newBuildCommand(),
		newDockerCommand(),
		newDevCommand(),
		newSeedCommand(),
		newTestCommand(),
		newDeployCommand(),
		newDBCommand(),
		newDoctorCommand(),
		newConfigCommand(),
		newHTTPCommand(),
		newClientCommand(),
		newVersionCommand(),
	)
	return root
}

// newNewCommand builds `weld new`.
func newNewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "new <name>",
		Short: "create a minimal Go CLI project",
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) != 1 {
				return fmt.Errorf("usage: %s new <name> [--module path] [--with capability,...] [--dir dir] [--dry-run]", progName)
			}
			return nil
		},
		RunE: runNew,
	}
	cmd.Flags().String("module", "", "Go module path (default example.com/<name>)")
	cmd.Flags().StringSlice("with", nil, "also install these capabilities and their requirements (comma-separated or repeated)")
	return cmd
}

// newAddCommand builds `weld add`, whose `module` and `command` subcommands are
// separate from the additive capability set.
func newAddCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "add <capability> [capability...]",
		Short: "additively extend an existing project in place",
		Long:  addHelp(),
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) < 1 {
				return fmt.Errorf("usage: %s add <capability> [capability...] [--with-dependencies] [--dir dir] [--dry-run]\n       %s add seeder <name> [--with-dependencies] [--dir dir] [--dry-run]\n       %s add module <name> [--command] [--with-dependencies] [--dir dir] [--dry-run]\n       %s add command <name> [--with-dependencies] [--dir dir] [--dry-run]\n       %s add enum <name> <value> [value...] [--dir dir] [--dry-run]", progName, progName, progName, progName, progName)
			}
			return nil
		},
		RunE: runAdd,
	}
	cmd.Flags().Bool("with-dependencies", false, "install missing required capabilities instead of failing")
	cmd.AddCommand(newAddModuleCommand(), newAddCrudSubcommand(), newAddCommandSubcommand(), newAddSeederSubcommand(), newAddEnumCommand())
	return cmd
}

// addHelp lists the embedded catalog rather than maintaining a second manual
// capability list that could go stale when a template capability is added.
func addHelp() string {
	var b strings.Builder
	b.WriteString("Add one or more capabilities to an existing project.\n\nAvailable capabilities:\n")
	catalog := template.Load()
	names, err := catalog.Names()
	if err != nil {
		return b.String() + "  (catalog unavailable)\n"
	}
	for _, name := range names {
		capability, err := catalog.Get(name)
		if err != nil || capability.Kind != template.KindAdd {
			continue
		}
		fmt.Fprintf(&b, "  %-9s %s\n", name, capability.Summary)
	}
	b.WriteString("\nSeveral capabilities can be added in one command (`weld add loom http redis`);")
	b.WriteString("\nthey share one plan, so a requirement they have in common is installed once,")
	b.WriteString("\nand naming more than one implies --with-dependencies.")
	b.WriteString("\n\nA single capability's required capabilities are not installed automatically: a")
	b.WriteString("\nmissing requirement is an error unless --with-dependencies is given.")
	b.WriteString("\n\n`weld add crud <name> [field:type...]` writes a database-backed resource: a")
	b.WriteString("\nmigration, sqlc queries, a repository and a list/get/create/update/delete")
	b.WriteString("\nmodule, then runs sqlc, Loom and go build, rolling back if any step fails.")
	b.WriteString("\n\n`weld add seeder <name>` writes internal/data/seeder/seed_<name>.go and")
	b.WriteString("\nregisters it in the seed capability's register.go index.")
	b.WriteString("\n\n`weld add enum <name> <value>...` writes internal/enums/<name>.go (the type and")
	b.WriteString("\nits string constants, written once) and generates internal/enums/<name>_gen.go")
	b.WriteString("\n(the derived methods). <name> is lower_snake_case and the exported type is its")
	b.WriteString("\nCamelCase form (config_key -> ConfigKey). Run `weld gen enum` to refresh the")
	b.WriteString("\ngenerated file after editing the constants.")
	b.WriteString("\n\nRun `weld list` to see installed capabilities in this project.")
	return b.String()
}

// newAddModuleCommand builds `weld add module <name>`.
func newAddModuleCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "module <name>",
		Short: "add an HTTP business module under internal/modules/<name>",
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) != 1 {
				return fmt.Errorf("usage: %s add module <name> [--command] [--dir dir] [--dry-run]", progName)
			}
			return nil
		},
		RunE: runAddModule,
	}
	cmd.Flags().Bool("command", false, "also add a root command group backed by the module's Service")
	cmd.Flags().Bool("with-dependencies", false, "install missing required capabilities instead of failing")
	return cmd
}

// newAddCommandSubcommand builds `weld add command <name>`, which installs an
// independent root command group with no HTTP, configuration or database
// requirement.
func newAddCommandSubcommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "command <name>",
		Short: "add an independent root command group under internal/commands/<name>",
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) != 1 {
				return fmt.Errorf("usage: %s add command <name> [--with-dependencies] [--dir dir] [--dry-run]", progName)
			}
			return nil
		},
		RunE: runAddCommand,
	}
	cmd.Flags().Bool("with-dependencies", false, "install missing required capabilities instead of failing")
	return cmd
}

// newAddSeederSubcommand builds `weld add seeder <name>`, which writes
// internal/data/seeder/seed_<name>.go and registers it in the seed capability's
// stable register.go index.
func newAddSeederSubcommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "seeder <name>",
		Short: "add a database seeder under internal/data/seeder",
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) != 1 {
				return fmt.Errorf("usage: %s add seeder <name> [--with-dependencies] [--dir dir] [--dry-run]", progName)
			}
			return nil
		},
		RunE: runAddSeeder,
	}
	cmd.Flags().Bool("with-dependencies", false, "install missing required capabilities instead of failing")
	return cmd
}

// runAddSeeder plans and applies `weld add seeder <name>`.
func runAddSeeder(cmd *cobra.Command, args []string) error {
	stdout := cmd.OutOrStdout()
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
	result, err := scaffold.AddSeeder(scaffold.Request{Dir: dir, Catalog: template.Load(), WithDependencies: withDependencies}, args[0])
	if err != nil {
		return err
	}
	return applyAddResult(stdout, result, dryRun)
}

// newAddEnumCommand builds `weld add enum <name> <value>...`, which writes a
// typed string enum under internal/enums and generates its methods.
func newAddEnumCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "enum <name> <value> [value...]",
		Short: "add a typed string enum under internal/enums",
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) < 2 {
				return fmt.Errorf("usage: %s add enum <name> <value> [value...] [--dir dir] [--dry-run]", progName)
			}
			return nil
		},
		RunE: runAddEnum,
	}
	return cmd
}

// runAddEnum plans and applies `weld add enum <name> <value>...`.
func runAddEnum(cmd *cobra.Command, args []string) error {
	stdout := cmd.OutOrStdout()
	dir, err := cmd.Flags().GetString("dir")
	if err != nil {
		return err
	}
	dryRun, err := cmd.Flags().GetBool("dry-run")
	if err != nil {
		return err
	}
	result, err := scaffold.AddEnum(scaffold.Request{Dir: dir, Catalog: template.Load()}, args[0], args[1:])
	if err != nil {
		return err
	}
	return applyAddResult(stdout, result, dryRun)
}

// newGenCommand builds `weld gen`, the umbrella for the project's code
// generators. A bare invocation runs every generator that applies; the `enum`,
// `loom` and `sqlc` subcommands select one.
func newGenCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "gen",
		Short: "regenerate the project's generated code",
		Long:  genHelp(),
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) != 0 {
				return fmt.Errorf("usage: %s gen [enum|loom|sqlc] [--dir dir] [--dry-run]", progName)
			}
			return nil
		},
		RunE: runGen,
	}
	cmd.AddCommand(newGenEnumCommand(), newGenLoomCommand(), newGenSQLCCommand())
	return cmd
}

// genHelp explains the generator surface.
func genHelp() string {
	return `Regenerate the project's generated code.

A bare ` + progName + ` gen runs every generator that applies to the project:

  enum   regenerate internal/enums/*_gen.go from the enum types and constants,
         so editing a value and running ` + progName + ` gen enum refreshes the derived methods
  loom   rebuild internal/di/loom_gen.go with the generator pinned in tools/loom
         (the same generator make generate runs, without the make fallback)
  sqlc   regenerate internal/data/sqlc from db/migrations and db/query with the
         generator pinned in db/tools (the same generator make sqlc runs)

The enum generator reads the human-owned internal/enums/*.go files (the type and
its constants) and writes one <name>_gen.go per enum. It never rewrites the
definition file. ` + progName + ` gen enum --check reports stale files without writing,
for CI; --prune deletes a generated file whose enum no longer exists.

The sqlc generator only applies when the db capability is installed.
` + progName + ` gen sqlc --check compares the committed generated files with the
pinned generator and fails instead of writing when they are stale.`
}

// runGen runs every generator that applies to the project.
func runGen(cmd *cobra.Command, args []string) error {
	stdout := cmd.OutOrStdout()
	dir, err := cmd.Flags().GetString("dir")
	if err != nil {
		return err
	}
	dryRun, err := cmd.Flags().GetBool("dry-run")
	if err != nil {
		return err
	}

	ran := false
	if _, err := os.Stat(filepath.Join(dir, "internal", "enums")); err == nil {
		if err := genEnums(stdout, dir, false, false, dryRun); err != nil {
			return err
		}
		ran = true
	}

	manifest, err := project.Load(dir)
	if err != nil {
		return err
	}
	if manifest.HasCapability("loom") {
		if err := genLoom(stdout, dir, dryRun); err != nil {
			return err
		}
		ran = true
	}
	if manifest.HasCapability("db") {
		if err := genSQLC(stdout, cmd.ErrOrStderr(), dir, false, dryRun); err != nil {
			return err
		}
		ran = true
	}
	if !ran {
		fmt.Fprintln(stdout, "Nothing to generate in this project.")
	}
	return nil
}

// newGenEnumCommand builds `weld gen enum`.
func newGenEnumCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "enum",
		Short: "regenerate internal/enums/*_gen.go from the enum definitions",
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) != 0 {
				return fmt.Errorf("usage: %s gen enum [--check] [--prune] [--dir dir] [--dry-run]", progName)
			}
			return nil
		},
		RunE: runGenEnum,
	}
	cmd.Flags().Bool("check", false, "report stale generated files and fail instead of writing")
	cmd.Flags().Bool("prune", false, "delete a generated file whose enum no longer exists")
	return cmd
}

// runGenEnum plans and applies `weld gen enum`.
func runGenEnum(cmd *cobra.Command, args []string) error {
	stdout := cmd.OutOrStdout()
	dir, err := cmd.Flags().GetString("dir")
	if err != nil {
		return err
	}
	dryRun, err := cmd.Flags().GetBool("dry-run")
	if err != nil {
		return err
	}
	prune, err := cmd.Flags().GetBool("prune")
	if err != nil {
		return err
	}
	check, err := cmd.Flags().GetBool("check")
	if err != nil {
		return err
	}
	return genEnums(stdout, dir, prune, check, dryRun)
}

// genEnums regenerates the enum methods. With check it writes nothing and
// returns an error naming every stale file; otherwise it applies the plan
// unless dryRun is set.
func genEnums(stdout io.Writer, dir string, prune, check, dryRun bool) error {
	if check {
		stale, err := scaffold.CheckEnums(scaffold.Request{Dir: dir})
		if err != nil {
			return err
		}
		if len(stale) > 0 {
			return fmt.Errorf("generated enum files are out of date:\n  %s\nrun: %s gen enum", strings.Join(stale, "\n  "), progName)
		}
		fmt.Fprintln(stdout, "generated enum files are up to date.")
		return nil
	}

	result, err := scaffold.GenEnums(scaffold.Request{Dir: dir, Catalog: template.Load()}, prune)
	if err != nil {
		return err
	}
	if dryRun {
		printResult(stdout, result, true)
		return nil
	}
	if err := result.Apply(); err != nil {
		return err
	}
	printResult(stdout, result, false)
	return nil
}

// newGenLoomCommand builds `weld gen loom`.
func newGenLoomCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "loom",
		Short: "rebuild internal/di/loom_gen.go with the pinned generator",
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) != 0 {
				return fmt.Errorf("usage: %s gen loom [--dir dir] [--dry-run]", progName)
			}
			return nil
		},
		RunE: runGenLoom,
	}
}

// runGenLoom rebuilds the Loom initializer with the pinned generator.
func runGenLoom(cmd *cobra.Command, args []string) error {
	stdout := cmd.OutOrStdout()
	dir, err := cmd.Flags().GetString("dir")
	if err != nil {
		return err
	}
	dryRun, err := cmd.Flags().GetBool("dry-run")
	if err != nil {
		return err
	}
	return genLoom(stdout, dir, dryRun)
}

// genLoom regenerates internal/di/loom_gen.go with the pinned generator. It is
// a no-op note when Loom is not installed.
func genLoom(stdout io.Writer, dir string, dryRun bool) error {
	manifest, err := project.Load(dir)
	if err != nil {
		return err
	}
	if !manifest.HasCapability("loom") {
		fmt.Fprintln(stdout, "loom is not installed; nothing to generate.")
		return nil
	}
	if _, err := os.Stat(filepath.Join(dir, "tools", "loom")); err != nil {
		return fmt.Errorf("tools/loom is missing; reinstall with: %s add loom", progName)
	}
	if dryRun {
		fmt.Fprintln(stdout, "Plan (1 change(s), dry run):\n  update internal/di/loom_gen.go")
		return nil
	}
	if err := loomgen.Generate(dir, "internal/di"); err != nil {
		return err
	}
	fmt.Fprintln(stdout, "Regenerated internal/di/loom_gen.go")
	return nil
}

// newGenSQLCCommand builds `weld gen sqlc`.
func newGenSQLCCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sqlc",
		Short: "regenerate internal/data/sqlc from db/migrations and db/query",
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) != 0 {
				return fmt.Errorf("usage: %s gen sqlc [--check] [--dir dir] [--dry-run]", progName)
			}
			return nil
		},
		RunE: runGenSQLC,
	}
	cmd.Flags().Bool("check", false, "report stale generated files and fail instead of writing")
	return cmd
}

// runGenSQLC plans and applies `weld gen sqlc`.
func runGenSQLC(cmd *cobra.Command, args []string) error {
	dir, err := cmd.Flags().GetString("dir")
	if err != nil {
		return err
	}
	dryRun, err := cmd.Flags().GetBool("dry-run")
	if err != nil {
		return err
	}
	check, err := cmd.Flags().GetBool("check")
	if err != nil {
		return err
	}
	return genSQLC(cmd.OutOrStdout(), cmd.ErrOrStderr(), dir, check, dryRun)
}

// genSQLC regenerates internal/data/sqlc with the pinned generator. It is a
// no-op note when the db capability is not installed.
func genSQLC(stdout, stderr io.Writer, dir string, check, dryRun bool) error {
	manifest, err := project.Load(dir)
	if err != nil {
		return err
	}
	if !manifest.HasCapability("db") {
		fmt.Fprintln(stdout, "db is not installed; nothing to generate.")
		return nil
	}
	result, err := db.SQLC(db.SQLCRequest{
		Dir:    dir,
		Check:  check,
		DryRun: dryRun,
		Stdout: stdout,
		Stderr: stderr,
	})
	if err != nil {
		return err
	}
	if dryRun {
		label := "generator"
		if check {
			label = "check"
		}
		fmt.Fprintf(stdout, "Plan (1 %s, dry run):\n  %s\n", label, result.Step)
		return nil
	}
	if check {
		fmt.Fprintln(stdout, "generated sqlc files are up to date.")
		return nil
	}
	fmt.Fprintf(stdout, "Ran 1 generator (sqlc):\n  %s\n", result.Step)
	return nil
}

// newUpgradeCommand builds `weld upgrade`.
func newUpgradeCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "upgrade [capability...]",
		Short: "move installed capabilities to this weld's template versions",
		Long:  upgradeHelp(),
		RunE:  runUpgrade,
	}
	cmd.Flags().Bool("check", false, "report what is out of date and fail instead of writing")
	cmd.Flags().Bool("prune", false, "delete unmodified files a newer payload no longer ships")
	cmd.Flags().String("verify", "vet", "check the project after writing: vet, build or none")
	return cmd
}

// upgradeHelp explains what `weld upgrade` does and, just as importantly, what
// it refuses to do.
func upgradeHelp() string {
	return `Move installed capabilities to the template versions this weld ships.

A capability records the version that wrote its files in weld.json, and every
managed file records the sha256 of what weld last wrote. Every file also has a
role, and the role decides what an upgrade may do with it:

  - a file weld owns outright is rewritten while the project has not changed it,
    and reported with a unified diff when it has;
  - a shared file (one that carries marker regions another capability extends,
    for example internal/config/config.go) is moved forward by rendering the
    newer payload and splicing the live marker regions into it, so a capability's
    snippet and your own settings inside a region both survive;
  - a project-owned seam is never written. internal/httpserver/middleware.go,
    internal/httpserver/policy.go, the *_providers.go and register.go files, the
    Makefile and config.yml are yours after weld writes them once, so a payload
    change there is reported with a diff for you to fold in;
  - the generated Loom graph is re-rendered from the installed capability set;
  - go.mod and go.sum belong to the Go toolchain and are never rewritten.

A file you deleted is reported, never resurrected. A file a newer payload no
longer ships is reported, and deleted only with --prune, only when it is a plain
payload file and only when it is unmodified.

With no arguments every installed capability is considered. Naming capabilities
limits the run, and a name that is not installed is an error.

After writing the plan, ` + progName + ` runs a check inside the project and rolls
the whole upgrade back if it fails, so an upgrade can never leave a project that
does not build. --verify selects the check:

  --verify=vet     go vet ./... (the default: it compiles the tests too)
  --verify=build   go build ./...
  --verify=none    write without checking

The check compares diagnostics, not just the outcome, so a project that is already
broken stays upgradable: only a diagnostic the upgrade introduces is a failure.
That is what lets a manual seam migration happen while the payload has not moved.

  ` + progName + ` upgrade                       upgrade everything that is behind
  ` + progName + ` upgrade http                  upgrade one capability
  ` + progName + ` upgrade --dry-run             print the plan, write nothing
  ` + progName + ` upgrade --check               CI: fail when anything is out of date
  ` + progName + ` upgrade --prune               also delete retired, unmodified files
  ` + progName + ` upgrade --verify=build        check with a build instead of vet`
}

// upgradeVerify turns the --verify flag into the go command Apply runs. An empty
// result writes the plan unchecked.
func upgradeVerify(mode string) ([]string, error) {
	switch mode {
	case "vet":
		return []string{"vet", "./..."}, nil
	case "build":
		return []string{"build", "./..."}, nil
	case "none", "":
		return nil, nil
	default:
		return nil, fmt.Errorf("unknown --verify mode %q: use vet, build or none", mode)
	}
}

// runUpgrade plans and, unless it is a check or a dry run, applies an upgrade.
func runUpgrade(cmd *cobra.Command, args []string) error {
	stdout := cmd.OutOrStdout()
	dir, err := cmd.Flags().GetString("dir")
	if err != nil {
		return err
	}
	dryRun, err := cmd.Flags().GetBool("dry-run")
	if err != nil {
		return err
	}
	check, err := cmd.Flags().GetBool("check")
	if err != nil {
		return err
	}
	prune, err := cmd.Flags().GetBool("prune")
	if err != nil {
		return err
	}
	verifyMode, err := cmd.Flags().GetString("verify")
	if err != nil {
		return err
	}
	verify, err := upgradeVerify(verifyMode)
	if err != nil {
		return err
	}

	result, err := scaffold.Upgrade(scaffold.Request{Dir: dir, Catalog: template.Load(), Verify: verify}, args, prune)
	if err != nil {
		return err
	}

	// A check never writes: it fails when weld would change anything at all,
	// including a generated file that drifted, so CI catches staleness rather
	// than only a version gap.
	if check {
		if len(result.Upgraded) == 0 && len(result.Operations) == 0 && len(result.Advisories) == 0 {
			fmt.Fprintln(stdout, "Every installed capability is already at this weld's version.")
			return nil
		}
		printUpgradeResult(stdout, result, upgradeCheck)
		return fmt.Errorf("the project is not up to date with this weld; run: %s upgrade", progName)
	}

	if dryRun {
		printUpgradeResult(stdout, result, upgradeDryRun)
		return nil
	}
	if err := result.Apply(); err != nil {
		return err
	}
	printUpgradeResult(stdout, result, upgradeApplied)
	return nil
}

// printUpgradeResult reports an upgrade: the capabilities that moved, the files
// weld writes, and the changes left for the user.
func printUpgradeResult(stdout io.Writer, result *scaffold.UpgradeResult, mode upgradeMode) {
	printNotes(stdout, result.Result)
	if len(result.Upgraded) > 0 {
		verb := "Upgraded"
		noun := "capabilities"
		if mode != upgradeApplied {
			verb = "Would upgrade"
		}
		if len(result.Upgraded) == 1 {
			noun = "capability"
		}
		fmt.Fprintf(stdout, "%s %d %s:\n", verb, len(result.Upgraded), noun)
		for _, delta := range result.Upgraded {
			fmt.Fprintf(stdout, "  %-8s %s -> %s\n", delta.Name, delta.From, delta.To)
			for _, path := range delta.Files {
				fmt.Fprintf(stdout, "      %s\n", path)
			}
		}
	}
	switch mode {
	case upgradeApplied:
		printOperations(stdout, result.Result, false)
	default:
		printOperationsFor(stdout, result.Result, "Plan (%d change(s), "+string(mode)+"):")
	}
	if len(result.Upgraded) == 0 && len(result.Operations) == 0 {
		fmt.Fprintln(stdout, "Every installed capability is already at this weld's version.")
	}
	printAdvisories(stdout, result.Advisories)
}

// upgradeMode is how an upgrade reports itself: it applied the plan, or it is
// previewing it as a dry run or a check.
type upgradeMode string

const (
	upgradeApplied upgradeMode = "applied"
	upgradeDryRun  upgradeMode = "dry run"
	upgradeCheck   upgradeMode = "check"
)

// printOperationsFor prints the planned operations under a caller-supplied
// header format, so an upgrade can say "dry run" or "check" where an add says
// "Applied".
func printOperationsFor(stdout io.Writer, result *scaffold.Result, header string) {
	if len(result.Operations) == 0 {
		if len(result.Notes) == 0 {
			fmt.Fprintln(stdout, "No changes.")
		}
		return
	}
	fmt.Fprintf(stdout, header+"\n", len(result.Operations))
	for _, operation := range result.Operations {
		action := "add"
		switch {
		case operation.Delete:
			action = "remove"
		case operation.Overwrite:
			action = "update"
		}
		fmt.Fprintf(stdout, "  %s %s\n", action, operation.Path)
	}
}

// printAdvisories prints the files weld refused to write on its own, each with
// the reason and, where one exists, the unified diff of what the newer payload
// implies.
func printAdvisories(stdout io.Writer, advisories []scaffold.Advisory) {
	if len(advisories) == 0 {
		return
	}
	noun := "files need"
	if len(advisories) == 1 {
		noun = "file needs"
	}
	fmt.Fprintf(stdout, "\n%d %s your attention (weld did not write these files):\n", len(advisories), noun)
	for _, advisory := range advisories {
		fmt.Fprintf(stdout, "  %s (%s, %s)\n", advisory.Path, advisory.Capability, advisory.Reason)
		if advisory.Detail != "" {
			fmt.Fprintf(stdout, "    %s\n", advisory.Detail)
		}
		if advisory.Diff == "" {
			continue
		}
		for _, line := range strings.Split(strings.TrimSuffix(advisory.Diff, "\n"), "\n") {
			fmt.Fprintf(stdout, "    %s\n", line)
		}
	}
}

// newListCommand builds `weld list` (alias `ls`).
func newListCommand() *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "show available capabilities and, inside a project, what is installed",
		RunE:    runList,
	}
}

// newSkillsCommand builds `weld skills`.
func newSkillsCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "skills",
		Short: "generate the project's agent skill file",
		RunE:  runSkills,
	}
}

// newVersionCommand builds the version command. The root's --version/-v flag
// prints the same line, so `weld version`, `weld --version` and `weld -v`
// agree.
func newVersionCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "print the version",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprintf(cmd.OutOrStdout(), "%s %s (templates %s)\n", progName, version, weldtemplate.Version)
			return nil
		},
	}
}

// usageLong is the help body. Cobra prints it above the generated usage and
// command list, so it explains the capability and module set rather than the
// command names.
const usageLong = `weld builds a minimal Go CLI and then extends that same project in
place, one capability at a time. It also drives the assembled project:
weld dev runs the backend and frontend together and weld build builds the
frontend and cross-compiles the binary, both configured by the optional,
user-owned weld.yml; weld seed runs the project's seeders behind the seed
build tag, so the built binary carries no seeder data, weld test runs go test
with the dedicated test database configured in config.yml's database_test
section, and weld deploy uploads the built binary to the configured host and
runs its remote script.

Capabilities:
  base   (scaffold) minimal Go CLI on a Cobra command tree with a tint-backed
                    logging.Logger contract (default slog)
  config (add)      typed YAML configuration (config.yml + environment) with a
                    redacting Secret type; installed by http and db
  http   (add)      HTTP lifecycle, shared JSON/middleware toolkit and a serve
                    command
  web    (add)      React + TypeScript + Vite frontend (requires http)
  db     (add)      PostgreSQL: SQL migrations, sqlc queries, an injectable pool
                    and repository
  seed   (add)      Opt-in database seeding: named, idempotent seeders over the
                    repositories the Loom seed graph injects, run by a short
                    seed command and make seed (requires db)
  events (add)      Opt-in in-process event dispatcher: typed Publish/On keyed
                    on the event's concrete Go type, synchronous delivery in
                    registration order, panic isolation and joined listener
                    errors; installing it dispatches nothing until you register
                    a listener in internal/di/events_listeners.go (requires loom)
  redis  (add)      Opt-in Redis client with typed connection config; installing
                    it never connects Redis to a service (requires base, config)
  mail   (add)      Opt-in SMTP sender with typed, secret-redacted config;
                    installing it never sends or connects
  storage (add)     Opt-in S3-compatible object-storage client; installing it
                    creates no bucket and uploads nothing
  jwt    (add)      Opt-in JWT bearer-token authentication: HS256 issue/verify,
                    a secret-redacted config section and net/http middleware that
                    stores validated claims on the request context; installing it
                    issues no token and guards no route (requires http)
  user   (add)      Opt-in user accounts: PostgreSQL users migration, sqlc
                    queries in the single sqlc.yaml, a repository under
                    internal/data, bcrypt registration/login under internal/auth
                    and JWT session issuance; installing it creates no default
                    account, administrator or route and writes no generated
                    code, so run make sqlc before make build (requires db, jwt)
  cron   (add)      Opt-in in-process scheduler that starts and stops with
                    serve; installing it schedules nothing until you register
                    service methods in internal/di/cron_jobs.go. Jobs take a
                    five-field spec or Every/DailyAt/WeeklyOn/MonthlyOn;
                    returned errors are logged automatically
  loom   (add)      Loom dependency-injection graph wiring the HTTP server, the
                    database, Redis, mail, storage and event bindings and the
                    cron scheduler (requires config; opt-in, never installed by
                    web, db, seed, redis, mail, storage, events or cron)

Modules:
  add module <name> generates internal/modules/<name>: a small HTTP business
  module (typed request/response, a Service seam and routes declared once with
  the internal/httpx helpers) under /api/<name>. It requires http (and the loom
  and config http requires); a missing requirement is an error unless
  --with-dependencies is given. It validates the name and records the module
  in weld.json. The generated Loom server graph registers the module on the
  composed mux, whichever is installed first. Its files are written once and
  never regenerated, so your edits survive later adds.
  add module <name> --command additionally generates a root command group in
  internal/commands/<name> backed by the module's own Service interface and
  NewService constructor, registered through internal/app/<name>_command.go.
  Adding it to a module that already exists writes only the command files and
  never rewrites your module service or handler. With Loom installed the group
  carries the module's Service through a stable command graph
  (internal/di/<name>_graph.go) and resolves it lazily on a real subcommand
  through the shared internal/commandkit seam.

Commands:
  add command <name> generates an independent root command group in
  internal/commands/<name> plus its registration in
  internal/app/<name>_command.go, for non-business or multi-business commands.
  It needs no HTTP, configuration or database, starts no server and shows help
  on a bare invocation. Its files are written once and never regenerated; edit
  the registration to inject business services, or the command itself, freely.
  When Loom is installed the group also gets a stable command graph in
  internal/di/<name>_graph.go, and a real subcommand resolves it lazily so
  help and unrelated commands never construct its dependencies; Loom installed
  after the command upgrades it in place without overwriting an edited file.
  To inject a dependency, add a field to the group's Deps and a parameter to
  NewDeps (a provider already in the shared module needs no graph edit).
  A name may be a command or a module, never both: serve, help and version are
  reserved, and a name already used by a module is rejected.

Seeders:
  add seeder <name> writes internal/data/seeder/seed_<name>.go and registers it
  in the seed capability's stable register.go index, so one seeder is one file
  and the run order stays in one place. --with-dependencies installs the
  db/loom/config/seed chain first when seed is not installed.

Enums:
  add enum <name> <value>... writes internal/enums/<name>.go, a typed string
  enumeration (the type and its constants) that weld writes once and never
  rewrites, and immediately generates internal/enums/<name>_gen.go with the
  derived methods (String, the *Values/*Names slices, Parse<Name>, IsValid and
  the JSON/YAML text and database/sql codecs). <name> is lower_snake_case and
  the exported type is its CamelCase form (config_key -> ConfigKey). It requires
  no capability. Add a value by editing the definition file, then run
  'weld gen enum' to refresh the generated methods.

Database:
  db migrate up|status|down runs the goose tool pinned in db/tools against the
  database described by config.yml (database.dsn or the host/port/user/password/
  name fields, with DB_* overrides) or DATABASE_URL, which takes precedence. The
  dsn is passed to goose but never printed; --dry-run shows $DATABASE_URL instead.
  up --allow-missing applies a migration older than the database's current
  version, which is how an installed capability's migration is added to an
  existing database. down reverts exactly one migration and requires --yes or
  CONFIRM=1.
  db create <name> writes db/migrations/<version>_<name>.sql as a goose
  skeleton. The db capability must be installed, and --config names a different
  application configuration file.

Diagnostics:
  doctor runs read-only checks on the manifest, weld.yml, config.yml, managed-file
  drift, go.mod, installed tools, generated Loom and enum files and the db
  migration layout. Warnings are advisory; failures exit non-zero. --json prints
  the report for CI. It never writes, builds or connects to a service.

Generators:
  gen runs every generator that applies to the project. gen enum regenerates
  internal/enums/*_gen.go from the definitions, reads the human-owned files and
  rewrites only the generated ones; --check reports stale files for CI, and
  --prune deletes a generated file whose enum no longer exists. gen loom
  rebuilds internal/di/loom_gen.go with the pinned tools/loom generator (the
  same generator make generate runs, without the make fallback). gen sqlc
  regenerates internal/data/sqlc from db/migrations and db/query with the
  generator pinned in db/tools (the same generator make sqlc runs); --check
  reports stale generated files for CI.

Upgrades:
  upgrade moves installed capabilities to the template versions this weld
  ships. Every file a capability ships has a role: a file weld owns is rewritten
  while it still matches weld.json; a shared file (internal/config/config.go)
  keeps its live marker regions while the newer payload supplies the skeleton; a
  project-owned seam is never rewritten and is reported with a unified diff;
  the generated Loom graph is re-rendered; and go.mod/go.sum stay with the Go
  toolchain. A file you changed or deleted is reported, never overwritten and
  never resurrected; a file the payload no longer ships is deleted only with
  --prune, only when it is a plain payload file and only when it is unmodified.
  After writing, upgrade runs go vet ./... and rolls the whole change back if it
  introduces a diagnostic, so an upgrade can never leave the project broken;
  --verify=build|none selects the check. --dry-run prints the plan without
  writing and --check reports what is out of date for CI. Naming capabilities
  limits the run; a name that is not installed is an error. Every file a
  capability writes is recorded in the project manifest (weld.json) with the
  capability, role and version that produced it, and that record is what makes
  the upgrade safe.

Requirements are not installed silently: weld add <capability> fails when a
required capability is missing and names it, and --with-dependencies installs
the missing chain in the same plan. http and db each require the config
capability, so the first of them installed adds the shared typed configuration
loader and generates a local, git-ignored config.yml from config.example.yml;
the other only appends its section to that file.
web requires http and shares the one serve command. db requires only base (and
config), so it can be added to a CLI-only project and composes with the HTTP
capabilities in any order. seed requires db and adds a short seed command over
the repositories you wire in internal/di/seed_providers.go; it seeds nothing
until you register a seeder. redis likewise requires only base (and config): it
installs a lazy client the caller owns and never wires Redis into a service on
its own. events requires loom and installs a dispatcher that publishes nothing
until you register a listener in internal/di/events_listeners.go. jwt requires
http and installs an HS256 manager and bearer middleware; it issues no token and
guards no route until you inject the manager into NewHTTPPolicies. user requires
db and jwt and adds the users table, a data repository and a bcrypt account
service; it creates no account and mounts no route. loom is required
by every capability beyond the bare CLI, requires
config, regenerates its graph when any capability it feeds is installed, and
raises the project's Go floor to 1.25.

Every file a capability writes is recorded in the project manifest (weld.json)
with the capability, the role and the version that produced it. That record is
what lets weld upgrade move a capability to a newer payload without touching a
file you changed or a seam the project owns. The skills command writes .agents/skills/weld/SKILL.md from that
manifest and refuses to overwrite the file once you edit it.

weld.yml is user-owned: it configures weld build (cross-compilation targets,
build tags, CGO_ENABLED, ldflags), weld dev (the backend and frontend commands),
weld seed (the seeder build tag) and weld deploy (the ssh host, remote paths and
upload target), and weld never rewrites it. It is not the machine-owned weld.json
manifest, and weld add never touches it. weld config example prints a
commented starter and weld config init writes one into the project (existing
files are kept unless --force is given); every setting defaults from the
installed capabilities and the host platform, so a project without weld.yml
still builds and runs.`

// runNew plans and applies `weld new`.
func runNew(cmd *cobra.Command, args []string) error {
	stdout := cmd.OutOrStdout()
	module, err := cmd.Flags().GetString("module")
	if err != nil {
		return err
	}
	dir, err := cmd.Flags().GetString("dir")
	if err != nil {
		return err
	}
	dryRun, err := cmd.Flags().GetBool("dry-run")
	if err != nil {
		return err
	}

	with, err := cmd.Flags().GetStringSlice("with")
	if err != nil {
		return err
	}

	result, err := scaffold.CreateWith(scaffold.Request{
		Dir:     dir,
		Name:    args[0],
		Module:  module,
		Version: version,
		Catalog: template.Load(),
	}, with...)
	if err != nil {
		return err
	}
	if dryRun {
		printResult(stdout, result, true)
		return nil
	}
	if err := result.Apply(); err != nil {
		return err
	}
	printResult(stdout, result, false)
	next := "go run . help"
	if len(with) > 0 {
		next = "make build"
	}
	fmt.Fprintf(stdout, "\nNext:\n  cd %s\n  %s\n", result.Dir, next)
	return nil
}

// runAdd plans and applies `weld add <capability>`.
func runAdd(cmd *cobra.Command, args []string) error {
	stdout := cmd.OutOrStdout()
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
	// Naming several capabilities is already an explicit request for the set, so
	// their shared requirement chain is installed without repeating the opt-in.
	if len(args) > 1 {
		withDependencies = true
	}

	result, err := scaffold.Add(scaffold.Request{Dir: dir, Catalog: template.Load(), WithDependencies: withDependencies}, args...)
	if err != nil {
		return err
	}
	return applyAddResult(stdout, result, dryRun)
}

// runAddModule plans and applies `weld add module <name>` with an optional
// `--command` root command group.
func runAddModule(cmd *cobra.Command, args []string) error {
	stdout := cmd.OutOrStdout()
	dir, err := cmd.Flags().GetString("dir")
	if err != nil {
		return err
	}
	dryRun, err := cmd.Flags().GetBool("dry-run")
	if err != nil {
		return err
	}
	withCommand, err := cmd.Flags().GetBool("command")
	if err != nil {
		return err
	}
	withDependencies, err := cmd.Flags().GetBool("with-dependencies")
	if err != nil {
		return err
	}

	result, err := scaffold.AddModule(scaffold.Request{Dir: dir, Catalog: template.Load(), Command: withCommand, WithDependencies: withDependencies}, args[0])
	if err != nil {
		return err
	}
	return applyAddResult(stdout, result, dryRun)
}

// runAddCommand plans and applies `weld add command <name>`.
func runAddCommand(cmd *cobra.Command, args []string) error {
	stdout := cmd.OutOrStdout()
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

	result, err := scaffold.AddCommand(scaffold.Request{Dir: dir, Catalog: template.Load(), WithDependencies: withDependencies}, args[0])
	if err != nil {
		return err
	}
	return applyAddResult(stdout, result, dryRun)
}

// applyAddResult prints an add plan and, unless it is a dry run, applies it.
func applyAddResult(stdout io.Writer, result *scaffold.Result, dryRun bool) error {
	if dryRun {
		printResult(stdout, result, true)
		return nil
	}
	if err := result.Apply(); err != nil {
		return err
	}
	printResult(stdout, result, false)
	if len(result.Operations) > 0 {
		fmt.Fprintf(stdout, "\nNext:\n  make build\n")
	}
	return nil
}

// runList prints the capability catalog and, inside a project, what is
// installed.
func runList(cmd *cobra.Command, args []string) error {
	stdout := cmd.OutOrStdout()
	dir, err := cmd.Flags().GetString("dir")
	if err != nil {
		return err
	}
	catalog := template.Load()
	names, err := catalog.Names()
	if err != nil {
		return err
	}
	manifest, loadErr := project.Load(dir)

	fmt.Fprintln(stdout, "Available capabilities:")
	for _, name := range names {
		capability, err := catalog.Get(name)
		if err != nil {
			return err
		}
		mark := " "
		if loadErr == nil && manifest.HasCapability(name) {
			mark = "*"
		}
		fmt.Fprintf(stdout, "  %s %-6s v%-7s %-6s %s\n", mark, capability.Name, capability.Version, capability.Kind, capability.Summary)
	}
	if loadErr != nil {
		fmt.Fprintf(stdout, "\nNo weld project in %s (run: %s new <name>)\n", dir, progName)
		return nil
	}
	fmt.Fprintf(stdout, "\nInstalled in %s:\n", manifest.Name)
	behind := false
	baseHint := upgradeHint(catalog, manifest.Base.Name, manifest.Base.Version)
	if baseHint != "" {
		behind = true
	}
	fmt.Fprintf(stdout, "  * base   v%s%s\n", manifest.Base.Version, baseHint)
	for _, capability := range manifest.Capabilities {
		hint := upgradeHint(catalog, capability.Name, capability.Version)
		if hint != "" {
			behind = true
		}
		fmt.Fprintf(stdout, "  * %-6s v%s%s (applied %s)\n", capability.Name, capability.Version, hint, capability.AppliedAt)
	}
	if len(manifest.Modules) > 0 {
		fmt.Fprintf(stdout, "\nModules in %s:\n", manifest.Name)
		for _, module := range manifest.Modules {
			fmt.Fprintf(stdout, "  * %-8s v%s (applied %s)\n", module.Name, module.Version, module.AppliedAt)
		}
	}
	if len(manifest.Commands) > 0 {
		fmt.Fprintf(stdout, "\nCommands in %s:\n", manifest.Name)
		for _, command := range manifest.Commands {
			kind := "independent"
			if manifest.HasModule(command.Name) {
				kind = "module-backed"
			}
			fmt.Fprintf(stdout, "  * %-8s v%s (%s, applied %s)\n", command.Name, command.Version, kind, command.AppliedAt)
		}
	}
	if drift := manifest.Drift(dir); len(drift) > 0 {
		fmt.Fprintf(stdout, "\n%d managed file(s) changed since install:\n", len(drift))
		for _, item := range drift {
			fmt.Fprintf(stdout, "  %s (%s)\n", item.Path, item.Reason)
		}
	}
	if behind {
		fmt.Fprintf(stdout, "\nThis weld ships a newer payload for an installed capability (run: %s upgrade)\n", progName)
	}
	return nil
}

// upgradeHint renders the "(vX available)" suffix for an installed capability
// this weld build ships a newer version of. It is silent for a capability this
// weld does not ship, or one whose recorded version cannot be compared.
func upgradeHint(catalog *template.Catalog, name, installed string) string {
	capability, err := catalog.Get(name)
	if err != nil {
		return ""
	}
	newer, err := semver.IsNewer(capability.Version, installed)
	if err != nil || !newer {
		return ""
	}
	return fmt.Sprintf(" (v%s available)", capability.Version)
}

// runSkills generates or refreshes the project's agent skill file.
func runSkills(cmd *cobra.Command, args []string) error {
	stdout := cmd.OutOrStdout()
	dir, err := cmd.Flags().GetString("dir")
	if err != nil {
		return err
	}
	dryRun, err := cmd.Flags().GetBool("dry-run")
	if err != nil {
		return err
	}

	manifest, err := project.Load(dir)
	if err != nil {
		return err
	}
	result, err := skills.Generate(dir, manifest, template.Load())
	if err != nil {
		return err
	}
	if result.Changed {
		action := "add"
		if result.Existing {
			action = "update"
		}
		if dryRun {
			fmt.Fprintf(stdout, "Plan (1 change(s), dry run):\n  %s %s\n", action, result.Path)
			return nil
		}
		if err := result.Apply(dir); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "Applied 1 change(s):\n  %s %s\n", action, result.Path)
		return nil
	}
	fmt.Fprintf(stdout, "%s is up to date.\n", result.Path)
	return nil
}

func printResult(stdout io.Writer, result *scaffold.Result, dryRun bool) {
	printNotes(stdout, result)
	printOperations(stdout, result, dryRun)
}

// printNotes prints the plan's human-readable observations.
func printNotes(stdout io.Writer, result *scaffold.Result) {
	for _, note := range result.Notes {
		fmt.Fprintln(stdout, note)
	}
}

// printOperations prints the planned file operations. An empty plan says "No
// changes." unless a note already explained the state.
func printOperations(stdout io.Writer, result *scaffold.Result, dryRun bool) {
	if len(result.Operations) == 0 {
		if len(result.Notes) == 0 {
			fmt.Fprintln(stdout, "No changes.")
		}
		return
	}
	if dryRun {
		fmt.Fprintf(stdout, "Plan (%d change(s), dry run):\n", len(result.Operations))
	} else {
		fmt.Fprintf(stdout, "Applied %d change(s):\n", len(result.Operations))
	}
	for _, operation := range result.Operations {
		action := "add"
		switch {
		case operation.Delete:
			action = "remove"
		case operation.Overwrite:
			action = "update"
		}
		fmt.Fprintf(stdout, "  %s %s\n", action, operation.Path)
	}
}
