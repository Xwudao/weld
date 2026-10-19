package db

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Xwudao/weld/internal/project"
)

// fakeProject writes the minimum a `weld db` command needs: a manifest with the
// db capability, the pinned tool module, a migration directory and a config file
// with database fields.
func fakeProject(t *testing.T, withDB bool) string {
	t.Helper()
	dir := t.TempDir()
	manifest := project.NewManifest("demo", "example.com/demo", "test")
	manifest.Base = project.CapabilityRef{Name: "base", Version: "0.3.0"}
	for _, name := range []string{"config", "loom", "http"} {
		manifest.AddCapability(project.CapabilityRef{Name: name, Version: "0.1.0"})
	}
	if withDB {
		manifest.AddCapability(project.CapabilityRef{Name: "db", Version: "0.1.1"})
	}
	encoded, err := manifest.Encode()
	if err != nil {
		t.Fatalf("encode manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, project.ManifestName), encoded, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{filepath.Join(ToolsDir, "go.mod"), MigrationsDir} {
		absolute := filepath.Join(dir, path)
		if strings.HasSuffix(path, "go.mod") {
			if err := os.MkdirAll(filepath.Dir(absolute), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(absolute, []byte("module example.com/demo/db/tools\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := os.MkdirAll(absolute, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	config := "database:\n  host: db.internal\n  port: 6543\n  user: app\n  password: file-secret\n  name: appdb\n"
	if err := os.WriteFile(filepath.Join(dir, "config.yml"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func noEnv(string) (string, bool) { return "", false }

// TestMigrateBuildsThePinnedGooseCommand covers the default config path, the
// recorded dsn fields, and the redacted display form.
func TestMigrateBuildsThePinnedGooseCommand(t *testing.T) {
	dir := fakeProject(t, true)
	var gotName, gotDir string
	var gotEnv, gotArgs []string
	runner := func(name, dir string, env []string, args ...string) error {
		gotName, gotDir, gotEnv, gotArgs = name, dir, env, append([]string(nil), args...)
		return nil
	}
	result, err := Migrate(Request{Dir: dir, Action: ActionUp, Runner: runner, LookupEnv: noEnv})
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if !result.Applied || result.Source != filepath.Join(dir, "config.yml") {
		t.Fatalf("result = %+v", result)
	}
	if gotName != "go" || gotDir != filepath.Join(dir, ToolsDir) {
		t.Fatalf("command = %s in %s", gotName, gotDir)
	}
	if !strings.Contains(strings.Join(gotEnv, ","), "GOWORK=off") {
		t.Fatalf("env = %v", gotEnv)
	}
	wantPrefix := []string{"tool", "goose", "-dir", "../migrations", "-table", gooseTable, "postgres"}
	if len(gotArgs) < len(wantPrefix)+2 {
		t.Fatalf("args = %v", gotArgs)
	}
	for i, want := range wantPrefix {
		if gotArgs[i] != want {
			t.Fatalf("args[%d] = %q, want %q (args %v)", i, gotArgs[i], want, gotArgs)
		}
	}
	if got := gotArgs[len(wantPrefix)]; !strings.Contains(got, "appdb") || !strings.Contains(got, "db.internal:6543") {
		t.Fatalf("dsn arg = %q", got)
	}
	if gotArgs[len(gotArgs)-1] != "up" {
		t.Fatalf("action arg = %q", gotArgs[len(gotArgs)-1])
	}
	if display := result.Step.String(); strings.Contains(display, "file-secret") || !strings.Contains(display, "$DATABASE_URL") {
		t.Fatalf("display command leaks the dsn or omits the placeholder: %s", display)
	}
}

// TestMigrateAllowMissing pins the opt-in flag for an out-of-order migration:
// it is placed before the database driver argument and is absent by default.
func TestMigrateAllowMissing(t *testing.T) {
	dir := fakeProject(t, true)
	var gotArgs []string
	runner := func(_ string, _ string, _ []string, args ...string) error {
		gotArgs = append([]string(nil), args...)
		return nil
	}
	result, err := Migrate(Request{Dir: dir, Action: ActionUp, AllowMissing: true, Runner: runner, LookupEnv: noEnv})
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	joined := strings.Join(gotArgs, " ")
	if !strings.Contains(joined, "-allow-missing postgres") {
		t.Fatalf("args = %v, want -allow-missing before postgres", gotArgs)
	}
	if !strings.Contains(result.Step.Display, "-allow-missing postgres $DATABASE_URL up") {
		t.Fatalf("display = %s", result.Step.Display)
	}
	if _, err := Migrate(Request{Dir: dir, Action: ActionStatus, AllowMissing: true, DryRun: true, LookupEnv: noEnv}); err == nil {
		t.Fatal("status accepted --allow-missing")
	}
}

// TestMigrateDownRequiresConfirmation proves the destructive action is refused
// before any database connection.
func TestMigrateDownRequiresConfirmation(t *testing.T) {
	dir := fakeProject(t, true)
	called := false
	runner := func(string, string, []string, ...string) error { called = true; return nil }
	if _, err := Migrate(Request{Dir: dir, Action: ActionDown, Runner: runner, LookupEnv: noEnv}); err == nil {
		t.Fatal("down without confirmation succeeded")
	}
	if called {
		t.Fatal("runner was called without confirmation")
	}
	result, err := Migrate(Request{Dir: dir, Action: ActionDown, Confirm: true, Runner: runner, LookupEnv: noEnv})
	if err != nil {
		t.Fatalf("confirmed down: %v", err)
	}
	if !result.Applied || result.Step.Args[len(result.Step.Args)-1] != "down" {
		t.Fatalf("result = %+v", result)
	}
}

// TestMigrateDryRunWritesNothing proves a dry run plans the command without
// invoking the tool or touching a database.
func TestMigrateDryRunWritesNothing(t *testing.T) {
	dir := fakeProject(t, true)
	called := false
	result, err := Migrate(Request{Dir: dir, Action: ActionStatus, DryRun: true, LookupEnv: noEnv, Runner: func(string, string, []string, ...string) error {
		called = true
		return nil
	}})
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if called || result.Applied {
		t.Fatal("dry run invoked the runner")
	}
	if !strings.Contains(result.Step.String(), "status") {
		t.Fatalf("plan = %s", result.Step)
	}
}

// TestResolveDSNPrecedence pins the dsn resolution order and the environment
// overrides shared with the generated configuration loader.
func TestResolveDSNPrecedence(t *testing.T) {
	dir := fakeProject(t, true)
	env := map[string]string{}
	lookup := func(key string) (string, bool) { value, ok := env[key]; return value, ok }

	dsn, source, err := ResolveDSN(dir, "", lookup)
	if err != nil {
		t.Fatalf("ResolveDSN: %v", err)
	}
	if !strings.Contains(dsn, "app:file-secret@db.internal:6543/appdb") || source != filepath.Join(dir, "config.yml") {
		t.Fatalf("dsn = %q, source = %q", dsn, source)
	}

	env[EnvDBHost] = "override.example"
	env[EnvDBPort] = "7432"
	env[EnvDBUser] = "override"
	env[EnvDBPassword] = "override-secret"
	env[EnvDBName] = "override_db"
	dsn, _, err = ResolveDSN(dir, "", lookup)
	if err != nil {
		t.Fatalf("ResolveDSN env: %v", err)
	}
	for _, want := range []string{"override:override-secret@override.example:7432/override_db"} {
		if !strings.Contains(dsn, want) {
			t.Fatalf("env dsn = %q, want %q", dsn, want)
		}
	}

	env[EnvDatabaseURL] = "postgres://url-user:url-secret@url-host:5432/url-db"
	dsn, source, err = ResolveDSN(dir, "", lookup)
	if err != nil {
		t.Fatalf("ResolveDSN DATABASE_URL: %v", err)
	}
	if dsn != env[EnvDatabaseURL] || source != EnvDatabaseURL {
		t.Fatalf("dsn = %q, source = %q", dsn, source)
	}

	// An explicitly blank DATABASE_URL is not a target; the file still applies.
	env[EnvDatabaseURL] = "   "
	delete(env, "DB_HOST")
	delete(env, "DB_PORT")
	delete(env, "DB_USER")
	delete(env, "DB_PASSWORD")
	delete(env, "DB_NAME")
	dsn, _, err = ResolveDSN(dir, "", lookup)
	if err != nil || !strings.Contains(dsn, "appdb") {
		t.Fatalf("blank DATABASE_URL did not fall back: dsn %q, err %v", dsn, err)
	}
}

// TestResolveDSNReportsActionableErrors proves a missing file, invalid port and
// missing required fields fail with a message the caller can act on, without
// echoing a secret.
func TestResolveDSNReportsActionableErrors(t *testing.T) {
	dir := fakeProject(t, true)
	if _, _, err := ResolveDSN(t.TempDir(), "", noEnv); err == nil || !strings.Contains(err.Error(), "DATABASE_URL") {
		t.Fatalf("missing config error = %v", err)
	}
	for name, env := range map[string]map[string]string{
		"invalid port":  {EnvDBPort: "not-a-port"},
		"negative port": {EnvDBPort: "-1"},
		"missing user":  {EnvDBUser: ""},
		"missing name":  {EnvDBName: ""},
	} {
		lookup := func(key string) (string, bool) { value, ok := env[key]; return value, ok }
		if name == "missing user" || name == "missing name" {
			// An explicitly empty override is the value that makes the field
			// required-and-blank; a map alone cannot distinguish unset from empty.
			lookup = func(key string) (string, bool) {
				if key == EnvDBUser || key == EnvDBName {
					return "", true
				}
				return "", false
			}
		}
		if _, _, err := ResolveDSN(dir, "", lookup); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

// TestCreateWritesTimestampedGooseSkeleton covers the created filename, content,
// dry-run and duplicate-name behavior.
func TestCreateWritesTimestampedGooseSkeleton(t *testing.T) {
	dir := fakeProject(t, true)
	fixed := time.Date(2024, time.January, 2, 3, 4, 5, 0, time.UTC)
	result, err := Create(CreateRequest{Dir: dir, Name: "create_users", Now: func() time.Time { return fixed }})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	want := filepath.Join(MigrationsDir, "20240102030405_create_users.sql")
	if result.Path != want || !result.Written {
		t.Fatalf("result = %+v, want path %s", result, want)
	}
	content, err := os.ReadFile(filepath.Join(dir, want))
	if err != nil {
		t.Fatal(err)
	}
	for _, marker := range []string{"-- +goose Up", "-- +goose Down"} {
		if !strings.Contains(string(content), marker) {
			t.Errorf("migration is missing %q:\n%s", marker, content)
		}
	}

	dry, err := Create(CreateRequest{Dir: dir, Name: "create_orders", DryRun: true, Now: func() time.Time { return fixed }})
	if err != nil {
		t.Fatalf("dry Create: %v", err)
	}
	if dry.Written {
		t.Fatal("dry run wrote a file")
	}
	if _, err := os.Stat(filepath.Join(dir, dry.Path)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("dry run left %s", dry.Path)
	}

	if _, err := Create(CreateRequest{Dir: dir, Name: "create_users", Now: func() time.Time { return fixed }}); err == nil {
		t.Fatal("duplicate migration name was accepted")
	}
	if _, err := Create(CreateRequest{Dir: dir, Name: "Bad-Name"}); err == nil {
		t.Fatal("invalid migration name was accepted")
	}
}

// TestCreateSortsAfterTheHighestMigration proves a new skeleton never lands
// before a migration the project already has: goose refuses an older pending
// migration without --allow-missing.
func TestCreateSortsAfterTheHighestMigration(t *testing.T) {
	dir := fakeProject(t, true)
	existing := filepath.Join(dir, MigrationsDir, "20300101000000_future.sql")
	if err := os.WriteFile(existing, []byte("-- +goose Up\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	fixed := time.Date(2024, time.January, 2, 3, 4, 5, 0, time.UTC)
	result, err := Create(CreateRequest{Dir: dir, Name: "create_orders", Now: func() time.Time { return fixed }})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	want := filepath.Join(MigrationsDir, "20300101000001_create_orders.sql")
	if result.Path != want {
		t.Fatalf("path = %s, want %s (one past the highest existing)", result.Path, want)
	}
}

// TestCommandsRequireTheDBCapability proves a project without db is rejected
// before any command is planned.
func TestCommandsRequireTheDBCapability(t *testing.T) {
	dir := fakeProject(t, false)
	if _, err := Migrate(Request{Dir: dir, Action: ActionUp, LookupEnv: noEnv}); err == nil {
		t.Fatal("Migrate accepted a project without db")
	}
	if _, err := Create(CreateRequest{Dir: dir, Name: "items"}); err == nil {
		t.Fatal("Create accepted a project without db")
	}
}
