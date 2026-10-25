package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAddMiddlewareSubcommand(t *testing.T) {
	root := t.TempDir()
	if _, err := run(t, "new", "demo", "--module", "example.com/demo", "--dir", root); err != nil {
		t.Fatalf("new: %v", err)
	}
	dir := filepath.Join(root, "demo")
	path := filepath.Join(dir, "internal", "middleware", "audit.go")

	// Without http the requirement is named, not installed.
	if _, err := run(t, "add", "middleware", "audit", "--dir", dir); err == nil || !strings.Contains(err.Error(), "--with-dependencies") {
		t.Fatalf("add middleware without http error = %v, want a missing-requirement error", err)
	}
	if _, err := run(t, "add", "middleware", "Audit", "--dir", dir, "--with-dependencies"); err == nil || !strings.Contains(err.Error(), "invalid middleware name") {
		t.Fatalf("add middleware Audit error = %v, want a name error", err)
	}

	out, err := run(t, "add", "middleware", "audit", "--with-dependencies", "--dir", dir, "--dry-run")
	if err != nil {
		t.Fatalf("add middleware --dry-run: %v", err)
	}
	if !strings.Contains(out, "dry run") || !strings.Contains(out, "add internal/middleware/audit.go") {
		t.Fatalf("dry-run output = %q", out)
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Fatal("a dry run wrote the middleware")
	}

	out, err = run(t, "add", "middleware", "audit", "--with-dependencies", "--dir", dir)
	if err != nil {
		t.Fatalf("add middleware: %v", err)
	}
	if !strings.Contains(out, "add internal/middleware/audit_test.go") || !strings.Contains(out, "middleware.Audit()") {
		t.Fatalf("add middleware output = %q", out)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("middleware not written: %v", err)
	}

	out, err = run(t, "add", "middleware", "audit", "--dir", dir)
	if err != nil {
		t.Fatalf("repeat add middleware: %v", err)
	}
	if !strings.Contains(out, "already installed") {
		t.Fatalf("repeat add middleware output = %q", out)
	}
}
