// Package loomgen runs the pinned Loom code generator against a generated
// project.
//
// The generator is pinned as a tool in the project's nested tools/loom module
// (see the loom capability), so building it never adds the generator's
// dependencies to the application module. The build and the generator both run
// with -mod=mod so a fresh project can resolve its module graph and write
// go.sum; a failure is returned to the caller, which rolls the project back.
package loomgen

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// ToolModule is the nested module that pins the generator.
const ToolModule = "tools/loom"

// Generate builds the pinned generator and runs it over pkgDir within
// projectDir. pkgDir is relative to the project root (for example
// "internal/di").
func Generate(projectDir, pkgDir string) error {
	toolDir := filepath.Join(projectDir, filepath.FromSlash(ToolModule))
	tmp, err := os.MkdirTemp("", "weld-loom-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	binary := filepath.Join(tmp, "loom")

	build := exec.Command("go", "build", "-o", binary, "github.com/Xwudao/loom/cmd/loom")
	build.Dir = toolDir
	build.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=-mod=mod")
	if out, err := build.CombinedOutput(); err != nil {
		return fmt.Errorf("build pinned loom generator: %w\n%s", err, out)
	}

	pattern := "./" + filepath.ToSlash(pkgDir)
	run := exec.Command(binary, "generate", pattern)
	run.Dir = projectDir
	run.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=-mod=mod")
	if out, err := run.CombinedOutput(); err != nil {
		return fmt.Errorf("loom generate %s: %w\n%s", pattern, err, out)
	}
	return nil
}
