package projectcfg

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestHookCommandAcceptsShellAndArgv(t *testing.T) {
	dir := writeConfig(t, `
version: 1
hooks:
  build:
    pre:
      - run: "go mod tidy"
      - run: [go, mod, download]
`)
	cfg, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	pre := cfg.Hooks.Build.Pre
	if len(pre) != 2 {
		t.Fatalf("build.pre has %d steps, want 2", len(pre))
	}
	if pre[0].Run.Shell != "go mod tidy" || len(pre[0].Run.Argv) != 0 {
		t.Errorf("scalar run = %+v, want a shell command", pre[0].Run)
	}
	if pre[1].Run.Shell != "" || strings.Join(pre[1].Run.Argv, " ") != "go mod download" {
		t.Errorf("sequence run = %+v, want argv", pre[1].Run)
	}
}

func TestHookPhasesAreSeparated(t *testing.T) {
	dir := writeConfig(t, `
version: 1
hooks:
  build:
    pre:
      - run: echo pre
    post:
      - run: echo post
  deploy:
    post:
      - run: echo deployed
`)
	cfg, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Hooks.Build.Pre) != 1 || len(cfg.Hooks.Build.Post) != 1 {
		t.Fatalf("build phases = %+v", cfg.Hooks.Build)
	}
	if len(cfg.Hooks.Deploy.Pre) != 0 || len(cfg.Hooks.Deploy.Post) != 1 {
		t.Fatalf("deploy phases = %+v", cfg.Hooks.Deploy)
	}
}

func TestHooksEnabledDefaultAndOverride(t *testing.T) {
	var zero HookConfig
	if !zero.IsEnabled() {
		t.Error("omitting enabled should default to true")
	}
	off := false
	if (HookConfig{Enabled: &off}).IsEnabled() {
		t.Error("enabled: false should disable hooks")
	}
	on := true
	if !(HookConfig{Enabled: &on}).IsEnabled() {
		t.Error("enabled: true should enable hooks")
	}
}

func TestHookTimeoutParsesDuration(t *testing.T) {
	dir := writeConfig(t, `
version: 1
hooks:
  dev:
    pre:
      - run: echo hi
        timeout: 2m30s
`)
	cfg, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Hooks.Dev.Pre[0].Timeout; got != 2*time.Minute+30*time.Second {
		t.Errorf("timeout = %s, want 2m30s", got)
	}
}

func TestLoadRejectsUnknownHookCommand(t *testing.T) {
	dir := writeConfig(t, `
version: 1
hooks:
  buld:
    pre:
      - run: echo hi
`)
	_, err := Load(dir)
	if err == nil || !strings.Contains(err.Error(), "buld") {
		t.Fatalf("Load error = %v, want an unknown-key error naming buld", err)
	}
}

func TestLoadRejectsUnknownHookStepField(t *testing.T) {
	dir := writeConfig(t, `
version: 1
hooks:
  build:
    pre:
      - run: echo hi
        platformz: [linux]
`)
	if _, err := Load(dir); err == nil {
		t.Fatal("expected an unknown-field error")
	}
}

func TestLoadRejectsInvalidHookSettings(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{
			name: "missing run",
			body: "version: 1\nhooks:\n  build:\n    pre:\n      - name: nothing\n",
			want: "run is required",
		},
		{
			name: "unknown platform",
			body: "version: 1\nhooks:\n  build:\n    pre:\n      - run: echo hi\n        platforms: [plan9]\n",
			want: "unknown platform",
		},
		{
			name: "negative timeout",
			body: "version: 1\nhooks:\n  build:\n    pre:\n      - run: echo hi\n        timeout: -1s\n",
			want: "timeout must not be negative",
		},
		{
			name: "absolute dir",
			body: "version: 1\nhooks:\n  build:\n    pre:\n      - run: echo hi\n        dir: /tmp\n",
			want: "relative path",
		},
		{
			name: "escaping dir",
			body: "version: 1\nhooks:\n  build:\n    pre:\n      - run: echo hi\n        dir: ../outside\n",
			want: "relative path",
		},
		{
			name: "empty when capability",
			body: "version: 1\nhooks:\n  build:\n    pre:\n      - run: echo hi\n        when: { capability: [\"\"] }\n",
			want: "when.capability",
		},
		{
			name: "wrong run type",
			body: "version: 1\nhooks:\n  build:\n    pre:\n      - run: {a: b}\n",
			want: "run must be",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := writeConfig(t, tc.body)
			_, err := Load(dir)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Load error = %v, want it to contain %q", err, tc.want)
			}
		})
	}
}

func TestMatchPlatform(t *testing.T) {
	if !MatchPlatform(nil, "linux") {
		t.Error("an empty platform list should run everywhere")
	}
	if !MatchPlatform([]string{"macos"}, "darwin") {
		t.Error("macos should match darwin")
	}
	if !MatchPlatform([]string{"windows"}, "windows") {
		t.Error("windows should match windows")
	}
	if MatchPlatform([]string{"windows"}, "linux") {
		t.Error("windows should not match linux")
	}
}

func TestExampleHooksAreStrictlyLoadable(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(Example()), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir); err != nil {
		t.Fatalf("Example does not load under strict decoding: %v", err)
	}
}
