package db

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// sqlcProject writes the files sqlc.yaml names on top of the shared db fixture.
func sqlcProject(t *testing.T) string {
	t.Helper()
	dir := fakeProject(t, true)
	for _, path := range []string{"sqlc.yaml", filepath.Join("db", "query")} {
		absolute := filepath.Join(dir, path)
		if strings.HasSuffix(path, ".yaml") {
			if err := os.WriteFile(absolute, []byte("version: \"2\"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := os.MkdirAll(absolute, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// TestSQLCBuildsThePinnedGeneratorCommand covers the generated command, its
// working directory and the redaction-free display form.
func TestSQLCBuildsThePinnedGeneratorCommand(t *testing.T) {
	dir := sqlcProject(t)
	var gotName, gotDir string
	var gotEnv, gotArgs []string
	runner := func(name, dir string, env []string, args ...string) error {
		gotName, gotDir, gotEnv, gotArgs = name, dir, env, append([]string(nil), args...)
		return nil
	}
	result, err := SQLC(SQLCRequest{Dir: dir, Runner: runner})
	if err != nil {
		t.Fatalf("SQLC: %v", err)
	}
	if !result.Applied || result.Check {
		t.Fatalf("result = %+v", result)
	}
	if gotName != "go" || gotDir != filepath.Join(dir, ToolsDir) {
		t.Fatalf("command = %s in %s", gotName, gotDir)
	}
	if !strings.Contains(strings.Join(gotEnv, ","), "GOWORK=off") {
		t.Fatalf("env = %v", gotEnv)
	}
	wantArgs := []string{"tool", "sqlc", "generate", "-f", "../../sqlc.yaml"}
	if strings.Join(gotArgs, " ") != strings.Join(wantArgs, " ") {
		t.Fatalf("args = %v, want %v", gotArgs, wantArgs)
	}
	wantDisplay := "go tool sqlc generate -f ../../sqlc.yaml"
	if result.Step.Display != wantDisplay {
		t.Fatalf("display = %q, want %q", result.Step.Display, wantDisplay)
	}
}

// TestSQLCCheckUsesDiff pins the CI form: sqlc diff never rewrites a file.
func TestSQLCCheckUsesDiff(t *testing.T) {
	dir := sqlcProject(t)
	var gotArgs []string
	runner := func(_ string, _ string, _ []string, args ...string) error {
		gotArgs = append([]string(nil), args...)
		return nil
	}
	result, err := SQLC(SQLCRequest{Dir: dir, Check: true, Runner: runner})
	if err != nil {
		t.Fatalf("SQLC check: %v", err)
	}
	if !result.Check || !result.Applied {
		t.Fatalf("result = %+v", result)
	}
	wantArgs := []string{"tool", "sqlc", "diff", "-f", "../../sqlc.yaml"}
	if strings.Join(gotArgs, " ") != strings.Join(wantArgs, " ") {
		t.Fatalf("args = %v, want %v", gotArgs, wantArgs)
	}
}

// TestSQLCCheckFailureIsActionable proves a stale tree fails with the command
// that repairs it.
func TestSQLCCheckFailureIsActionable(t *testing.T) {
	dir := sqlcProject(t)
	runner := func(string, string, []string, ...string) error { return errors.New("exit status 1") }
	_, err := SQLC(SQLCRequest{Dir: dir, Check: true, Runner: runner})
	if err == nil {
		t.Fatal("a stale tree passed the check")
	}
	for _, want := range []string{"out of date", "weld gen sqlc"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error = %v, want %q", err, want)
		}
	}
}

// TestSQLCDryRunDoesNotRun proves a dry run only builds the plan.
func TestSQLCDryRunDoesNotRun(t *testing.T) {
	dir := sqlcProject(t)
	called := false
	result, err := SQLC(SQLCRequest{Dir: dir, DryRun: true, Runner: func(string, string, []string, ...string) error {
		called = true
		return nil
	}})
	if err != nil {
		t.Fatalf("SQLC dry run: %v", err)
	}
	if called || result.Applied {
		t.Fatal("dry run invoked the generator")
	}
	if !strings.Contains(result.Step.String(), "go tool sqlc generate") {
		t.Fatalf("plan = %s", result.Step)
	}
}

// TestSQLCRequiresTheDBCapabilityAndPayload proves the preconditions fail before
// the pinned tool is built.
func TestSQLCRequiresTheDBCapabilityAndPayload(t *testing.T) {
	if _, err := SQLC(SQLCRequest{Dir: fakeProject(t, false), DryRun: true}); err == nil || !strings.Contains(err.Error(), "db capability") {
		t.Fatalf("missing db capability error = %v", err)
	}
	dir := sqlcProject(t)
	if err := os.Remove(filepath.Join(dir, "sqlc.yaml")); err != nil {
		t.Fatal(err)
	}
	if _, err := SQLC(SQLCRequest{Dir: dir, DryRun: true}); err == nil || !strings.Contains(err.Error(), "sqlc.yaml is missing") {
		t.Fatalf("missing sqlc.yaml error = %v", err)
	}
}
