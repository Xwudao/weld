package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Xwudao/weld/internal/hook"
	"github.com/Xwudao/weld/internal/project"
	"github.com/Xwudao/weld/internal/projectcfg"
)

// hooksExecute is the hook execution seam. It is a package variable so a test
// observes the Request without running a shell; production runs hook.Run.
var hooksExecute = hook.Run

// runHooks runs the hooks declared for event in weld.yml. It is a no-op when the
// project has no weld.yml, when hooks are disabled, or when the run opts out
// with --no-hooks / WELD_NO_HOOKS=1.
//
// extraEnv carries command-specific values (WELD_BUILD_OUTPUT, WELD_DEPLOY_HOST,
// ...) into the hook environment on top of the base WELD_* variables.
//
// dryRun plans the hooks without running them. It is true only for the commands
// that already honour --dry-run, so a command that ignores the flag (build, dev,
// seed) still runs its hooks rather than silently skipping them.
func runHooks(cmd *cobra.Command, cfg *projectcfg.Config, event hook.Event, dryRun bool, extraEnv ...string) error {
	if cfg == nil || hooksDisabled(cmd) {
		return nil
	}
	dir, err := cmd.Flags().GetString("dir")
	if err != nil {
		return err
	}

	name := ""
	var capabilities []string
	if manifest, loadErr := project.Load(dir); loadErr == nil {
		name = manifest.Name
		capabilities = manifestCapabilities(manifest)
	}

	result, err := hooksExecute(hook.Request{
		Event:        event,
		Hooks:        &cfg.Hooks,
		Dir:          dir,
		Project:      name,
		Capabilities: capabilities,
		Env:          append(baseHookEnv(dir, name, event), extraEnv...),
		Stdout:       cmd.OutOrStdout(),
		Stderr:       cmd.ErrOrStderr(),
		DryRun:       dryRun,
	})
	if err != nil {
		return err
	}
	if dryRun {
		printHookPlan(cmd.OutOrStdout(), event, result)
	}
	return nil
}

// hooksDisabled reports whether this run opted out of hooks, by flag or by
// environment. WELD_NO_HOOKS is for CI, where the flag is easy to forget.
func hooksDisabled(cmd *cobra.Command) bool {
	if noHooks, err := cmd.Flags().GetBool("no-hooks"); err == nil && noHooks {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(os.Getenv("WELD_NO_HOOKS"))) {
	case "", "0", "false", "no", "off":
		return false
	default:
		return true
	}
}

// baseHookEnv returns the WELD_* variables every hook sees, so a hook can be
// written once and stay generic.
func baseHookEnv(dir, name string, event hook.Event) []string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		abs = dir
	}
	env := []string{
		"WELD_COMMAND=" + event.Command(),
		"WELD_EVENT=" + string(event),
		"WELD_DIR=" + abs,
		"WELD_VERSION=" + version,
	}
	if name != "" {
		env = append(env, "WELD_PROJECT="+name)
	}
	return env
}

// manifestCapabilities returns the installed capability names, base first, for
// when.capability. Business modules and commands are not capabilities and are
// deliberately absent.
func manifestCapabilities(manifest *project.Manifest) []string {
	var names []string
	if manifest.Base.Name != "" {
		names = append(names, manifest.Base.Name)
	}
	for _, capability := range manifest.Capabilities {
		names = append(names, capability.Name)
	}
	return names
}

// printHookPlan writes the hook part of a dry-run plan.
func printHookPlan(w io.Writer, event hook.Event, result *hook.Result) {
	if len(result.Steps) == 0 {
		return
	}
	var planned, skipped []hook.Step
	for _, step := range result.Steps {
		if step.Skip != "" {
			skipped = append(skipped, step)
			continue
		}
		planned = append(planned, step)
	}
	fmt.Fprintf(w, "Plan (%d hook step(s), dry run):\n", len(planned))
	for _, step := range planned {
		fmt.Fprintf(w, "  run %s: %s\n", event, step.Line)
	}
	for _, step := range skipped {
		fmt.Fprintf(w, "  skip %s: %s (%s)\n", event, step.Line, step.Skip)
	}
}
