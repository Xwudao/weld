package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAddListenerSubcommand(t *testing.T) {
	root := t.TempDir()
	if _, err := run(t, "new", "demo", "--module", "example.com/demo", "--dir", root); err != nil {
		t.Fatalf("new: %v", err)
	}
	dir := filepath.Join(root, "demo")
	path := filepath.Join(dir, "internal", "listeners", "item_created.go")

	if _, err := run(t, "add", "listener", "item_created", "--dir", dir); err == nil || !strings.Contains(err.Error(), "--with-dependencies") {
		t.Fatalf("add listener without events error = %v, want a missing-requirement error", err)
	}
	if _, err := run(t, "add", "listener", "item_test", "--with-dependencies", "--dir", dir); err == nil || !strings.Contains(err.Error(), "invalid listener name") {
		t.Fatalf("add listener item_test error = %v, want a name error", err)
	}

	out, err := run(t, "add", "listener", "item_created", "--with-dependencies", "--dir", dir, "--dry-run")
	if err != nil {
		t.Fatalf("add listener --dry-run: %v", err)
	}
	if !strings.Contains(out, "dry run") || !strings.Contains(out, "add internal/listeners/item_created.go") {
		t.Fatalf("dry-run output = %q", out)
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Fatal("a dry run wrote the listener")
	}

	out, err = run(t, "add", "listener", "item_created", "--with-dependencies", "--dir", dir)
	if err != nil {
		t.Fatalf("add listener: %v", err)
	}
	if !strings.Contains(out, "add internal/listeners/item_created_test.go") || !strings.Contains(out, "listeners.HandleItemCreated") {
		t.Fatalf("add listener output = %q", out)
	}

	out, err = run(t, "add", "listener", "item_created", "--dir", dir)
	if err != nil {
		t.Fatalf("repeat add listener: %v", err)
	}
	if !strings.Contains(out, "already installed") {
		t.Fatalf("repeat add listener output = %q", out)
	}
}
