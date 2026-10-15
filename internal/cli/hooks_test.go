package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/Xwudao/weld/internal/hook"
	"github.com/Xwudao/weld/internal/project"
	"github.com/Xwudao/weld/internal/projectcfg"
)

// hookCommand builds a command with the flags runHooks reads.
func hookCommand(dir string) *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Flags().String("dir", dir, "")
	cmd.Flags().Bool("no-hooks", false, "")
	cmd.Flags().Bool("dry-run", false, "")
	return cmd
}

// stubHooks replaces the hook execution seam and returns the last Request.
func stubHooks(t *testing.T, result *hook.Result) *hook.Request {
	t.Helper()
	captured := &hook.Request{}
	previous := hooksExecute
	hooksExecute = func(req hook.Request) (*hook.Result, error) {
		*captured = req
		if result == nil {
			result = &hook.Result{}
		}
		return result, nil
	}
	t.Cleanup(func() { hooksExecute = previous })
	return captured
}

func withBuildPre(step projectcfg.HookStep) *projectcfg.Config {
	return &projectcfg.Config{Hooks: projectcfg.HookConfig{
		Build: projectcfg.HookPhases{Pre: []projectcfg.HookStep{step}},
	}}
}

func TestHooksDisabled(t *testing.T) {
	cmd := hookCommand(".")
	if hooksDisabled(cmd) {
		t.Error("hooks should be enabled by default")
	}
	if err := cmd.Flags().Set("no-hooks", "true"); err != nil {
		t.Fatal(err)
	}
	if !hooksDisabled(cmd) {
		t.Error("--no-hooks should disable hooks")
	}

	cmd = hookCommand(".")
	t.Setenv("WELD_NO_HOOKS", "1")
	if !hooksDisabled(cmd) {
		t.Error("WELD_NO_HOOKS=1 should disable hooks")
	}
	t.Setenv("WELD_NO_HOOKS", "0")
	if hooksDisabled(cmd) {
		t.Error("WELD_NO_HOOKS=0 should not disable hooks")
	}
}

func TestBaseHookEnv(t *testing.T) {
	dir := t.TempDir()
	env := baseHookEnv(dir, "demo", hook.EventBuildPre)
	joined := strings.Join(env, "\n")
	abs, _ := filepath.Abs(dir)
	for _, want := range []string{
		"WELD_COMMAND=build",
		"WELD_EVENT=build.pre",
		"WELD_DIR=" + abs,
		"WELD_VERSION=" + version,
		"WELD_PROJECT=demo",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("baseHookEnv missing %q in %v", want, env)
		}
	}
	if strings.Contains(strings.Join(baseHookEnv(dir, "", hook.EventSeedPost), "\n"), "WELD_PROJECT") {
		t.Error("an unnamed project should not inject WELD_PROJECT")
	}
}

func TestManifestCapabilities(t *testing.T) {
	manifest := project.NewManifest("demo", "example.com/demo", "test")
	manifest.Base.Name = "base"
	manifest.Capabilities = []project.CapabilityRef{{Name: "http"}, {Name: "web"}}
	got := manifestCapabilities(manifest)
	want := []string{"base", "http", "web"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("manifestCapabilities = %v, want %v", got, want)
	}
}

func TestRunHooksBuildsRequest(t *testing.T) {
	captured := stubHooks(t, nil)
	cmd := hookCommand(t.TempDir())
	cfg := withBuildPre(projectcfg.HookStep{Run: projectcfg.HookCommand{Argv: []string{"echo", "hi"}}})

	if err := runHooks(cmd, cfg, hook.EventBuildPre, false, "WELD_BUILD_OUTPUT=bin"); err != nil {
		t.Fatal(err)
	}
	if captured.Event != hook.EventBuildPre {
		t.Errorf("event = %q", captured.Event)
	}
	if captured.Hooks != &cfg.Hooks {
		t.Error("request did not carry the hook configuration")
	}
	if !containsEnv(captured.Env, "WELD_BUILD_OUTPUT=bin") {
		t.Errorf("extra env missing from %v", captured.Env)
	}
	if !containsEnv(captured.Env, "WELD_EVENT=build.pre") {
		t.Errorf("base env missing from %v", captured.Env)
	}
}

func TestRunHooksNoConfigOrOptOut(t *testing.T) {
	called := false
	previous := hooksExecute
	hooksExecute = func(hook.Request) (*hook.Result, error) { called = true; return &hook.Result{}, nil }
	t.Cleanup(func() { hooksExecute = previous })

	if err := runHooks(hookCommand("."), nil, hook.EventBuildPre, false); err != nil {
		t.Fatal(err)
	}
	if called {
		t.Fatal("a nil config ran hooks")
	}

	cmd := hookCommand(".")
	if err := cmd.Flags().Set("no-hooks", "true"); err != nil {
		t.Fatal(err)
	}
	cfg := withBuildPre(projectcfg.HookStep{Run: projectcfg.HookCommand{Shell: "echo hi"}})
	if err := runHooks(cmd, cfg, hook.EventBuildPre, false); err != nil {
		t.Fatal(err)
	}
	if called {
		t.Fatal("--no-hooks ran hooks")
	}
}

func TestRunHooksDryRunPrintsPlan(t *testing.T) {
	stubHooks(t, &hook.Result{Steps: []hook.Step{{Name: "tidy", Line: "go mod tidy"}}})
	cmd := hookCommand(".")
	if err := cmd.Flags().Set("dry-run", "true"); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	cmd.SetOut(&out)
	cfg := withBuildPre(projectcfg.HookStep{Run: projectcfg.HookCommand{Argv: []string{"go", "mod", "tidy"}}})

	if err := runHooks(cmd, cfg, hook.EventBuildPre, true); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "run build.pre: go mod tidy") {
		t.Fatalf("plan output = %q", out.String())
	}
}

func TestPrintHookPlanShowsSkips(t *testing.T) {
	var out bytes.Buffer
	printHookPlan(&out, hook.EventBuildPre, &hook.Result{Steps: []hook.Step{
		{Name: "tidy", Line: "go mod tidy"},
		{Name: "win", Line: "build.bat", Skip: "platform linux"},
	}})
	got := out.String()
	if !strings.Contains(got, "run build.pre: go mod tidy") {
		t.Errorf("plan missing the run line: %q", got)
	}
	if !strings.Contains(got, "skip build.pre: build.bat (platform linux)") {
		t.Errorf("plan missing the skip reason: %q", got)
	}
}

func TestWeldNoHooksEnvOptOutReachesRunHooks(t *testing.T) {
	called := false
	previous := hooksExecute
	hooksExecute = func(hook.Request) (*hook.Result, error) { called = true; return &hook.Result{}, nil }
	t.Cleanup(func() { hooksExecute = previous })

	t.Setenv("WELD_NO_HOOKS", "true")

	cfg := withBuildPre(projectcfg.HookStep{Run: projectcfg.HookCommand{Shell: "echo hi"}})
	if err := runHooks(hookCommand("."), cfg, hook.EventBuildPre, false); err != nil {
		t.Fatal(err)
	}
	if called {
		t.Fatal("WELD_NO_HOOKS did not opt out")
	}
}

func containsEnv(env []string, want string) bool {
	for _, item := range env {
		if item == want {
			return true
		}
	}
	return false
}

// TestCommandsRunPreHooks wires each hookable command to its pre event. The
// stub aborts with an error, so the real build/dev/seed/deploy never runs.
func TestCommandsRunPreHooks(t *testing.T) {
	cases := []struct {
		name  string
		body  string
		args  []string
		event hook.Event
	}{
		{
			name:  "build",
			body:  "version: 1\nhooks:\n  build:\n    pre:\n      - run: echo hi\n",
			args:  []string{"build"},
			event: hook.EventBuildPre,
		},
		{
			name:  "dev",
			body:  "version: 1\nhooks:\n  dev:\n    pre:\n      - run: echo hi\n",
			args:  []string{"dev"},
			event: hook.EventDevPre,
		},
		{
			name:  "seed",
			body:  "version: 1\nhooks:\n  seed:\n    pre:\n      - run: echo hi\n",
			args:  []string{"seed"},
			event: hook.EventSeedPre,
		},
		{
			name:  "deploy",
			body:  "version: 1\ndeploy:\n  host: prod\n  remote_upload_dir: /srv/app\n  remote_script: /srv/app/deploy.sh\nhooks:\n  deploy:\n    pre:\n      - run: echo hi\n",
			args:  []string{"deploy"},
			event: hook.EventDeployPre,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, projectcfg.FileName), []byte(tc.body), 0o644); err != nil {
				t.Fatal(err)
			}
			captured := &hook.Request{}
			previous := hooksExecute
			hooksExecute = func(req hook.Request) (*hook.Result, error) {
				*captured = req
				return nil, errors.New("stop")
			}
			t.Cleanup(func() { hooksExecute = previous })

			_, err := run(t, append(append([]string{}, tc.args...), "--dir", root)...)
			if err == nil || !strings.Contains(err.Error(), "stop") {
				t.Fatalf("err = %v, want the stub error", err)
			}
			if captured.Event != tc.event {
				t.Fatalf("event = %q, want %q", captured.Event, tc.event)
			}
		})
	}
}
