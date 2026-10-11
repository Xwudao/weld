// Package seed implements `weld seed`: it runs the project's database seeders
// through the seed build tag.
//
// The generated seeders live behind `//go:build seed`, so the production binary
// `weld build` produces compiles in neither the seed data nor any faker a seeder
// imports. weld seed is the explicit entry point that compiles the tag in and
// runs the project's short seed command.
package seed

import (
	"fmt"
	"io"
	"os"
	"os/exec"

	"github.com/Xwudao/weld/internal/project"
	"github.com/Xwudao/weld/internal/projectcfg"
)

// Runner runs one external command in dir. It is the injection seam: a test
// records the command instead of invoking go.
type Runner func(name, dir string, args ...string) error

// Request describes a seed run.
type Request struct {
	// Dir is the project directory.
	Dir string
	// Name is an optional single seeder to run. Empty runs every seeder.
	Name string
	// Tag is the build tag that compiles the seeders. Empty means weld.yml's
	// seed.tag, or "seed".
	Tag string
	// Stdout and Stderr receive the command's output.
	Stdout io.Writer
	Stderr io.Writer
	// Runner overrides command execution. A nil Runner runs the real command.
	Runner Runner
}

// Run executes a seed request against the project in req.Dir.
func Run(req Request) error {
	manifest, err := project.Load(req.Dir)
	if err != nil {
		return fmt.Errorf("weld seed: %w", err)
	}
	if !manifest.HasCapability("seed") {
		return fmt.Errorf("weld seed: the seed capability is not installed in %s (run: weld add seed)", req.Dir)
	}

	cfg, err := projectcfg.LoadOptional(req.Dir)
	if err != nil {
		return fmt.Errorf("weld seed: %w", err)
	}
	tag := req.Tag
	if tag == "" {
		tag = cfg.SeedTag()
	}
	args := []string{"run", "-tags", tag, ".", "seed"}
	if req.Name != "" {
		args = append(args, req.Name)
	}

	runner := req.Runner
	if runner == nil {
		runner = commandRunner(req.Stdout, req.Stderr)
	}
	return runner("go", req.Dir, args...)
}

// commandRunner returns the default Runner, which streams the command's output
// and inherits the process environment.
func commandRunner(stdout, stderr io.Writer) Runner {
	return func(name, dir string, args ...string) error {
		cmd := exec.Command(name, args...)
		cmd.Dir = dir
		cmd.Stdout = stdout
		cmd.Stderr = stderr
		cmd.Stdin = os.Stdin
		return cmd.Run()
	}
}
