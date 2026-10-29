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
	"strings"
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

	toolchain, err := projectToolchain(projectDir)
	if err != nil {
		return err
	}
	build := exec.Command("go", "build", "-o", binary, "github.com/Xwudao/loom/cmd/loom")
	build.Dir = toolDir
	build.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=-mod=mod", "GOTOOLCHAIN="+toolchain)
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

// projectToolchain returns the Go toolchain the project's go.mod selects.
//
// The generator type-checks the project with the go/types it was compiled
// with, and go/types refuses a module whose go directive is newer than its own
// release. tools/loom declares only Loom's floor, so building there would pick
// an older toolchain than a project whose floor a capability raised (sqlcx
// needs Go 1.27). `go env GOVERSION` run in the project root goes through the
// same toolchain selection as a build there, so the generator is compiled with
// a toolchain at least as new as the project requires.
func projectToolchain(projectDir string) (string, error) {
	cmd := exec.Command("go", "env", "GOVERSION")
	cmd.Dir = projectDir
	cmd.Env = append(os.Environ(), "GOWORK=off")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("select the project's Go toolchain: %w", err)
	}
	version := strings.TrimSpace(string(out))
	if !strings.HasPrefix(version, "go") {
		return "", fmt.Errorf("select the project's Go toolchain: unexpected go env GOVERSION %q", version)
	}
	return version, nil
}
