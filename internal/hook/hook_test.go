package hook

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Xwudao/weld/internal/projectcfg"
)

// call records one command a test Runner observed.
type call struct {
	name string
	dir  string
	env  []string
	args []string
}

// recorder is a Runner that records calls and can fail or block selected ones.
type recorder struct {
	calls []call
	fail  map[string]error
	block bool
}

func (r *recorder) runner(ctx context.Context, name, dir string, env []string, args ...string) error {
	r.calls = append(r.calls, call{name: name, dir: dir, env: env, args: append([]string(nil), args...)})
	if r.block {
		<-ctx.Done()
		return ctx.Err()
	}
	if r.fail != nil {
		if err := r.fail[name]; err != nil {
			return err
		}
	}
	return nil
}

// argvStep builds a step whose run is an argv vector.
func argvStep(name string, args ...string) projectcfg.HookStep {
	return projectcfg.HookStep{Run: projectcfg.HookCommand{Argv: append([]string{name}, args...)}}
}

// buildHooks builds a HookConfig with the given build pre/post steps.
func buildHooks(pre, post []projectcfg.HookStep) *projectcfg.HookConfig {
	return &projectcfg.HookConfig{Build: projectcfg.HookPhases{Pre: pre, Post: post}}
}

func TestEventsAndCommands(t *testing.T) {
	if got := len(Events()); got != 8 {
		t.Errorf("Events() returned %d events, want 8", got)
	}
	if EventDeployPost.Command() != "deploy" {
		t.Errorf("Command = %q, want deploy", EventDeployPost.Command())
	}
}

func TestRunExecutesStepsInOrder(t *testing.T) {
	rec := &recorder{}
	req := Request{
		Event:  EventBuildPre,
		Hooks:  buildHooks([]projectcfg.HookStep{argvStep("one"), argvStep("two")}, nil),
		Dir:    t.TempDir(),
		Runner: rec.runner,
	}
	result, err := Run(req)
	if err != nil {
		t.Fatal(err)
	}
	if len(rec.calls) != 2 || rec.calls[0].name != "one" || rec.calls[1].name != "two" {
		t.Fatalf("calls = %+v, want one then two", rec.calls)
	}
	if len(result.Steps) != 2 || result.Steps[0].Skip != "" {
		t.Fatalf("steps = %+v", result.Steps)
	}
}

func TestRunSeparatesPreAndPost(t *testing.T) {
	rec := &recorder{}
	hooks := buildHooks([]projectcfg.HookStep{argvStep("pre")}, []projectcfg.HookStep{argvStep("post")})
	if _, err := Run(Request{Event: EventBuildPre, Hooks: hooks, Dir: t.TempDir(), Runner: rec.runner}); err != nil {
		t.Fatal(err)
	}
	if len(rec.calls) != 1 || rec.calls[0].name != "pre" {
		t.Fatalf("build.pre ran %+v, want only pre", rec.calls)
	}
}

func TestRunSkipsWrongPlatform(t *testing.T) {
	rec := &recorder{}
	step := argvStep("win")
	step.Platforms = []string{"windows"}
	if runtime.GOOS == "windows" {
		step.Platforms = []string{"linux"}
	}
	result, err := Run(Request{Event: EventBuildPre, Hooks: buildHooks([]projectcfg.HookStep{step}, nil), Dir: t.TempDir(), Runner: rec.runner})
	if err != nil {
		t.Fatal(err)
	}
	if len(rec.calls) != 0 {
		t.Fatalf("platform-restricted step ran: %+v", rec.calls)
	}
	if len(result.Steps) != 1 || !strings.Contains(result.Steps[0].Skip, "platform") {
		t.Fatalf("steps = %+v, want a platform skip", result.Steps)
	}
}

func TestRunWhenCapability(t *testing.T) {
	step := argvStep("run")
	step.When = &projectcfg.HookWhen{Capability: []string{"web"}}

	rec := &recorder{}
	if _, err := Run(Request{Event: EventBuildPre, Hooks: buildHooks([]projectcfg.HookStep{step}, nil), Dir: t.TempDir(), Capabilities: []string{"http"}, Runner: rec.runner}); err != nil {
		t.Fatal(err)
	}
	if len(rec.calls) != 0 {
		t.Fatal("step ran without the required capability")
	}

	rec = &recorder{}
	if _, err := Run(Request{Event: EventBuildPre, Hooks: buildHooks([]projectcfg.HookStep{step}, nil), Dir: t.TempDir(), Capabilities: []string{"web"}, Runner: rec.runner}); err != nil {
		t.Fatal(err)
	}
	if len(rec.calls) != 1 {
		t.Fatal("step did not run with the required capability")
	}
}

func TestRunWhenEnv(t *testing.T) {
	step := argvStep("run")
	step.When = &projectcfg.HookWhen{Env: map[string]string{"CI": "true"}}
	hooks := buildHooks([]projectcfg.HookStep{step}, nil)

	rec := &recorder{}
	lookup := func(key string) (string, bool) { return "", false }
	if _, err := Run(Request{Event: EventBuildPre, Hooks: hooks, Dir: t.TempDir(), Runner: rec.runner, LookupEnv: lookup}); err != nil {
		t.Fatal(err)
	}
	if len(rec.calls) != 0 {
		t.Fatal("step ran while the condition was unmet")
	}

	rec = &recorder{}
	lookup = func(key string) (string, bool) { return "true", key == "CI" }
	if _, err := Run(Request{Event: EventBuildPre, Hooks: hooks, Dir: t.TempDir(), Runner: rec.runner, LookupEnv: lookup}); err != nil {
		t.Fatal(err)
	}
	if len(rec.calls) != 1 {
		t.Fatal("step did not run while the condition was met")
	}
}

func TestRunWhenFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "web"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "web", "pnpm-lock.yaml"), nil, 0o644); err != nil {
		t.Fatal(err)
	}

	step := argvStep("run")
	step.When = &projectcfg.HookWhen{Files: []string{"web/pnpm-lock.yaml"}}
	hooks := buildHooks([]projectcfg.HookStep{step}, nil)

	rec := &recorder{}
	if _, err := Run(Request{Event: EventBuildPre, Hooks: hooks, Dir: dir, Runner: rec.runner}); err != nil {
		t.Fatal(err)
	}
	if len(rec.calls) != 1 {
		t.Fatal("step did not run although the file exists")
	}

	step.When = &projectcfg.HookWhen{Files: []string{"web/missing.yaml"}}
	rec = &recorder{}
	if _, err := Run(Request{Event: EventBuildPre, Hooks: buildHooks([]projectcfg.HookStep{step}, nil), Dir: dir, Runner: rec.runner}); err != nil {
		t.Fatal(err)
	}
	if len(rec.calls) != 0 {
		t.Fatal("step ran although the file is missing")
	}
}

func TestRunEnvOverlay(t *testing.T) {
	step := argvStep("echo")
	step.Env = map[string]string{"APP_ENV": "production"}
	rec := &recorder{}
	if _, err := Run(Request{
		Event:  EventBuildPre,
		Hooks:  buildHooks([]projectcfg.HookStep{step}, nil),
		Dir:    t.TempDir(),
		Env:    []string{"WELD_EVENT=build.pre"},
		Runner: rec.runner,
	}); err != nil {
		t.Fatal(err)
	}
	want := []string{"WELD_EVENT=build.pre", "APP_ENV=production"}
	if !reflect.DeepEqual(rec.calls[0].env, want) {
		t.Fatalf("env = %v, want %v", rec.calls[0].env, want)
	}
}

func TestRunStepDirIsRelativeToRoot(t *testing.T) {
	dir := t.TempDir()
	step := argvStep("echo")
	step.Dir = "web"
	rec := &recorder{}
	if _, err := Run(Request{Event: EventBuildPre, Hooks: buildHooks([]projectcfg.HookStep{step}, nil), Dir: dir, Runner: rec.runner}); err != nil {
		t.Fatal(err)
	}
	abs, _ := filepath.Abs(dir)
	if got, want := rec.calls[0].dir, filepath.Join(abs, "web"); got != want {
		t.Fatalf("dir = %q, want %q", got, want)
	}
}

func TestRunFailsFast(t *testing.T) {
	rec := &recorder{fail: map[string]error{"bad": errors.New("boom")}}
	hooks := buildHooks([]projectcfg.HookStep{argvStep("bad"), argvStep("never")}, nil)
	_, err := Run(Request{Event: EventBuildPre, Hooks: hooks, Dir: t.TempDir(), Runner: rec.runner})
	if err == nil || !strings.Contains(err.Error(), "build.pre") || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("err = %v, want a build.pre error mentioning boom", err)
	}
	if len(rec.calls) != 1 {
		t.Fatalf("calls = %+v, want only the failing step", rec.calls)
	}
}

func TestRunContinueOnError(t *testing.T) {
	rec := &recorder{fail: map[string]error{"bad": errors.New("boom")}}
	bad := argvStep("bad")
	bad.ContinueOnError = true
	hooks := buildHooks([]projectcfg.HookStep{bad, argvStep("after")}, nil)
	if _, err := Run(Request{Event: EventBuildPre, Hooks: hooks, Dir: t.TempDir(), Runner: rec.runner}); err != nil {
		t.Fatalf("Run = %v, want nil", err)
	}
	if len(rec.calls) != 2 {
		t.Fatalf("calls = %+v, want the failing step and the next one", rec.calls)
	}
}

func TestRunTimeout(t *testing.T) {
	step := argvStep("slow")
	step.Timeout = 20 * time.Millisecond
	rec := &recorder{block: true}
	_, err := Run(Request{Event: EventBuildPre, Hooks: buildHooks([]projectcfg.HookStep{step}, nil), Dir: t.TempDir(), Runner: rec.runner})
	if err == nil || !strings.Contains(err.Error(), "deadline exceeded") {
		t.Fatalf("err = %v, want a deadline error", err)
	}
}

func TestRunDryRunPlansWithoutRunning(t *testing.T) {
	rec := &recorder{}
	result, err := Run(Request{
		Event:  EventBuildPre,
		Hooks:  buildHooks([]projectcfg.HookStep{argvStep("echo", "hi")}, nil),
		Dir:    t.TempDir(),
		DryRun: true,
		Runner: rec.runner,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(rec.calls) != 0 {
		t.Fatalf("dry run executed %+v", rec.calls)
	}
	if len(result.Steps) != 1 || result.Steps[0].Line != "echo hi" {
		t.Fatalf("steps = %+v", result.Steps)
	}
}

func TestRunDisabledAndNil(t *testing.T) {
	rec := &recorder{}
	off := false
	hooks := buildHooks([]projectcfg.HookStep{argvStep("echo")}, nil)
	hooks.Enabled = &off
	if _, err := Run(Request{Event: EventBuildPre, Hooks: hooks, Dir: t.TempDir(), Runner: rec.runner}); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(Request{Event: EventBuildPre, Hooks: nil, Dir: t.TempDir(), Runner: rec.runner}); err != nil {
		t.Fatal(err)
	}
	if len(rec.calls) != 0 {
		t.Fatalf("disabled or nil hooks ran %+v", rec.calls)
	}
}

func TestRunArgvKeepsSpaces(t *testing.T) {
	rec := &recorder{}
	if _, err := Run(Request{Event: EventBuildPre, Hooks: buildHooks([]projectcfg.HookStep{argvStep("echo", "a b")}, nil), Dir: t.TempDir(), Runner: rec.runner}); err != nil {
		t.Fatal(err)
	}
	if len(rec.calls[0].args) != 1 || rec.calls[0].args[0] != "a b" {
		t.Fatalf("args = %v, want one argument \"a b\"", rec.calls[0].args)
	}
}

func TestShellCommand(t *testing.T) {
	name, args := shellCommand("echo hi | cat")
	if runtime.GOOS == "windows" {
		if name != "cmd" || len(args) != 2 || args[0] != "/C" {
			t.Fatalf("windows shell = %s %v", name, args)
		}
		return
	}
	if name != "sh" || len(args) != 2 || args[0] != "-c" || args[1] != "echo hi | cat" {
		t.Fatalf("posix shell = %s %v", name, args)
	}
}

func TestMergeEnvOverrideWins(t *testing.T) {
	got := mergeEnv([]string{"A=1", "B=2"}, []string{"A=3", "C=4"})
	want := []string{"B=2", "A=3", "C=4"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("mergeEnv = %v, want %v", got, want)
	}
}
