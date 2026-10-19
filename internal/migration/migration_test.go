package migration

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestVersionUsesTheGooseTimestampLayout(t *testing.T) {
	t.Parallel()
	fixed := time.Date(2024, time.January, 2, 3, 4, 5, 0, time.UTC)
	if got, want := Version(fixed), int64(20240102030405); got != want {
		t.Fatalf("Version = %d, want %d", got, want)
	}
	if got, want := Name(Version(fixed), "create_users"), "20240102030405_create_users.sql"; got != want {
		t.Fatalf("Name = %q, want %q", got, want)
	}
}

func TestParseReadsTheVersionPrefix(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		version int64
		ok      bool
	}{
		{"20240102030405_create_users.sql", 20240102030405, true},
		{"db/migrations/000001_create_items.sql", 1, true},
		{"create_users.sql", 0, false},
		{"_create_users.sql", 0, false},
		{"20240102x_create_users.sql", 0, false},
	} {
		version, ok := Parse(tc.name)
		if ok != tc.ok || version != tc.version {
			t.Errorf("Parse(%q) = %d, %v; want %d, %v", tc.name, version, ok, tc.version, tc.ok)
		}
	}
}

func TestHighestIgnoresNonMigrationsAndMissingDirectories(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if highest, err := Highest(filepath.Join(dir, "absent")); err != nil || highest != 0 {
		t.Fatalf("Highest(absent) = %d, %v; want 0, nil", highest, err)
	}
	writeFile(t, dir, "20240102030405_first.sql")
	writeFile(t, dir, "20250101000000_second.sql")
	writeFile(t, dir, "notes.sql")
	if err := os.MkdirAll(filepath.Join(dir, "20260101000000_dir.sql"), 0o755); err != nil {
		t.Fatal(err)
	}
	highest, err := Highest(dir)
	if err != nil {
		t.Fatalf("Highest: %v", err)
	}
	if want := int64(20250101000000); highest != want {
		t.Fatalf("Highest = %d, want %d", highest, want)
	}
}

func TestAllocatorAdvancesWithinAPlan(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	fixed := time.Date(2024, time.January, 2, 3, 4, 5, 0, time.UTC)
	allocator := NewAllocator(dir, func() time.Time { return fixed })

	first, err := allocator.Next()
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	second, err := allocator.Next()
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if want := Version(fixed); first != want {
		t.Fatalf("first version = %d, want %d", first, want)
	}
	if second != first+1 {
		t.Fatalf("second version = %d, want %d", second, first+1)
	}
}

func TestAllocatorNeverSortsBeforeAnExistingMigration(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFile(t, dir, "20300101000000_future.sql")
	fixed := time.Date(2024, time.January, 2, 3, 4, 5, 0, time.UTC)

	version, err := NewAllocator(dir, func() time.Time { return fixed }).Next()
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if want := int64(20300101000001); version != want {
		t.Fatalf("version = %d, want %d (one past the highest existing)", version, want)
	}
}

func writeFile(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("-- +goose Up\n"), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}
