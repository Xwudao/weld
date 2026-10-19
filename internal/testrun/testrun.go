// Package testrun implements `weld test`: it runs the project's Go tests and,
// when the db capability is installed, points the generated integration tests at
// the configured test database.
//
// It never starts a database. The test database comes from config.yml's
// database_test section or PG_TEST_DSN, and the generated internal/testdb helper
// consumes the same value. Resolving here and injecting PG_TEST_DSN keeps the
// CLI and the generated tests on one dsn.
package testrun

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/Xwudao/weld/internal/db"
	"github.com/Xwudao/weld/internal/project"
)

// Runner runs one external command with extra environment appended to the
// inherited process environment. It is the injection seam: a test records the
// command instead of invoking go.
type Runner func(name, dir string, env []string, args ...string) error

// Mode selects how a missing test database is treated.
type Mode string

const (
	// ModeAuto runs the tests; a missing test database makes the integration
	// tests skip.
	ModeAuto Mode = "auto"
	// ModeOff ignores any configured test database.
	ModeOff Mode = "off"
	// ModeRequire fails when no test database is configured.
	ModeRequire Mode = "require"
)

// Request describes a test run.
type Request struct {
	// Dir is the project directory.
	Dir string
	// Mode selects the test database policy. Empty means ModeAuto.
	Mode Mode
	// DBURL is an explicit test dsn from --db-url.
	DBURL string
	// ConfigPath overrides config.yml. An empty value uses config.yml in Dir.
	ConfigPath string
	// Args are passed to `go test` after the package list. Empty means ./...
	Args []string
	// Stdout and Stderr receive the command's output.
	Stdout io.Writer
	Stderr io.Writer
	// Runner overrides command execution. A nil Runner runs the real command.
	Runner Runner
	// LookupEnv overrides the process environment. A nil function uses os.LookupEnv.
	LookupEnv func(string) (string, bool)
	// DryRun plans the command without running it.
	DryRun bool
}

// Result reports what a test run planned or did.
type Result struct {
	// DSN is the resolved test dsn, empty when none is configured.
	DSN string
	// Source names where the dsn came from. It is safe to print.
	Source string
	// Args is the go test argv after "go".
	Args []string
	// Env is the extra environment applied to go test.
	Env []string
	// Ran reports whether go test executed. It is false for a dry run.
	Ran bool
}

// Run executes a test request against the project in req.Dir.
func Run(req Request) (*Result, error) {
	dir := strings.TrimSpace(req.Dir)
	if dir == "" {
		dir = "."
	}
	mode := req.Mode
	if mode == "" {
		mode = ModeAuto
	}
	switch mode {
	case ModeAuto, ModeOff, ModeRequire:
	default:
		return nil, fmt.Errorf("weld test: unknown db mode %q (want auto, off or require)", mode)
	}

	result := &Result{}
	if mode != ModeOff {
		dsn, source, err := resolveTestDSN(req, dir)
		switch {
		case err == nil:
			result.DSN, result.Source = dsn, source
		case errors.Is(err, db.ErrTestDatabaseNotConfigured):
			if mode == ModeRequire {
				return nil, errors.New("weld test: no test database is configured; set database_test.name in config.yml or PG_TEST_DSN")
			}
		default:
			return nil, err
		}
	}

	args := []string{"test"}
	if len(req.Args) == 0 {
		args = append(args, "./...")
	} else {
		args = append(args, req.Args...)
	}
	result.Args = args
	if result.DSN != "" {
		// WELD_REQUIRE_DB_TESTS makes a failed connection a failure rather than a
		// skip, so a configured test database is really exercised.
		result.Env = []string{"PG_TEST_DSN=" + result.DSN, "WELD_REQUIRE_DB_TESTS=1"}
	}
	if req.DryRun {
		return result, nil
	}

	runner := req.Runner
	if runner == nil {
		runner = commandRunner(req.Stdout, req.Stderr)
	}
	if err := runner("go", dir, result.Env, args...); err != nil {
		return nil, fmt.Errorf("weld test: %w", err)
	}
	result.Ran = true
	return result, nil
}

// resolveTestDSN resolves the test dsn from the explicit flag, the environment
// or config.yml. A project without the db capability has no test database.
func resolveTestDSN(req Request, dir string) (string, string, error) {
	if dsn := strings.TrimSpace(req.DBURL); dsn != "" {
		return dsn, "--db-url", nil
	}
	manifest, err := project.Load(dir)
	if err != nil {
		return "", "", fmt.Errorf("weld test: %w", err)
	}
	if !manifest.HasCapability("db") {
		return "", "", db.ErrTestDatabaseNotConfigured
	}
	return db.ResolveTestDSN(dir, req.ConfigPath, req.LookupEnv)
}

// commandRunner returns the default Runner, which streams the command's output,
// inherits the process environment and overlays the extra environment.
func commandRunner(stdout, stderr io.Writer) Runner {
	return func(name, dir string, env []string, args ...string) error {
		cmd := exec.Command(name, args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), env...)
		cmd.Stdout = stdout
		cmd.Stderr = stderr
		cmd.Stdin = os.Stdin
		return cmd.Run()
	}
}
