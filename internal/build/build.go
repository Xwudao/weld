// Package build implements `weld build`: it builds the optional frontend and
// then cross-compiles the Go binary for every configured target.
//
// Planning and execution are separated the same way the scaffold separates
// them: Run validates the whole plan first, so a bad target fails before a
// single command runs. Command execution is injected through Runner, so a test
// exercises the plan without invoking go or a package manager.
package build

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/Xwudao/weld/internal/projectcfg"
)

// Runner runs one external command in dir with the extra environment. It is the
// injection seam: a test records the commands instead of running them.
type Runner func(name, dir string, env []string, args ...string) error

// Request describes a build.
type Request struct {
	// Dir is the project directory.
	Dir string
	// Name is the binary base name. It defaults to the project directory name.
	Name string
	// Config is the parsed weld.yml, or nil when the project has none.
	Config *projectcfg.Config
	// WebInstalled reports whether the web capability is recorded. It decides
	// the default for building the frontend.
	WebInstalled bool
	// Stdout and Stderr receive the output of every command.
	Stdout io.Writer
	Stderr io.Writer
	// Runner overrides command execution. A nil Runner runs real commands.
	Runner Runner
}

// Result reports what a build produced.
type Result struct {
	// Binaries are the built binary paths, relative to Dir.
	Binaries []string
	// WebBuilt reports whether the frontend was built.
	WebBuilt bool
}

// Run executes a build request.
func Run(req Request) (*Result, error) {
	cfg := req.Config
	name := req.Name
	if name == "" {
		abs, err := filepath.Abs(req.Dir)
		if err != nil {
			return nil, err
		}
		name = filepath.Base(abs)
	}
	runner := req.Runner
	if runner == nil {
		runner = commandRunner(req.Stdout, req.Stderr)
	}

	targets := cfg.Targets()
	for _, target := range targets {
		if target.OS == "" || target.Arch == "" {
			return nil, fmt.Errorf("weld.yml: build target needs both os and arch, got os=%q arch=%q", target.OS, target.Arch)
		}
	}

	result := &Result{}
	if cfg.WebEnabled(req.WebInstalled) {
		if err := buildWeb(cfg, runner, req.Dir); err != nil {
			return nil, err
		}
		result.WebBuilt = true
	}

	outputDir := cfg.Output()
	single := len(targets) == 1
	for _, target := range targets {
		out := binaryName(outputDir, name, target, single)
		env := []string{"GOOS=" + target.OS, "GOARCH=" + target.Arch}
		env = append(env, cfg.GoBuildEnv()...)
		args := []string{"build", "-trimpath"}
		if tags := cfg.BuildTags(); tags != "" {
			args = append(args, "-tags", tags)
		}
		if ldflags := cfg.BuildLdflags(); ldflags != "" {
			args = append(args, "-ldflags="+ldflags)
		}
		args = append(args, "-o", filepath.FromSlash(out), ".")
		if err := runner("go", req.Dir, env, args...); err != nil {
			return nil, fmt.Errorf("go build %s/%s: %w", target.OS, target.Arch, err)
		}
		result.Binaries = append(result.Binaries, out)
	}
	return result, nil
}

// binaryName returns the relative output path for one target. A single target
// keeps the bare name; several targets get an <os>-<arch> suffix so they never
// overwrite each other.
func binaryName(outputDir, name string, target projectcfg.Target, single bool) string {
	base := name
	if !single {
		base = fmt.Sprintf("%s-%s-%s", name, target.OS, target.Arch)
	}
	if target.OS == "windows" {
		base += ".exe"
	}
	return filepath.ToSlash(filepath.Join(outputDir, base))
}

// buildWeb runs the package manager's install and build in the frontend
// directory. It mirrors the generated Makefile's web-build target.
func buildWeb(cfg *projectcfg.Config, runner Runner, dir string) error {
	frontend := cfg.EffectiveDev().Frontend
	webDir := filepath.Join(dir, frontend.Dir)
	info, err := os.Stat(webDir)
	if err != nil || !info.IsDir() {
		return fmt.Errorf("build web: frontend directory not found: %s", webDir)
	}
	pm := frontend.Pm
	if pm == "" {
		pm = "pnpm"
	}
	if err := runner(pm, webDir, nil, "install"); err != nil {
		return fmt.Errorf("%s install: %w", pm, err)
	}
	if err := runner(pm, webDir, nil, "run", "build"); err != nil {
		return fmt.Errorf("%s run build: %w", pm, err)
	}
	return nil
}

// commandRunner returns the default Runner, which streams every command's
// output to stdout/stderr and inherits the process environment.
func commandRunner(stdout, stderr io.Writer) Runner {
	return func(name, dir string, env []string, args ...string) error {
		cmd := exec.Command(name, args...)
		cmd.Dir = dir
		if len(env) > 0 {
			cmd.Env = append(os.Environ(), env...)
		}
		cmd.Stdout = stdout
		cmd.Stderr = stderr
		cmd.Stdin = nil
		return cmd.Run()
	}
}
