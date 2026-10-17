package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDBCommands covers the migration and creation surface end to end. It uses a
// dry run for the goose command so the test never touches a database.
func TestDBCommands(t *testing.T) {
	root := t.TempDir()
	if _, err := run(t, "new", "demo", "--module", "example.com/demo", "--dir", root); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "demo")
	if _, err := run(t, "add", "db", "--with-dependencies", "--dir", dir); err != nil {
		t.Fatalf("add db: %v", err)
	}

	const sentinel = "sentinel-db-password-do-not-print"
	t.Setenv("DATABASE_URL", "postgres://user:"+sentinel+"@localhost:5432/app?sslmode=disable")

	out, err := run(t, "db", "migrate", "up", "--dry-run", "--dir", dir)
	if err != nil {
		t.Fatalf("migrate up dry run: %v", err)
	}
	if !strings.Contains(out, "go tool goose") || !strings.Contains(out, "$DATABASE_URL") {
		t.Fatalf("plan lacks the goose command or placeholder: %s", out)
	}
	if strings.Contains(out, sentinel) {
		t.Fatalf("dry run printed the dsn: %s", out)
	}
	if strings.Contains(out, "-allow-missing") {
		t.Fatalf("default up plan unexpectedly allows missing migrations: %s", out)
	}
	out, err = run(t, "db", "migrate", "up", "--allow-missing", "--dry-run", "--dir", dir)
	if err != nil {
		t.Fatalf("migrate up --allow-missing dry run: %v", err)
	}
	if !strings.Contains(out, "-allow-missing") {
		t.Fatalf("allow-missing plan lacks the flag: %s", out)
	}

	if _, err := run(t, "db", "migrate", "down", "--dir", dir); err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("down without confirmation error = %v", err)
	}
	out, err = run(t, "db", "migrate", "down", "--yes", "--dry-run", "--dir", dir)
	if err != nil || !strings.Contains(out, "down") {
		t.Fatalf("confirmed down dry run: %s %v", out, err)
	}

	out, err = run(t, "db", "create", "create_users", "--dry-run", "--dir", dir)
	if err != nil {
		t.Fatalf("create dry run: %v", err)
	}
	if !strings.Contains(out, filepath.Join("db", "migrations")) || !strings.Contains(out, "_create_users.sql") {
		t.Fatalf("create plan = %s", out)
	}
	if matches, err := filepath.Glob(filepath.Join(dir, "db", "migrations", "*_create_users.sql")); err != nil || len(matches) != 0 {
		t.Fatalf("dry run wrote a migration: %v %v", matches, err)
	}

	out, err = run(t, "db", "create", "create_users", "--dir", dir)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	created := strings.TrimSpace(strings.TrimPrefix(out, "Created "))
	content, err := os.ReadFile(filepath.Join(dir, created))
	if err != nil {
		t.Fatalf("read created migration: %v", err)
	}
	if !strings.Contains(string(content), "-- +goose Up") || !strings.Contains(string(content), "-- +goose Down") {
		t.Fatalf("created migration is not a goose skeleton:\n%s", content)
	}
	if _, err := run(t, "db", "create", "create_users", "--dir", dir); err == nil {
		t.Fatal("duplicate migration name was accepted")
	}

	if out, err := run(t, "db", "--help"); err != nil || !strings.Contains(out, "migrate") {
		t.Fatalf("db help: %s %v", out, err)
	}
}

// TestDBCommandsRequireDBCapability proves the commands reject a project that
// never installed db instead of guessing a schema layout.
func TestDBCommandsRequireDBCapability(t *testing.T) {
	root := t.TempDir()
	if _, err := run(t, "new", "demo", "--module", "example.com/demo", "--dir", root); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "demo")
	if _, err := run(t, "db", "migrate", "up", "--dry-run", "--dir", dir); err == nil || !strings.Contains(err.Error(), "weld add db") {
		t.Fatalf("migrate without db error = %v", err)
	}
	if _, err := run(t, "db", "create", "items", "--dir", dir); err == nil || !strings.Contains(err.Error(), "weld add db") {
		t.Fatalf("create without db error = %v", err)
	}
}
