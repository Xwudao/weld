package projectcfg

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestLoadOptionalAbsent(t *testing.T) {
	cfg, err := LoadOptional(t.TempDir())
	if err != nil {
		t.Fatalf("LoadOptional: %v", err)
	}
	if cfg != nil {
		t.Fatalf("cfg = %+v, want nil", cfg)
	}
	if _, err := Load(t.TempDir()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Load error = %v, want ErrNotFound", err)
	}
}

func TestLoadRejectsUnknownVersion(t *testing.T) {
	dir := writeConfig(t, "version: 2\n")
	if _, err := Load(dir); err == nil {
		t.Fatal("expected an unsupported-version error")
	}
}

func TestDefaults(t *testing.T) {
	var cfg *Config // a missing file is a nil Config
	if got := cfg.Output(); got != "bin" {
		t.Errorf("Output = %q, want bin", got)
	}
	if !cfg.WebEnabled(true) || cfg.WebEnabled(false) {
		t.Errorf("WebEnabled did not follow the installed capability")
	}
	targets := cfg.Targets()
	if len(targets) != 1 || targets[0].OS != runtime.GOOS || targets[0].Arch != runtime.GOARCH {
		t.Errorf("Targets = %+v, want the host target", targets)
	}
	if got := cfg.BuildTags(); got != "" {
		t.Errorf("BuildTags = %q, want empty", got)
	}
	if got := cfg.GoBuildEnv(); got != nil {
		t.Errorf("GoBuildEnv = %v, want nil", got)
	}
	dev := cfg.EffectiveDev()
	if dev.Frontend.Dir != "web" || dev.Frontend.Pm != "pnpm" || dev.Frontend.Cmd != "run dev" {
		t.Errorf("EffectiveDev frontend = %+v", dev.Frontend)
	}
	if dev.Backend.Cmd != "" {
		t.Errorf("EffectiveDev backend cmd = %q, want empty", dev.Backend.Cmd)
	}
}

func TestDevWatchSettings(t *testing.T) {
	dir := writeConfig(t, `version: 1
dev:
  backend:
    watch: ["**/*.go", "config.yml"]
    watch_exclude: ["**/*_test.go"]
`)
	cfg, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	dev := cfg.EffectiveDev()
	if len(dev.Backend.Watch) != 2 || dev.Backend.Watch[0] != "**/*.go" ||
		len(dev.Backend.WatchExclude) != 1 || dev.Backend.WatchExclude[0] != "**/*_test.go" {
		t.Fatalf("watch settings = %+v", dev.Backend)
	}
	dev.Backend.Watch[0] = "changed"
	dev.Backend.WatchExclude[0] = "changed"
	if cfg.Dev.Backend.Watch[0] != "**/*.go" || cfg.Dev.Backend.WatchExclude[0] != "**/*_test.go" {
		t.Fatal("EffectiveDev aliased watch settings")
	}
	var absent *Config
	if got := absent.EffectiveDev(); len(got.Backend.Watch) != 0 {
		t.Fatalf("watch enabled by default: %+v", got)
	}
}

func TestBuildSettings(t *testing.T) {
	dir := writeConfig(t, `
version: 1
build:
  output: dist
  web: false
  tags: [prod, netpoll]
  cgo: false
  targets:
    - {os: linux, arch: amd64}
    - {os: windows, arch: arm64}
`)
	cfg, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Output() != "dist" {
		t.Errorf("Output = %q", cfg.Output())
	}
	if cfg.WebEnabled(true) {
		t.Error("build.web=false did not disable the frontend")
	}
	if got := cfg.BuildTags(); got != "prod,netpoll" {
		t.Errorf("BuildTags = %q", got)
	}
	if env := cfg.GoBuildEnv(); len(env) != 1 || env[0] != "CGO_ENABLED=0" {
		t.Errorf("GoBuildEnv = %v", env)
	}
	if targets := cfg.Targets(); len(targets) != 2 || targets[1].OS != "windows" {
		t.Errorf("Targets = %+v", targets)
	}
}

func TestBuildLdflagsExpandsBuiltins(t *testing.T) {
	cfg := &Config{Build: BuildConfig{Ldflags: []Ldflag{{
		Package: "example.com/demo/internal/app",
		Vars:    map[string]string{"Version": "${go_version}"},
	}}}}
	got := cfg.BuildLdflags()
	if !strings.Contains(got, "example.com/demo/internal/app.Version="+runtime.Version()) {
		t.Errorf("BuildLdflags = %q", got)
	}
}

func TestExpandVarsPrefersBuiltinOverEnv(t *testing.T) {
	t.Setenv("year", "not-the-year")
	if got := expandVars("${year}"); got == "not-the-year" {
		t.Errorf("expandVars used the environment for a built-in: %q", got)
	}
	if got := expandVars("${WELD_TEST_UNSET_VAR}"); got != "" {
		t.Errorf("expandVars unset = %q, want empty", got)
	}
}

func TestExampleIsValidYAML(t *testing.T) {
	var cfg Config
	if err := yaml.Unmarshal([]byte(Example()), &cfg); err != nil {
		t.Fatalf("Example is not valid YAML: %v", err)
	}
	if cfg.Version != 1 {
		t.Errorf("Example version = %d", cfg.Version)
	}
}

func TestWriteExample(t *testing.T) {
	dir := t.TempDir()
	path, written, err := WriteExample(dir, false)
	if err != nil {
		t.Fatalf("WriteExample: %v", err)
	}
	if !written || path != filepath.Join(dir, FileName) {
		t.Fatalf("path=%q written=%v", path, written)
	}
	if _, err := Load(dir); err != nil {
		t.Fatalf("written weld.yml does not load: %v", err)
	}

	// A second call without force must keep hand-edited settings.
	if err := os.WriteFile(path, []byte("version: 1\n# edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, written, err := WriteExample(dir, false); err != nil || written {
		t.Fatalf("second WriteExample written=%v err=%v, want written=false", written, err)
	}
	if raw, _ := os.ReadFile(path); string(raw) != "version: 1\n# edited\n" {
		t.Fatalf("existing file was clobbered: %q", raw)
	}

	// force restores the canonical example.
	if _, written, err := WriteExample(dir, true); err != nil || !written {
		t.Fatalf("force WriteExample written=%v err=%v, want written=true", written, err)
	}
	if raw, _ := os.ReadFile(path); string(raw) != Example() {
		t.Fatalf("force did not restore the example")
	}
}

func TestSeedTagDefaultAndOverride(t *testing.T) {
	var cfg *Config
	if got := cfg.SeedTag(); got != "seed" {
		t.Errorf("nil SeedTag = %q, want seed", got)
	}
	dir := writeConfig(t, "version: 1\nseed:\n  tag: devseed\n")
	loaded, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := loaded.SeedTag(); got != "devseed" {
		t.Errorf("SeedTag = %q, want devseed", got)
	}
}

func TestDeployDefaultsToLinuxAmd64(t *testing.T) {
	var cfg *Config // a missing file is a nil Config
	got := cfg.EffectiveDeploy()
	if got.Target == nil || got.Target.OS != "linux" || got.Target.Arch != "amd64" {
		t.Fatalf("EffectiveDeploy target = %+v, want linux/amd64", got.Target)
	}
	if got.Host != "" {
		t.Errorf("EffectiveDeploy host = %q, want empty", got.Host)
	}
}

func TestDeploySettings(t *testing.T) {
	dir := writeConfig(t, `
version: 1
deploy:
  host: prod-app
  remote_upload_dir: /srv/app
  remote_script: /srv/app/deploy.sh
  target: {os: linux, arch: arm64}
`)
	cfg, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	got := cfg.EffectiveDeploy()
	if got.Host != "prod-app" || got.RemoteUploadDir != "/srv/app" || got.RemoteScript != "/srv/app/deploy.sh" {
		t.Errorf("EffectiveDeploy = %+v", got)
	}
	if got.Target == nil || got.Target.Arch != "arm64" {
		t.Errorf("EffectiveDeploy target = %+v, want arm64", got.Target)
	}
}

func TestDeployValidate(t *testing.T) {
	full := DeployConfig{Host: "h", RemoteUploadDir: "/srv", RemoteScript: "/srv/x.sh"}
	if err := full.Validate(); err != nil {
		t.Fatalf("Validate(full) = %v", err)
	}
	empty := DeployConfig{}
	if err := empty.Validate(); err == nil || !strings.Contains(err.Error(), "deploy.host") {
		t.Fatalf("Validate(empty) = %v, want a host error", err)
	}
}
