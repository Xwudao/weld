// Package projectcfg loads the user-owned weld.yml project configuration.
//
// weld.yml is deliberately separate from the machine-owned weld.json manifest.
// weld.json records what weld wrote and is rewritten by every `weld add`, so a
// hand-edited build or dev setting must live in a file weld never rewrites.
// weld.yml is that file: it is optional, it is never recorded in the manifest
// and `weld add` never touches it.
//
// Every setting has a default derived from the installed capability set and the
// host platform, so a project with no weld.yml still builds and runs. A flag on
// `weld build` / `weld dev` overrides the file, which overrides the default.
package projectcfg

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// FileName is the user-owned project configuration filename.
const FileName = "weld.yml"

// ErrNotFound reports that a project has no weld.yml. LoadOptional turns it into
// a nil Config so callers fall back to the built-in defaults.
var ErrNotFound = errors.New("weld.yml not found")

// Target is one cross-compilation target for `weld build`.
type Target struct {
	OS   string `yaml:"os"`
	Arch string `yaml:"arch"`
}

// Ldflag injects build-time variables into a package with -ldflags -X.
type Ldflag struct {
	// Package is the import path of the package that declares the variables.
	Package string `yaml:"package"`
	// Vars maps a variable name to its value. Placeholders such as ${git_tag}
	// and ${datetime} are expanded at build time.
	Vars map[string]string `yaml:"vars"`
}

// DockerConfig holds local image build settings (no push or deployment).
type DockerConfig struct {
	Image      string `yaml:"image"`
	Dockerfile string `yaml:"dockerfile"`
	Platform   string `yaml:"platform"`
}

// BuildConfig holds the `weld build` settings.
type BuildConfig struct {
	Docker DockerConfig `yaml:"docker"`
	// Output is the directory cross-compiled binaries are written to. It
	// defaults to "bin".
	Output string `yaml:"output"`
	// Web controls whether the frontend is built before the Go binary. When
	// omitted, the frontend is built exactly when the web capability is
	// installed.
	Web *bool `yaml:"web"`
	// Targets lists the cross-compilation targets. When empty, the host
	// GOOS/GOARCH is built once.
	Targets []Target `yaml:"targets"`
	// Tags is passed to `go build -tags`.
	Tags []string `yaml:"tags"`
	// Cgo sets CGO_ENABLED. When omitted the toolchain default is left alone.
	Cgo *bool `yaml:"cgo"`
	// Ldflags injects variables with -ldflags -X.
	Ldflags []Ldflag `yaml:"ldflags"`
}

// DevBackendConfig holds the backend process of `weld dev`.
type DevBackendConfig struct {
	// Cmd replaces the default backend command entirely. It is split on spaces
	// like a shell command line. Empty means `go run .`, which serves when the
	// http capability is installed.
	Cmd string `yaml:"cmd"`
	// Args are appended to the default backend command (and ignored when Cmd is
	// set).
	Args []string `yaml:"args"`
}

// DevFrontendConfig holds the frontend process of `weld dev`.
type DevFrontendConfig struct {
	// Dir is the frontend directory. It defaults to "web".
	Dir string `yaml:"dir"`
	// Pm is the package manager. It defaults to "pnpm".
	Pm string `yaml:"pm"`
	// Cmd is the package manager arguments. It defaults to "run dev".
	Cmd string `yaml:"cmd"`
}

// DevConfig holds the `weld dev` process settings.
type DevConfig struct {
	Backend  DevBackendConfig  `yaml:"backend"`
	Frontend DevFrontendConfig `yaml:"frontend"`
}

// SeedConfig holds the `weld seed` settings.
type SeedConfig struct {
	// Tag is the build tag that compiles the seeders. It defaults to "seed".
	Tag string `yaml:"tag"`
}

// DeployConfig holds the `weld deploy` settings.
type DeployConfig struct {
	// Host is the ssh destination the artifact is uploaded to, usually a Host
	// alias from ~/.ssh/config.
	Host string `yaml:"host"`
	// RemoteUploadDir is the remote directory the artifact is uploaded to.
	RemoteUploadDir string `yaml:"remote_upload_dir"`
	// RemoteScript is the remote script run after the upload. The uploaded
	// artifact path is passed as its argument.
	RemoteScript string `yaml:"remote_script"`
	// Target is the GOOS/GOARCH built and uploaded. It defaults to linux/amd64.
	Target *Target `yaml:"target"`
}

// Config is a parsed weld.yml.
type Config struct {
	// Version is the schema version. It is currently always 1.
	Version int          `yaml:"version"`
	Build   BuildConfig  `yaml:"build"`
	Dev     DevConfig    `yaml:"dev"`
	Seed    SeedConfig   `yaml:"seed"`
	Deploy  DeployConfig `yaml:"deploy"`
	// Hooks extend the build/dev/deploy/seed commands with user-owned commands.
	// They are never run by `weld add` or `weld upgrade`.
	Hooks HookConfig `yaml:"hooks"`
}

// Load reads weld.yml from dir and returns ErrNotFound when it is absent.
func Load(dir string) (*Config, error) {
	path := Path(dir)
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("%s: %w", dir, ErrNotFound)
		}
		return nil, err
	}
	cfg := &Config{}
	// KnownFields makes an unknown key a load-time error rather than a silently
	// ignored setting. That is what turns a mistyped hook command (hooks.buld)
	// or step field into a message instead of a hook that never runs.
	decoder := yaml.NewDecoder(bytes.NewReader(raw))
	decoder.KnownFields(true)
	if err := decoder.Decode(cfg); err != nil {
		// An empty or comment-only file is an empty config, matching the
		// previous yaml.Unmarshal behaviour.
		if errors.Is(err, io.EOF) {
			cfg = &Config{}
		} else {
			return nil, fmt.Errorf("%s: invalid %s: %w", dir, FileName, err)
		}
	}
	if cfg.Version != 0 && cfg.Version != 1 {
		return nil, fmt.Errorf("%s: unsupported %s version %d (want 1)", dir, FileName, cfg.Version)
	}
	if err := cfg.validateHooks(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// Path returns the weld.yml path inside dir.
func Path(dir string) string {
	return filepath.Join(dir, FileName)
}

// WriteExample writes the commented Example to dir/weld.yml. It refuses to
// overwrite an existing file unless force is true and reports whether it wrote,
// so `weld config init` is idempotent and never clobbers hand-edited settings
// by accident. The directory must already exist.
func WriteExample(dir string, force bool) (path string, written bool, err error) {
	path = Path(dir)
	if _, statErr := os.Stat(path); statErr == nil {
		if !force {
			return path, false, nil
		}
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return path, false, statErr
	}
	if err := os.WriteFile(path, []byte(Example()), 0o644); err != nil {
		return path, false, err
	}
	return path, true, nil
}

// LoadOptional reads weld.yml from dir, returning (nil, nil) when it is absent.
func LoadOptional(dir string) (*Config, error) {
	cfg, err := Load(dir)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return cfg, nil
}

// Output returns the directory built binaries are written to.
func (c *Config) Output() string {
	if c != nil && c.Build.Output != "" {
		return c.Build.Output
	}
	return "bin"
}

// WebEnabled reports whether `weld build` builds the frontend. When weld.yml
// does not decide, the frontend is built exactly when the web capability is
// installed.
func (c *Config) WebEnabled(webInstalled bool) bool {
	if c != nil && c.Build.Web != nil {
		return *c.Build.Web
	}
	return webInstalled
}

// Targets returns the cross-compilation targets, defaulting to the host.
func (c *Config) Targets() []Target {
	if c != nil && len(c.Build.Targets) > 0 {
		return c.Build.Targets
	}
	return []Target{{OS: runtime.GOOS, Arch: runtime.GOARCH}}
}

// BuildTags returns the comma-joined -tags value, or "" when none are set.
func (c *Config) BuildTags() string {
	if c == nil || len(c.Build.Tags) == 0 {
		return ""
	}
	return strings.Join(c.Build.Tags, ",")
}

// GoBuildEnv returns the extra environment for `go build`, derived from the cgo
// setting. It returns nil when cgo is not configured.
func (c *Config) GoBuildEnv() []string {
	if c == nil || c.Build.Cgo == nil {
		return nil
	}
	if *c.Build.Cgo {
		return []string{"CGO_ENABLED=1"}
	}
	return []string{"CGO_ENABLED=0"}
}

// BuildLdflags returns the -ldflags value for `go build`, or "" when no
// ldflags are configured. Placeholders in every value are expanded at call
// time, so ${datetime}, ${git_tag} and friends reflect the build, not the parse.
func (c *Config) BuildLdflags() string {
	if c == nil || len(c.Build.Ldflags) == 0 {
		return ""
	}
	var parts []string
	for _, lf := range c.Build.Ldflags {
		for name, value := range lf.Vars {
			parts = append(parts, fmt.Sprintf("-X '%s.%s=%s'", lf.Package, name, expandVars(value)))
		}
	}
	return strings.Join(parts, " ")
}

// EffectiveDev returns the dev settings with every default filled in.
func (c *Config) EffectiveDev() DevConfig {
	cfg := DevConfig{
		Frontend: DevFrontendConfig{Dir: "web", Pm: "pnpm", Cmd: "run dev"},
	}
	if c == nil {
		return cfg
	}
	if c.Dev.Backend.Cmd != "" {
		cfg.Backend.Cmd = c.Dev.Backend.Cmd
	}
	if len(c.Dev.Backend.Args) > 0 {
		cfg.Backend.Args = append([]string(nil), c.Dev.Backend.Args...)
	}
	if c.Dev.Frontend.Dir != "" {
		cfg.Frontend.Dir = c.Dev.Frontend.Dir
	}
	if c.Dev.Frontend.Pm != "" {
		cfg.Frontend.Pm = c.Dev.Frontend.Pm
	}
	if c.Dev.Frontend.Cmd != "" {
		cfg.Frontend.Cmd = c.Dev.Frontend.Cmd
	}
	return cfg
}

// SeedTag returns the build tag `weld seed` compiles the seeders with. It
// defaults to "seed", the tag the generated seeders carry (`//go:build seed`).
func (c *Config) SeedTag() string {
	if c != nil && c.Seed.Tag != "" {
		return c.Seed.Tag
	}
	return "seed"
}

// EffectiveDeploy returns the deploy settings with the target defaulted to
// linux/amd64, the server platform `weld deploy` assumes when weld.yml does not
// name one. The receiver may be nil.
func (c *Config) EffectiveDeploy() DeployConfig {
	cfg := DeployConfig{Target: &Target{OS: "linux", Arch: "amd64"}}
	if c == nil {
		return cfg
	}
	cfg = c.Deploy
	if cfg.Target == nil {
		cfg.Target = &Target{OS: "linux", Arch: "amd64"}
	}
	return cfg
}

// Validate reports the first missing required deploy setting.
func (c DeployConfig) Validate() error {
	if strings.TrimSpace(c.Host) == "" {
		return errors.New("weld.yml: deploy.host is required")
	}
	if strings.TrimSpace(c.RemoteUploadDir) == "" {
		return errors.New("weld.yml: deploy.remote_upload_dir is required")
	}
	if strings.TrimSpace(c.RemoteScript) == "" {
		return errors.New("weld.yml: deploy.remote_script is required")
	}
	return nil
}

// expandVars resolves ${VAR} placeholders in s. Built-in variables take
// precedence over environment variables, so a build never depends on a
// same-named variable a user happened to export.
func expandVars(s string) string {
	return os.Expand(s, func(key string) string {
		if v, ok := getBuiltinVar(key); ok {
			return v
		}
		return os.Getenv(key)
	})
}

// getBuiltinVar returns the value of a named built-in variable. The second
// return value reports whether the name is a known built-in.
func getBuiltinVar(name string) (string, bool) {
	now := time.Now()
	switch name {
	case "date":
		return now.Format("2006-01-02"), true
	case "datetime":
		return now.Format("2006-01-02 15:04:05"), true
	case "time":
		return now.Format("15:04:05"), true
	case "timestamp":
		return strconv.FormatInt(now.Unix(), 10), true
	case "timestamp_ms":
		return strconv.FormatInt(now.UnixMilli(), 10), true
	case "year":
		return strconv.Itoa(now.Year()), true
	case "month":
		return fmt.Sprintf("%02d", now.Month()), true
	case "day":
		return fmt.Sprintf("%02d", now.Day()), true
	case "git_hash":
		return shortHash(gitHash()), true
	case "git_hash_full":
		return gitHash(), true
	case "git_tag":
		return gitTag(), true
	case "go_version":
		return runtime.Version(), true
	default:
		return "", false
	}
}

// shortHash truncates a full commit hash to its conventional short form. An
// empty hash stays empty, so a build outside a repository substitutes nothing
// rather than a bogus "(unknown)" string.
func shortHash(hash string) string {
	if len(hash) > 7 {
		return hash[:7]
	}
	return hash
}

// Example returns a commented weld.yml starter.
func Example() string {
	return `# weld.yml — user-owned project configuration.
#
# This file configures the ` + "`weld build`" + ` and ` + "`weld dev`" + ` commands.
# It is not the machine-owned weld.json manifest: weld never rewrites weld.yml,
# and ` + "`weld add`" + ` never touches it. Every setting is optional; when the file
# is absent the commands derive their defaults from the installed capabilities
# and the host platform.
#
# Run ` + "`weld config example`" + ` to print this file, or
# ` + "`weld config init`" + ` to write it into a project that has none.

version: 1

build:
  # Directory built binaries are written to (default: bin).
  output: bin
  # Local image settings for weld build --docker (requires weld docker init).
  # docker:
  #   image: my-app:latest
  #   dockerfile: Dockerfile
  #   platform: linux/amd64
  # Build the frontend before the Go binary. Omit to build it exactly when the
  # web capability is installed.
  # web: true
  # Cross-compilation targets. Omit to build the host platform once.
  # targets:
  #   - { os: linux, arch: amd64 }
  #   - { os: darwin, arch: arm64 }
  #   - { os: windows, arch: amd64 }
  # Passed to ` + "`go build -tags`" + `.
  # tags: []
  # CGO_ENABLED (default: leave the toolchain default alone).
  # cgo: false
  # Link-time variables. Placeholders: ${date} ${datetime} ${time}
  # ${timestamp} ${timestamp_ms} ${year} ${month} ${day} ${git_hash}
  # ${git_hash_full} ${git_tag} ${go_version}.
  # ldflags:
  #   - package: example.com/app/internal/app
  #     vars:
  #       Version: "${git_tag}"

dev:
  backend:
    # Replace the default ` + "`go run .`" + ` backend command. When omitted the
    # bare binary is run, which serves once the http capability is installed.
    # cmd: "go run ./cmd/app serve"
    # Appended to the default command (ignored when cmd is set).
    # args: []
  frontend:
    dir: web
    pm: pnpm
    cmd: run dev

seed:
  # Build tag that compiles the seeders (default: seed). The generated seeders
  # live behind this tag, so the production binary ` + "`weld build`" + ` produces
  # carries neither the seed data nor any faker a seeder imports. Run them with
  # ` + "`weld seed`" + ` or ` + "`make seed`" + `.
  tag: seed

deploy:
  # ssh destination, usually a Host alias from ~/.ssh/config.
  host: prod-app
  # Remote directory the built artifact is uploaded to.
  remote_upload_dir: /srv/app
  # Remote script run after the upload; the uploaded artifact path is passed
  # as its argument. ` + "`weld deploy`" + ` builds the linux target, scp's the
  # binary and runs this script over ssh.
  remote_script: /srv/app/deploy.sh
  # GOOS/GOARCH built and uploaded (default: linux/amd64).
  # target: { os: linux, arch: arm64 }

# Hooks run your own commands around a weld command. Each command has a "pre"
# and a "post" list; steps run in order, and a failure aborts the command unless
# the step sets continue_on_error. A whole run can skip them with --no-hooks or
# WELD_NO_HOOKS=1. A "run" scalar is a shell command line; a "run" list is an
# argv vector run directly. Valid commands: build, dev, deploy, seed.
hooks:
  # enabled: true
  # build:
  #   pre:
  #     - name: tidy
  #       run: [go, mod, tidy]
  #   post:
  #     - run: "gofmt -w ."
  #       platforms: [linux, darwin]
  # dev:
  #   pre:
  #     - run: "./scripts/pre_dev.sh"
  #       timeout: 2m
  # deploy:
  #   pre:
  #     - name: build assets
  #       run: [pnpm, run, build]
  #       dir: web
  #       when: { capability: [web] }
  #   post:
  #     - run: "./scripts/notify.sh"
  #       continue_on_error: true
  #       env: { SLACK_CHANNEL: "#deploys" }
`
}
