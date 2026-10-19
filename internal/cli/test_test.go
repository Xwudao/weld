package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestTestCommandPlan covers the weld test surface without running go test: a
// fresh db project has no test database, --db=require fails, and a configured
// test database is reported by source rather than by dsn.
func TestTestCommandPlan(t *testing.T) {
	root := t.TempDir()
	if _, err := run(t, "new", "demo", "--module", "example.com/demo", "--dir", root); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "demo")
	if _, err := run(t, "add", "db", "--with-dependencies", "--dir", dir); err != nil {
		t.Fatalf("add db: %v", err)
	}

	out, err := run(t, "test", "--dry-run", "--dir", dir)
	if err != nil {
		t.Fatalf("test dry run: %v", err)
	}
	if !strings.Contains(out, "go test ./...") || !strings.Contains(out, "none (integration tests skip)") {
		t.Fatalf("unconfigured plan = %s", out)
	}

	if _, err := run(t, "test", "--db=require", "--dry-run", "--dir", dir); err == nil || !strings.Contains(err.Error(), "no test database") {
		t.Fatalf("require without a test database error = %v", err)
	}

	// Configure the dedicated test database and read the plan again.
	path := filepath.Join(dir, "config.yml")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	updated := strings.Replace(string(raw), "database_test:\n  name: \"\"", "database_test:\n  name: appdb_test\n  host: localhost\n  port: 5432\n  user: app\n  password: test-secret", 1)
	if updated == string(raw) {
		t.Fatalf("could not find the database_test block:\n%s", raw)
	}
	if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err = run(t, "test", "--dry-run", "--dir", dir)
	if err != nil {
		t.Fatalf("configured dry run: %v", err)
	}
	if !strings.Contains(out, "test database: "+path) {
		t.Fatalf("configured plan = %s", out)
	}
	if strings.Contains(out, "test-secret") {
		t.Fatalf("the plan printed a credential: %s", out)
	}

	const sentinel = "sentinel-test-password-do-not-print"
	out, err = run(t, "test", "--db-url", "postgres://user:"+sentinel+"@localhost:5432/app_test", "--dry-run", "--dir", dir)
	if err != nil {
		t.Fatalf("explicit dsn dry run: %v", err)
	}
	if !strings.Contains(out, "--db-url") || strings.Contains(out, sentinel) {
		t.Fatalf("explicit dsn plan = %s", out)
	}
}

// TestTestCommandPassesGoArgs proves positional arguments are forwarded to go
// test in order, including flags after the -- separator.
func TestTestCommandPassesGoArgs(t *testing.T) {
	root := t.TempDir()
	if _, err := run(t, "new", "demo", "--module", "example.com/demo", "--dir", root); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "demo")
	out, err := run(t, "test", "--db=off", "--dry-run", "--dir", dir, "./internal/data/...", "--", "-run", "Integration")
	if err != nil {
		t.Fatalf("test dry run: %v", err)
	}
	if !strings.Contains(out, "go test ./internal/data/... -run Integration") {
		t.Fatalf("plan = %s", out)
	}
}
