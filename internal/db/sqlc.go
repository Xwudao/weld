// Package db also drives the project's pinned sqlc generator. It is the same
// nested db/tools module the Makefile uses, invoked from the project root so
// sqlc.yaml keeps resolving its paths relative to the project. Once the sqlcx
// capability is installed, sqlc.yaml names the sqlc-dynamic process plugin, so
// generation runs through sqlc-dynamic from its own tools/sqlcx module instead:
// it puts the plugin on sqlc's PATH and then links the dynamic query code.
package db

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Xwudao/weld/internal/project"
)

// SQLCRequest describes one sqlc generation or check.
type SQLCRequest struct {
	// Dir is the project directory.
	Dir string
	// Check compares the generated files with what sqlc would write instead of
	// rewriting them. It is the CI counterpart of a dry run.
	Check bool
	// DryRun plans the command without running the generator.
	DryRun bool
	// Stdout and Stderr receive the generator's output.
	Stdout io.Writer
	Stderr io.Writer
	// Runner overrides command execution. A nil Runner runs the real command.
	Runner Runner
}

// SQLCResult reports what a sqlc invocation produced (or planned).
type SQLCResult struct {
	Step    Step
	Check   bool
	Applied bool
}

// SQLCXToolsDir is the nested module that pins sqlc-dynamic together with the
// sqlc release it drives.
const SQLCXToolsDir = "tools/sqlcx"

// SQLC runs the sqlc generator pinned in db/tools, or checks that the committed
// generated files match it. It validates the db capability and the paths
// sqlc.yaml names before invoking the tool, so a missing payload fails with an
// actionable message instead of sqlc's own parser error.
func SQLC(req SQLCRequest) (*SQLCResult, error) {
	dir := strings.TrimSpace(req.Dir)
	if dir == "" {
		return nil, errors.New("weld gen sqlc: project directory is required")
	}
	manifest, err := project.Load(dir)
	if err != nil {
		return nil, fmt.Errorf("weld gen sqlc: %w", err)
	}
	if !manifest.HasCapability("db") {
		return nil, fmt.Errorf("weld gen sqlc: the db capability is not installed in %s (run: weld add db)", dir)
	}
	dynamic := manifest.HasCapability("sqlcx")
	required := []struct{ path, capability string }{
		{filepath.Join(ToolsDir, "go.mod"), "db"},
		{"sqlc.yaml", "db"},
		{"db/query", "db"},
		{MigrationsDir, "db"},
	}
	if dynamic {
		required = append(required, struct{ path, capability string }{filepath.Join(SQLCXToolsDir, "go.mod"), "sqlcx"})
	}
	for _, r := range required {
		if _, err := os.Stat(filepath.Join(dir, r.path)); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil, fmt.Errorf("weld gen sqlc: %s is missing; reinstall with: weld add %s", r.path, r.capability)
			}
			return nil, fmt.Errorf("weld gen sqlc: stat %s: %w", r.path, err)
		}
	}

	step := sqlcStep(filepath.Join(dir, ToolsDir), req.Check)
	if dynamic {
		step = sqlcxStep(filepath.Join(dir, SQLCXToolsDir), req.Check)
	}
	result := &SQLCResult{Step: step, Check: req.Check}
	if req.DryRun {
		return result, nil
	}

	runner := req.Runner
	if runner == nil {
		runner = commandRunner(req.Stdout, req.Stderr)
	}
	if err := runner(step.Name, step.Dir, step.Env, step.Args...); err != nil {
		if req.Check {
			return nil, fmt.Errorf("weld gen sqlc: generated files are out of date; run without --check to refresh them: %w", err)
		}
		return nil, fmt.Errorf("weld gen sqlc: %w", err)
	}
	result.Applied = true
	return result, nil
}

// sqlcStep builds the pinned generator command. -f points back at the project's
// sqlc.yaml from the nested tools module, exactly as the Makefile does.
func sqlcStep(toolsDir string, check bool) Step {
	action := "generate"
	if check {
		action = "diff"
	}
	args := []string{"tool", "sqlc", action, "-f", "../../sqlc.yaml"}
	return Step{
		Name:    "go",
		Dir:     toolsDir,
		Env:     []string{"GOWORK=off", "GOFLAGS=-mod=mod"},
		Args:    args,
		Display: strings.Join(append([]string{"go"}, args...), " "),
	}
}

// sqlcxStep builds the sqlc-dynamic command. sqlc-dynamic runs the sqlc that
// its own module pins, so the plugin and the generator it serves move together;
// with check it runs `sqlc diff` and verifies the linked code without writing.
func sqlcxStep(toolsDir string, check bool) Step {
	args := []string{"tool", "sqlc-dynamic", "generate"}
	if check {
		args = append(args, "-check")
	}
	args = append(args, "-f", "../../sqlc.yaml")
	return Step{
		Name:    "go",
		Dir:     toolsDir,
		Env:     []string{"GOWORK=off", "GOFLAGS=-mod=mod"},
		Args:    args,
		Display: strings.Join(append([]string{"go"}, args...), " "),
	}
}
