package testrun

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Xwudao/weld/internal/project"
)

// fakeProject writes a minimal project with the given capabilities and
// config.yml.
func fakeProject(t *testing.T, capabilities []string, config string) string {
	t.Helper()
	dir := t.TempDir()
	manifest := project.NewManifest("demo", "example.com/demo", "test")
	manifest.Base = project.CapabilityRef{Name: "base", Version: "0.3.0"}
	for _, name := range capabilities {
		manifest.AddCapability(project.CapabilityRef{Name: name, Version: "0.1.0"})
	}
	encoded, err := manifest.Encode()
	if err != nil {
		t.Fatalf("encode manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, project.ManifestName), encoded, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.yml"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

const configuredDatabase = `environment: local
database:
  host: db.internal
  port: 6543
  user: app
  password: file-secret
  name: appdb
database_test:
  name: appdb_test
`

func noEnv(string) (string, bool) { return "", false }

// TestRunInjectsConfiguredTestDSN proves weld test resolves the configured test
// database and passes it to go test through the environment, never argv.
func TestRunInjectsConfiguredTestDSN(t *testing.T) {
	dir := fakeProject(t, []string{"config", "loom", "db"}, configuredDatabase)
	var gotName, gotDir string
	var gotEnv, gotArgs []string
	runner := func(name, dir string, env []string, args ...string) error {
		gotName, gotDir, gotEnv, gotArgs = name, dir, env, append([]string(nil), args...)
		return nil
	}
	result, err := Run(Request{Dir: dir, Runner: runner, LookupEnv: noEnv})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !result.Ran || !strings.Contains(result.DSN, "@db.internal:6543/appdb_test") {
		t.Fatalf("result = %+v", result)
	}
	if gotName != "go" || gotDir != dir {
		t.Fatalf("command = %s in %s", gotName, gotDir)
	}
	if strings.Join(gotArgs, " ") != "test ./..." {
		t.Fatalf("args = %v", gotArgs)
	}
	env := strings.Join(gotEnv, "\n")
	if !strings.Contains(env, "PG_TEST_DSN=postgres://") || !strings.Contains(env, "WELD_REQUIRE_DB_TESTS=1") {
		t.Fatalf("env = %v", gotEnv)
	}
	for _, arg := range gotArgs {
		if strings.Contains(arg, "PG_TEST_DSN") || strings.Contains(arg, "file-secret") {
			t.Fatalf("the dsn leaked into argv: %v", gotArgs)
		}
	}
}

// TestRunModeOffIgnoresTestDatabase proves --db=off runs the tests without a
// test database even when config.yml has one.
func TestRunModeOffIgnoresTestDatabase(t *testing.T) {
	dir := fakeProject(t, []string{"config", "loom", "db"}, configuredDatabase)
	var gotEnv []string
	runner := func(name, dir string, env []string, args ...string) error {
		gotEnv = append([]string(nil), env...)
		return nil
	}
	result, err := Run(Request{Dir: dir, Mode: ModeOff, Runner: runner, LookupEnv: noEnv})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.DSN != "" || len(gotEnv) != 0 {
		t.Fatalf("result = %+v, env = %v", result, gotEnv)
	}
}

// TestRunRequireWithoutTestDatabase proves --db=require fails instead of
// running tests that would silently skip.
func TestRunRequireWithoutTestDatabase(t *testing.T) {
	dir := fakeProject(t, []string{"config", "loom", "db"}, "environment: local\ndatabase:\n  host: db\n  user: app\n  name: appdb\n")
	runner := func(name, dir string, env []string, args ...string) error {
		t.Fatal("runner ran despite a missing required test database")
		return nil
	}
	if _, err := Run(Request{Dir: dir, Mode: ModeRequire, Runner: runner, LookupEnv: noEnv}); err == nil || !strings.Contains(err.Error(), "no test database") {
		t.Fatalf("error = %v", err)
	}
}

// TestRunWithoutDBCapabilityRunsPlainTests proves weld test works in a project
// that never installed db.
func TestRunWithoutDBCapabilityRunsPlainTests(t *testing.T) {
	dir := fakeProject(t, []string{"config"}, "environment: local\n")
	var gotEnv []string
	runner := func(name, dir string, env []string, args ...string) error {
		gotEnv = append([]string(nil), env...)
		return nil
	}
	result, err := Run(Request{Dir: dir, Runner: runner, LookupEnv: noEnv})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.DSN != "" || len(gotEnv) != 0 {
		t.Fatalf("result = %+v, env = %v", result, gotEnv)
	}
}

// TestRunDryRunDoesNotExecute proves a dry run prints the plan without invoking
// go test.
func TestRunDryRunDoesNotExecute(t *testing.T) {
	dir := fakeProject(t, []string{"config", "loom", "db"}, configuredDatabase)
	runner := func(name, dir string, env []string, args ...string) error {
		t.Fatal("dry run executed go test")
		return nil
	}
	result, err := Run(Request{Dir: dir, DryRun: true, Runner: runner, LookupEnv: noEnv})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Ran || result.DSN == "" || strings.Join(result.Args, " ") != "test ./..." {
		t.Fatalf("result = %+v", result)
	}
}

// TestRunPassesGoTestArgs proves explicit arguments replace the default package
// list and are passed through in order.
func TestRunPassesGoTestArgs(t *testing.T) {
	dir := fakeProject(t, []string{"config"}, "environment: local\n")
	var gotArgs []string
	runner := func(name, dir string, env []string, args ...string) error {
		gotArgs = append([]string(nil), args...)
		return nil
	}
	if _, err := Run(Request{Dir: dir, Args: []string{"./internal/data/...", "-run", "Integration"}, Runner: runner, LookupEnv: noEnv}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if strings.Join(gotArgs, " ") != "test ./internal/data/... -run Integration" {
		t.Fatalf("args = %v", gotArgs)
	}
}
