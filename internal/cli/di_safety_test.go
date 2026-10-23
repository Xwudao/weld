package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGeneratedDIConflictBlocksAddAndUpgradeIncludingDryRun(t *testing.T) {
	root := t.TempDir()
	if _, err := run(t, "new", "demo", "--module", "example.com/demo", "--dir", root); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "demo")
	if _, err := run(t, "add", "http", "--with-dependencies", "--dir", dir); err != nil {
		t.Fatal(err)
	}
	read := func(path string) string {
		t.Helper()
		raw, err := os.ReadFile(filepath.Join(dir, path))
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	original := read("internal/di/di.go")
	manifest := read("weld.json")
	modified := original + "\n// handwritten business dependency\n"
	if err := os.WriteFile(filepath.Join(dir, "internal/di/di.go"), []byte(modified), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"add", "cron", "--dry-run"},
		{"add", "cron"},
		{"upgrade", "--dry-run"},
		{"upgrade"},
	} {
		_, err := run(t, append(args, "--dir", dir)...)
		if err == nil || !strings.Contains(err.Error(), "generated DI source was modified") || !strings.Contains(err.Error(), "--- a/internal/di/di.go") {
			t.Fatalf("%v: want source conflict with diff, got %v", args, err)
		}
		if read("internal/di/di.go") != modified || read("weld.json") != manifest {
			t.Fatalf("%v changed the project despite conflict", args)
		}
		if _, err := os.Stat(filepath.Join(dir, "internal/cron")); !os.IsNotExist(err) {
			t.Fatalf("%v wrote cron files: %v", args, err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "internal/di/di.go"), []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, "add", "cron", "--dir", dir, "--dry-run"); err != nil {
		t.Fatal(err)
	}
	if read("internal/di/di.go") != original || read("weld.json") != manifest {
		t.Fatal("successful dry run changed the project")
	}
	if _, err := run(t, "add", "cron", "--dir", dir); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(read("internal/di/di.go"), "loom.Provide(NewScheduler)") {
		t.Fatal("restoring generated source did not unblock cron installation")
	}
}
