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
	"strings"

	"github.com/spf13/cobra"

	"github.com/Xwudao/weld/internal/project"
	"github.com/Xwudao/weld/internal/scaffold"
	"github.com/Xwudao/weld/internal/skills"
	"github.com/Xwudao/weld/internal/template"
	weldtemplate "github.com/Xwudao/weld/internal/weldtemplate"
)

const (
	progName = "weld"
	version  = "0.1.1"
)

// Run executes a weld invocation and returns a non-nil error on failure. It
// neither prints the error nor exits: main reports a returned error once and
// sets the exit status, so the command tree stays silent here.
func Run(args []string, stdout, stderr io.Writer) error {
	root := newRootCommand()
	root.SetArgs(args)
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
	root.AddCommand(
		newNewCommand(),
		newAddCommand(),
		newListCommand(),
		newSkillsCommand(),
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
				return fmt.Errorf("usage: %s new <name> [--module path] [--dir dir] [--dry-run]", progName)
			}
			return nil
		},
		RunE: runNew,
	}
	cmd.Flags().String("module", "", "Go module path (default example.com/<name>)")
	return cmd
}

// newAddCommand builds `weld add`, whose `module` and `command` subcommands are
// separate from the additive capability set.
func newAddCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "add <capability>",
		Short: "additively extend an existing project in place",
		Long:  addHelp(),
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) != 1 {
				return fmt.Errorf("usage: %s add <capability> [--dir dir] [--dry-run]\n       %s add module <name> [--command] [--dir dir] [--dry-run]\n       %s add command <name> [--dir dir] [--dry-run]", progName, progName, progName)
			}
			return nil
		},
		RunE: runAdd,
	}
	cmd.AddCommand(newAddModuleCommand(), newAddCommandSubcommand())
	return cmd
}

// addHelp lists the embedded catalog rather than maintaining a second manual
// capability list that could go stale when a template capability is added.
func addHelp() string {
	var b strings.Builder
	b.WriteString("Add a capability to an existing project.\n\nAvailable capabilities:\n")
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
	b.WriteString("\nRun `weld list` to see installed capabilities in this project.")
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
	return cmd
}

// newAddCommandSubcommand builds `weld add command <name>`, which installs an
// independent root command group with no HTTP, configuration or database
// requirement.
func newAddCommandSubcommand() *cobra.Command {
	return &cobra.Command{
		Use:   "command <name>",
		Short: "add an independent root command group under internal/commands/<name>",
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) != 1 {
				return fmt.Errorf("usage: %s add command <name> [--dir dir] [--dry-run]", progName)
			}
			return nil
		},
		RunE: runAddCommand,
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
place, one capability at a time.

Capabilities:
  base   (scaffold) minimal Go CLI on a Cobra command tree with a log/slog
                    factory
  config (add)      typed YAML configuration (config.yml + environment) with a
                    redacting Secret type; installed by http and db
  http   (add)      HTTP lifecycle, shared JSON/middleware toolkit and a serve
                    command
  web    (add)      React + TypeScript + Vite frontend (requires http)
  api    (add)      JSON API with a {code,msg,data} envelope, go-validate rules
                    and an OpenAPI 3.1 document
  db     (add)      PostgreSQL: SQL migrations, sqlc queries, an injectable pool
                    and repository
  redis  (add)      Opt-in Redis client with typed connection config; installing
                    it never connects Redis to a service (requires base, config)
  mail   (add)      Opt-in SMTP sender with typed, secret-redacted config;
                    installing it never sends or connects
  storage (add)     Opt-in S3-compatible object-storage client; installing it
                    creates no bucket and uploads nothing
  cron   (add)      Opt-in in-process scheduler that starts and stops with
                    serve; installing it schedules nothing until you register
                    jobs in internal/cron/register.go
  loom   (add)      Loom dependency-injection graph wiring the HTTP server, the
                    JSON API service, the database, Redis, mail, storage and
                    the cron scheduler (requires http; opt-in, never installed
                    by web, api, db, redis, mail, storage or cron)

Modules:
  add module <name> generates internal/modules/<name>: a small HTTP business
  module (typed request/response, a Service seam and a handler under
  /api/<name>) plus its route wiring. It installs http and config automatically
  when absent, validates the name, and records the module in weld.json. Without
  Loom it registers through the weld:routes extension point; with Loom the
  generated graph picks it up, whichever is installed first. Its files are
  written once and never regenerated, so your edits survive later adds.
  add module <name> --command additionally generates a root command group in
  internal/commands/<name> backed by the module's own Service interface and
  NewService constructor, registered through internal/app/<name>_command.go.
  Adding it to a module that already exists writes only the command files and
  never rewrites your module service or handler. With Loom installed the group
  carries the module's Service through a stable command graph
  (internal/di/<name>_graph.go) and resolves it lazily on a real subcommand.

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
  A name may be a command or a module, never both: serve, help and version are
  reserved, and a name already used by a module is rejected.

http and db each install the config capability, so the first of them adds the
shared typed configuration loader and generates a local, git-ignored config.yml
from config.example.yml; adding the other only appends its section to that file.
web and api are independent and both require http: each can be added first and
both share the one serve command. db requires only base (and config), so it can
be added to a CLI-only project and composes with the HTTP capabilities in any
order. redis likewise requires only base (and config): it installs a lazy client
the caller owns and never wires Redis into a service on its own. loom is
opt-in: it requires http and regenerates its graph when web, api, db or redis is
installed, and it raises the project's Go floor to 1.25.

Every file a capability writes is recorded in the project manifest (weld.json)
with the capability and version that produced it. The skills command writes
.agents/skills/weld/SKILL.md from that manifest and refuses to overwrite the
file once you edit it.`

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

	result, err := scaffold.Create(scaffold.Request{
		Dir:     dir,
		Name:    args[0],
		Module:  module,
		Version: version,
		Catalog: template.Load(),
	})
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
	fmt.Fprintf(stdout, "\nNext:\n  cd %s\n  go run . help\n", result.Dir)
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

	result, err := scaffold.Add(scaffold.Request{Dir: dir, Catalog: template.Load()}, args[0])
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

	result, err := scaffold.AddModule(scaffold.Request{Dir: dir, Catalog: template.Load(), Command: withCommand}, args[0])
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

	result, err := scaffold.AddCommand(scaffold.Request{Dir: dir, Catalog: template.Load()}, args[0])
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
	fmt.Fprintf(stdout, "  * base   v%s\n", manifest.Base.Version)
	for _, capability := range manifest.Capabilities {
		fmt.Fprintf(stdout, "  * %-6s v%s (applied %s)\n", capability.Name, capability.Version, capability.AppliedAt)
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
	return nil
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
	for _, note := range result.Notes {
		fmt.Fprintln(stdout, note)
	}
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
		if operation.Overwrite {
			action = "update"
		}
		fmt.Fprintf(stdout, "  %s %s\n", action, operation.Path)
	}
}
