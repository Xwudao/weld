// Package hook runs the user-declared lifecycle hooks from weld.yml.
//
// A hook extends a weld command with the project's own tooling: `build.pre`
// runs before the frontend and the Go compiler, `deploy.post` after the remote
// script, and so on. Hooks are declared in weld.yml and never recorded in
// weld.json; `weld add` and `weld upgrade` never run them.
//
// The package is deliberately thin and testable: it only knows how to filter a
// list of steps and run them. Command execution is injected through Runner, so a
// test records the commands instead of invoking a shell.
package hook

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/Xwudao/weld/internal/projectcfg"
)

// Event names a hook point. It is "<command>.<phase>".
type Event string

const (
	EventBuildPre   Event = "build.pre"
	EventBuildPost  Event = "build.post"
	EventDevPre     Event = "dev.pre"
	EventDevPost    Event = "dev.post"
	EventDeployPre  Event = "deploy.pre"
	EventDeployPost Event = "deploy.post"
	EventSeedPre    Event = "seed.pre"
	EventSeedPost   Event = "seed.post"
)

// Events lists every hook point, in a stable order. `weld config example` and
// error messages use it to name the valid events.
func Events() []Event {
	return []Event{
		EventBuildPre, EventBuildPost,
		EventDevPre, EventDevPost,
		EventDeployPre, EventDeployPost,
		EventSeedPre, EventSeedPost,
	}
}

// Command returns the weld command an event belongs to ("build" for
// "build.pre").
func (e Event) Command() string {
	command, _, _ := strings.Cut(string(e), ".")
	return command
}

// Runner runs one external command in dir with extra environment appended to
// the inherited process environment. It is the injection seam: a test records
// the command instead of running it.
type Runner func(ctx context.Context, name, dir string, env []string, args ...string) error

// Request describes one hook event.
type Request struct {
	// Event is the hook point to run.
	Event Event
	// Hooks is the parsed weld.yml hook configuration. A nil value (a project
	// with no weld.yml) is a no-op.
	Hooks *projectcfg.HookConfig
	// Dir is the project root. Relative paths are resolved against the current
	// directory.
	Dir string
	// Project is the project name, injected as WELD_PROJECT.
	Project string
	// Capabilities are the installed capability names, matched by
	// when.capability.
	Capabilities []string
	// Env is extra environment (for example WELD_* variables) applied to every
	// step, before the step's own env.
	Env []string
	// Stdout and Stderr receive the output of every command. A nil writer
	// discards it.
	Stdout io.Writer
	Stderr io.Writer
	// DryRun plans the steps without running them.
	DryRun bool
	// Runner overrides command execution. A nil Runner runs real commands.
	Runner Runner
	// LookupEnv reads a process environment variable for when.env. A nil
	// LookupEnv uses os.LookupEnv.
	LookupEnv func(string) (string, bool)
}

// Step is one hook step in a plan. Skip is empty when the step ran (or would
// run), and holds the reason it did not otherwise.
type Step struct {
	// Name labels the step: its name, or its command when it has none.
	Name string
	// Line is the command line, for display.
	Line string
	// Skip is the reason the step did not run, or empty.
	Skip string
}

// Result reports the steps a hook event considered, including skipped ones.
type Result struct {
	Steps []Step
}

// Run executes the steps configured for req.Event. It is fail-fast: the first
// step that fails stops the event unless the step sets continue_on_error. A nil
// or disabled hook configuration is a no-op.
func Run(req Request) (*Result, error) {
	result := &Result{}
	if req.Hooks == nil || !req.Hooks.IsEnabled() {
		return result, nil
	}

	root := req.Dir
	if abs, err := filepath.Abs(req.Dir); err == nil {
		root = abs
	}
	stdout := orDiscard(req.Stdout)
	stderr := orDiscard(req.Stderr)
	lookup := req.LookupEnv
	if lookup == nil {
		lookup = os.LookupEnv
	}
	runner := req.Runner
	if runner == nil {
		runner = commandRunner(stdout, stderr)
	}

	for _, step := range stepsFor(*req.Hooks, req.Event) {
		name := step.Name
		if name == "" {
			name = step.Run.String()
		}
		line := step.Run.String()

		if !projectcfg.MatchPlatform(step.Platforms, runtime.GOOS) {
			result.Steps = append(result.Steps, Step{Name: name, Line: line, Skip: "platform " + runtime.GOOS})
			continue
		}
		if reason := skipWhen(step.When, req.Capabilities, lookup, root); reason != "" {
			result.Steps = append(result.Steps, Step{Name: name, Line: line, Skip: reason})
			continue
		}

		result.Steps = append(result.Steps, Step{Name: name, Line: line})
		if req.DryRun {
			continue
		}

		fmt.Fprintf(stderr, "[hook] %s: %s\n", req.Event, name)
		dir := root
		if step.Dir != "" {
			dir = filepath.Join(root, step.Dir)
		}
		env := append(append([]string{}, req.Env...), envPairs(step.Env)...)
		if err := execute(runner, step, dir, env, req.Event); err != nil {
			if step.ContinueOnError {
				fmt.Fprintf(stderr, "[hook] %s: %s failed: %v (continuing)\n", req.Event, name, err)
				continue
			}
			return result, fmt.Errorf("weld: %s hook %q: %w", req.Event, name, err)
		}
	}
	return result, nil
}

// stepsFor returns the configured steps for one event.
func stepsFor(cfg projectcfg.HookConfig, event Event) []projectcfg.HookStep {
	switch event {
	case EventBuildPre:
		return cfg.Build.Pre
	case EventBuildPost:
		return cfg.Build.Post
	case EventDevPre:
		return cfg.Dev.Pre
	case EventDevPost:
		return cfg.Dev.Post
	case EventDeployPre:
		return cfg.Deploy.Pre
	case EventDeployPost:
		return cfg.Deploy.Post
	case EventSeedPre:
		return cfg.Seed.Pre
	case EventSeedPost:
		return cfg.Seed.Post
	default:
		return nil
	}
}

// execute runs one hook step. A step with a timeout gets a bounded context.
func execute(runner Runner, step projectcfg.HookStep, dir string, env []string, event Event) error {
	ctx := context.Background()
	if step.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, step.Timeout)
		defer cancel()
	}
	if len(step.Run.Argv) > 0 {
		argv := step.Run.Argv
		return runner(ctx, argv[0], dir, env, argv[1:]...)
	}
	name, args := shellCommand(step.Run.Shell)
	return runner(ctx, name, dir, env, args...)
}

// shellCommand wraps a shell command line for the host platform so pipes,
// redirection and quoting behave as written.
func shellCommand(line string) (string, []string) {
	if runtime.GOOS == "windows" {
		return "cmd", []string{"/C", line}
	}
	return "sh", []string{"-c", line}
}

// skipWhen reports the first unmet condition, or "" when the step may run.
func skipWhen(when *projectcfg.HookWhen, capabilities []string, lookup func(string) (string, bool), root string) string {
	if when == nil {
		return ""
	}
	for _, capability := range when.Capability {
		if !contains(capabilities, capability) {
			return "capability " + capability + " not installed"
		}
	}
	for _, key := range sortedKeys(when.Env) {
		got, ok := lookup(key)
		if !ok || got != when.Env[key] {
			return "env " + key + " is not " + when.Env[key]
		}
	}
	for _, file := range when.Files {
		if _, err := os.Stat(filepath.Join(root, file)); err != nil {
			return "file " + file + " is missing"
		}
	}
	return ""
}

// commandRunner returns the default Runner: it streams the command's output and
// merges the extra environment over the process environment.
func commandRunner(stdout, stderr io.Writer) Runner {
	return func(ctx context.Context, name, dir string, env []string, args ...string) error {
		cmd := exec.CommandContext(ctx, name, args...)
		cmd.Dir = dir
		cmd.Env = mergeEnv(os.Environ(), env)
		cmd.Stdout = stdout
		cmd.Stderr = stderr
		cmd.Stdin = os.Stdin
		return cmd.Run()
	}
}

// mergeEnv overlays extra (KEY=VALUE) entries onto base, so an override wins
// regardless of how the platform resolves duplicate keys.
func mergeEnv(base, extra []string) []string {
	if len(extra) == 0 {
		return base
	}
	overridden := make(map[string]struct{}, len(extra))
	for _, kv := range extra {
		if key, _, ok := strings.Cut(kv, "="); ok {
			overridden[key] = struct{}{}
		}
	}
	out := make([]string, 0, len(base)+len(extra))
	for _, kv := range base {
		if key, _, ok := strings.Cut(kv, "="); ok {
			if _, drop := overridden[key]; drop {
				continue
			}
		}
		out = append(out, kv)
	}
	return append(out, extra...)
}

// envPairs flattens an env map in a stable key order.
func envPairs(env map[string]string) []string {
	if len(env) == 0 {
		return nil
	}
	out := make([]string, 0, len(env))
	for _, key := range sortedKeys(env) {
		out = append(out, key+"="+env[key])
	}
	return out
}

// contains reports whether a list has needle.
func contains(list []string, needle string) bool {
	for _, item := range list {
		if item == needle {
			return true
		}
	}
	return false
}

// orDiscard returns w, or a discard writer when w is nil.
func orDiscard(w io.Writer) io.Writer {
	if w == nil {
		return io.Discard
	}
	return w
}

// sortedKeys returns the keys of an env map in ascending order, so a plan and
// an injected environment are deterministic.
func sortedKeys(env map[string]string) []string {
	keys := make([]string, 0, len(env))
	for key := range env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
