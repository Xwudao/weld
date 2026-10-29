package db

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Xwudao/weld/internal/project"
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

// sqlcxProject is sqlcProject with the sqlcx capability recorded and its tools
// module written.
func sqlcxProject(t *testing.T) string {
	t.Helper()
	dir := sqlcProject(t)
	manifest, err := project.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	manifest.AddCapability(project.CapabilityRef{Name: "sqlcx", Version: "0.1.0"})
	encoded, err := manifest.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, project.ManifestName), encoded, 0o644); err != nil {
		t.Fatal(err)
	}
	goMod := filepath.Join(dir, SQLCXToolsDir, "go.mod")
	if err := os.MkdirAll(filepath.Dir(goMod), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(goMod, []byte("module example.com/demo/tools/sqlcx\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestSQLCRunsSQLCDynamicWhenSQLCXIsInstalled proves that once sqlc.yaml names
// the sqlc-dynamic plugin, generation and the check both go through
// sqlc-dynamic from its own tools module: plain sqlc could not find the plugin.
func TestSQLCRunsSQLCDynamicWhenSQLCXIsInstalled(t *testing.T) {
	for _, check := range []bool{false, true} {
		dir := sqlcxProject(t)
		var gotDir string
		var gotArgs []string
		runner := func(_ string, dir string, _ []string, args ...string) error {
			gotDir, gotArgs = dir, append([]string(nil), args...)
			return nil
		}
		result, err := SQLC(SQLCRequest{Dir: dir, Check: check, Runner: runner})
		if err != nil {
			t.Fatalf("SQLC check=%v: %v", check, err)
		}
		want := "tool sqlc-dynamic generate -f ../../sqlc.yaml"
		if check {
			want = "tool sqlc-dynamic generate -check -f ../../sqlc.yaml"
		}
		if got := strings.Join(gotArgs, " "); got != want {
			t.Fatalf("check=%v: args = %q, want %q", check, got, want)
		}
		if gotDir != filepath.Join(dir, SQLCXToolsDir) {
			t.Fatalf("check=%v: dir = %s", check, gotDir)
		}
		if result.Step.Display != "go "+want {
			t.Fatalf("check=%v: display = %q", check, result.Step.Display)
		}
	}
}

// TestSQLCRequiresTheSQLCXToolsModule proves a missing tools/sqlcx module names
// the capability that restores it.
func TestSQLCRequiresTheSQLCXToolsModule(t *testing.T) {
	dir := sqlcxProject(t)
	if err := os.RemoveAll(filepath.Join(dir, SQLCXToolsDir)); err != nil {
		t.Fatal(err)
	}
	_, err := SQLC(SQLCRequest{Dir: dir, DryRun: true})
	if err == nil || !strings.Contains(err.Error(), "tools/sqlcx/go.mod is missing; reinstall with: weld add sqlcx") {
		t.Fatalf("missing tools/sqlcx error = %v", err)
	}
}
