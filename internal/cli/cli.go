// Package cli implements the weld command line.
package cli

import (
	"flag"
	"fmt"
	"io"
	"strings"

	weldtemplate "github.com/Xwudao/weld-template"
	"github.com/Xwudao/weld/internal/project"
	"github.com/Xwudao/weld/internal/scaffold"
	"github.com/Xwudao/weld/internal/skills"
	"github.com/Xwudao/weld/internal/template"
)

const (
	progName = "weld"
	version  = "0.1.0"
)

// Run executes a weld invocation and returns a non-nil error on failure.
func Run(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		printUsage(stdout)
		return nil
	}
	switch args[0] {
	case "new":
		return runNew(args[1:], stdout)
	case "add":
		return runAdd(args[1:], stdout)
	case "list", "ls":
		return runList(args[1:], stdout)
	case "skills":
		return runSkills(args[1:], stdout)
	case "version", "--version", "-v":
		fmt.Fprintf(stdout, "%s %s (templates %s)\n", progName, version, weldtemplate.Version)
		return nil
	case "help", "--help", "-h":
		printUsage(stdout)
		return nil
	default:
		return fmt.Errorf("unknown command %q (run: %s help)", args[0], progName)
	}
}

func printUsage(w io.Writer) {
	fmt.Fprintf(w, `%s - progressive Go scaffold

Usage:
  %s new <name> [--module path] [--dir dir] [--dry-run]
      Create a minimal Go CLI project.

  %s add <capability> [--dir dir] [--dry-run]
      Additively extend an existing project in place.

  %s add module <name> [--dir dir] [--dry-run]
      Add an HTTP business module under internal/modules/<name> wired to
      /api/<name>; installs http and config automatically when absent.

  %s list [--dir dir]
      Show available capabilities and, when inside a project, what is installed.

  %s skills [--dir dir] [--dry-run]
      Generate the project's .agents/skills/weld/SKILL.md, a project-specific
      guide to the installed capabilities for coding agents.

  %s version
  %s help

Capabilities:
  base   (scaffold) minimal, dependency-free Go CLI with a log/slog factory
  config (add)      typed YAML configuration (config.yml + environment) with a
                    redacting Secret type; installed by http and db
  http   (add)      HTTP lifecycle, composable mux and a serve command
  web    (add)      React + TypeScript + Vite frontend (requires http)
  api    (add)      JSON API, go-validate rules and an OpenAPI 3.1 document
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
file once you edit it.
`, progName, progName, progName, progName, progName, progName, progName, progName)
}

func runNew(args []string, stdout io.Writer) error {
	flags := flag.NewFlagSet("new", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	module := flags.String("module", "", "Go module path (default example.com/<name>)")
	dir := flags.String("dir", ".", "directory to create the project in")
	dryRun := flags.Bool("dry-run", false, "print the plan without writing")
	positional, err := parseArgs(flags, args)
	if err != nil {
		return err
	}
	if len(positional) != 1 {
		return fmt.Errorf("usage: %s new <name> [--module path] [--dir dir] [--dry-run]", progName)
	}

	result, err := scaffold.Create(scaffold.Request{
		Dir:     *dir,
		Name:    positional[0],
		Module:  *module,
		Version: version,
		Catalog: template.Load(),
	})
	if err != nil {
		return err
	}
	if *dryRun {
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

func runAdd(args []string, stdout io.Writer) error {
	flags := flag.NewFlagSet("add", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	dir := flags.String("dir", ".", "project directory")
	dryRun := flags.Bool("dry-run", false, "print the plan without writing")
	positional, err := parseArgs(flags, args)
	if err != nil {
		return err
	}
	var result *scaffold.Result
	switch {
	case len(positional) == 2 && positional[0] == "module":
		result, err = scaffold.AddModule(scaffold.Request{Dir: *dir, Catalog: template.Load()}, positional[1])
	case len(positional) == 1 && positional[0] != "module":
		result, err = scaffold.Add(scaffold.Request{Dir: *dir, Catalog: template.Load()}, positional[0])
	default:
		return fmt.Errorf("usage: %s add <capability> [--dir dir] [--dry-run]\n       %s add module <name> [--dir dir] [--dry-run]", progName, progName)
	}
	if err != nil {
		return err
	}
	if *dryRun {
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

func runList(args []string, stdout io.Writer) error {
	flags := flag.NewFlagSet("list", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	dir := flags.String("dir", ".", "project directory")
	if _, err := parseArgs(flags, args); err != nil {
		return err
	}
	catalog := template.Load()
	names, err := catalog.Names()
	if err != nil {
		return err
	}
	manifest, loadErr := project.Load(*dir)

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
		fmt.Fprintf(stdout, "\nNo weld project in %s (run: %s new <name>)\n", *dir, progName)
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
	if drift := manifest.Drift(*dir); len(drift) > 0 {
		fmt.Fprintf(stdout, "\n%d managed file(s) changed since install:\n", len(drift))
		for _, item := range drift {
			fmt.Fprintf(stdout, "  %s (%s)\n", item.Path, item.Reason)
		}
	}
	return nil
}

func runSkills(args []string, stdout io.Writer) error {
	flags := flag.NewFlagSet("skills", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	dir := flags.String("dir", ".", "project directory")
	dryRun := flags.Bool("dry-run", false, "print the plan without writing")
	if _, err := parseArgs(flags, args); err != nil {
		return err
	}

	manifest, err := project.Load(*dir)
	if err != nil {
		return err
	}
	result, err := skills.Generate(*dir, manifest, template.Load())
	if err != nil {
		return err
	}
	if result.Changed {
		action := "add"
		if result.Existing {
			action = "update"
		}
		if *dryRun {
			fmt.Fprintf(stdout, "Plan (1 change(s), dry run):\n  %s %s\n", action, result.Path)
			return nil
		}
		if err := result.Apply(*dir); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "Applied 1 change(s):\n  %s %s\n", action, result.Path)
		return nil
	}
	fmt.Fprintf(stdout, "%s is up to date.\n", result.Path)
	return nil
}

// parseArgs parses flags even when they follow positional arguments, which the
// standard flag package does not support on its own. It returns the positional
// arguments.
func parseArgs(flags *flag.FlagSet, args []string) ([]string, error) {
	var flagArgs, positional []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--":
			positional = append(positional, args[i+1:]...)
			return tails(flags, flagArgs, positional)
		case strings.HasPrefix(arg, "-") && arg != "-":
			flagArgs = append(flagArgs, arg)
			name := strings.TrimLeft(arg, "-")
			if strings.ContainsRune(name, '=') {
				continue
			}
			spec := flags.Lookup(name)
			if spec == nil {
				continue // let Parse report the unknown flag
			}
			if boolFlag, ok := spec.Value.(interface{ IsBoolFlag() bool }); ok && boolFlag.IsBoolFlag() {
				continue
			}
			if i+1 < len(args) {
				i++
				flagArgs = append(flagArgs, args[i])
			}
		default:
			positional = append(positional, arg)
		}
	}
	return tails(flags, flagArgs, positional)
}

func tails(flags *flag.FlagSet, flagArgs, positional []string) ([]string, error) {
	if err := flags.Parse(flagArgs); err != nil {
		return nil, err
	}
	return positional, nil
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
