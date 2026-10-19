package scaffold

import (
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Xwudao/weld/internal/migration"
)

// fixedClock is the instant a migration test pins the version allocator to, so
// the planned filename is predictable without hard-coding a numbering scheme.
func fixedClock() time.Time {
	return time.Date(2024, time.March, 4, 5, 6, 7, 0, time.UTC)
}

func fixedNow() func() time.Time {
	return func() time.Time { return fixedClock() }
}

// TestAddDBVersionsItsMigration proves the shipped items migration gets a goose
// version chosen at install time, and that the manifest records the payload
// source so a later upgrade can find the file again.
func TestAddDBVersionsItsMigration(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	dir := create(t, root)

	result, err := Add(Request{Dir: dir, Catalog: newCatalog(), WithDependencies: true, Now: fixedNow()}, "db")
	if err != nil {
		t.Fatalf("Add db: %v", err)
	}
	if err := result.Apply(); err != nil {
		t.Fatalf("Apply add db: %v", err)
	}

	migrated := migrationFile(t, dir, "create_items")
	want := migration.Name(migration.Version(fixedClock()), "create_items")
	if got := filepath.Base(migrated); got != want {
		t.Fatalf("migration = %q, want %q", got, want)
	}
	if got := readFile(t, migrated); !strings.Contains(got, "-- +goose Up") || !strings.Contains(got, "-- +goose Down") {
		t.Fatalf("migration skeleton is incomplete:\n%s", got)
	}

	entry, ok := mustLoad(t, dir).FileEntry(path.Join("db", "migrations", want))
	if !ok {
		t.Fatalf("%s is not recorded in the manifest", want)
	}
	if entry.Source != "files/migrations/create_items.sql" {
		t.Fatalf("recorded source = %q, want the payload source", entry.Source)
	}
}

// TestInstalledMigrationsNeverSortBeforeTheProjectsOwn is the property the fixed
// numbering broke: a capability installed after the project wrote its own
// migrations must still land last, or goose refuses it without --allow-missing.
func TestInstalledMigrationsNeverSortBeforeTheProjectsOwn(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	dir := create(t, root)

	writeProjectFile(t, dir, "db/migrations/20300101000000_add_orders.sql", "-- +goose Up\n")

	result, err := Add(Request{Dir: dir, Catalog: newCatalog(), WithDependencies: true, Now: fixedNow()}, "db")
	if err != nil {
		t.Fatalf("Add db: %v", err)
	}
	if err := result.Apply(); err != nil {
		t.Fatalf("Apply add db: %v", err)
	}

	migrated := migrationFile(t, dir, "create_items")
	version, ok := migration.Parse(filepath.Base(migrated))
	if !ok {
		t.Fatalf("cannot parse a version from %s", migrated)
	}
	if want := int64(20300101000001); version != want {
		t.Fatalf("installed migration version = %d, want %d (one past the project's own)", version, want)
	}
}

// TestAddDBRejectsAProjectMigrationWithTheSameName proves weld refuses to add a
// second migration for a schema change the project already wrote under the
// payload's name, instead of running the same CREATE twice.
func TestAddDBRejectsAProjectMigrationWithTheSameName(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	dir := create(t, root)

	writeProjectFile(t, dir, "db/migrations/000002_create_items.sql", "-- mine\n")

	request := Request{Dir: dir, Catalog: newCatalog(), WithDependencies: true, Now: fixedNow()}
	_, err := Add(request, "db")
	if err == nil {
		t.Fatal("expected a conflict for the project's own create_items migration")
	}
	for _, want := range []string{"create_items.sql", "already exists"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
	if got := readFile(t, filepath.Join(dir, "db", "migrations", "000002_create_items.sql")); got != "-- mine\n" {
		t.Fatal("the project's migration was modified")
	}
	if _, statErr := os.Stat(filepath.Join(dir, "internal", "data")); !os.IsNotExist(statErr) {
		t.Fatal("partial files were written on failure")
	}
}

// TestMigrationsPlannedTogetherGetDistinctVersions proves two capabilities
// installed in one plan never collide on a version, even when the clock has not
// ticked between them.
func TestMigrationsPlannedTogetherGetDistinctVersions(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	dir := create(t, root)

	result, err := Add(Request{Dir: dir, Catalog: newCatalog(), WithDependencies: true, Now: fixedNow()}, "db", "user")
	if err != nil {
		t.Fatalf("Add db user: %v", err)
	}
	if err := result.Apply(); err != nil {
		t.Fatalf("Apply add: %v", err)
	}

	items := versionOf(t, migrationFile(t, dir, "create_items"))
	users := versionOf(t, migrationFile(t, dir, "create_users"))
	if items == users {
		t.Fatalf("both migrations got version %d", items)
	}
	if users <= items {
		t.Fatalf("user migration version %d is not after the db migration version %d", users, items)
	}
}

// TestUpgradeKeepsAnInstalledMigrationVersion proves an upgrade rewrites the
// migration weld installed and never renames it: a migration that may already be
// applied must keep its version.
func TestUpgradeKeepsAnInstalledMigrationVersion(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	dir := create(t, root)
	add(t, dir, "db")
	before := migrationFile(t, dir, "create_items")

	downgrade(t, dir, "db", "0.0.1")
	result := upgrade(t, dir, nil, false)
	if err := result.Apply(); err != nil {
		t.Fatalf("apply upgrade: %v", err)
	}

	if after := migrationFile(t, dir, "create_items"); after != before {
		t.Fatalf("upgrade renamed the migration: %s -> %s", before, after)
	}
}

// TestUpgradeFindsAMigrationInstalledUnderItsOldFixedNumber covers a project
// scaffolded before capability migrations were versioned: the manifest has no
// recorded source and the file carries the number the payload used to declare.
// The upgrade must rewrite that file, not allocate a second migration for the
// same schema change.
func TestUpgradeFindsAMigrationInstalledUnderItsOldFixedNumber(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	dir := create(t, root)
	add(t, dir, "db")

	installed := migrationFile(t, dir, "create_items")
	legacy := path.Join("db", "migrations", "000001_create_items.sql")
	if err := os.Rename(installed, filepath.Join(dir, legacy)); err != nil {
		t.Fatalf("rename migration: %v", err)
	}
	forget(t, dir, path.Join("db", "migrations", filepath.Base(installed)))
	// An older weld wrote the payload's content and recorded the hash, but not the
	// source; welding a different hash makes the upgrade plan a rewrite.
	weldWrote(t, dir, "db", legacy, "-- +goose Up\n-- an older payload\n\n-- +goose Down\n")
	downgrade(t, dir, "db", "0.2.1")

	result := upgrade(t, dir, nil, false)
	if err := result.Apply(); err != nil {
		t.Fatalf("apply upgrade: %v", err)
	}

	matches, err := filepath.Glob(filepath.Join(dir, "db", "migrations", "*_create_items.sql"))
	if err != nil {
		t.Fatalf("glob migrations: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("migrations = %v, want exactly one (the upgrade must not add a second)", matches)
	}
	if got := readFile(t, matches[0]); got == "-- +goose Up\n-- an older payload\n\n-- +goose Down\n" {
		t.Fatal("the legacy migration was not rewritten in place")
	}
	entry, ok := mustLoad(t, dir).FileEntry(legacy)
	if !ok {
		t.Fatalf("%s is no longer recorded", legacy)
	}
	if entry.Source != "files/migrations/create_items.sql" {
		t.Fatalf("recorded source = %q, want the payload source", entry.Source)
	}
}

// TestPruneKeepsTheInstalledMigration proves the retired-file pass recognises the
// versioned migration as still shipped, so `weld upgrade --prune` cannot delete
// the project's schema.
func TestPruneKeepsTheInstalledMigration(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	dir := create(t, root)
	add(t, dir, "db")
	migrated := migrationFile(t, dir, "create_items")

	result := upgrade(t, dir, []string{"db"}, true)
	if err := result.Apply(); err != nil {
		t.Fatalf("apply upgrade: %v", err)
	}
	if _, err := os.Stat(migrated); err != nil {
		t.Fatalf("prune deleted %s: %v", migrated, err)
	}
	if _, ok := mustLoad(t, dir).FileEntry(path.Join("db", "migrations", filepath.Base(migrated))); !ok {
		t.Fatal("prune dropped the migration's manifest record")
	}
}

func versionOf(t *testing.T, file string) int64 {
	t.Helper()
	version, ok := migration.Parse(filepath.Base(file))
	if !ok {
		t.Fatalf("cannot parse a version from %s", file)
	}
	return version
}
