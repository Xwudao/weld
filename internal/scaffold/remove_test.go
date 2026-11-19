package scaffold

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Xwudao/weld/internal/project"
)

func TestRemoveModuleRollsBackOnReference(t *testing.T) {
	t.Parallel()
	dir := create(t, t.TempDir())
	addModule(t, dir, "widget")
	addModule(t, dir, "other")
	manifest := mustLoad(t, dir)
	writeRemovalFixture(t, dir, "internal/modules/other/widget.go", "package other\n\nimport _ \""+manifest.Module+"/internal/modules/widget\"\n", 0o644)
	asset := "internal/modules/widget/tool.sh"
	writeRemovalFixture(t, dir, asset, "#!/bin/sh\n", 0o755)
	before := map[string]string{}
	for _, file := range []string{asset, "internal/modules/widget/module.go", "internal/di/di.go", "internal/di/loom_gen.go", project.ManifestName} {
		before[file] = readFile(t, filepath.Join(dir, file))
	}
	plan, err := RemoveModule(Request{Dir: dir, Catalog: newCatalog()}, "widget", true)
	if err != nil {
		t.Fatal(err)
	}
	if err := plan.Apply(); err == nil || !strings.Contains(err.Error(), "widget") || !strings.Contains(err.Error(), "changes rolled back") {
		t.Fatalf("expected failed build and rollback, got %v", err)
	}
	for file, content := range before {
		if readFile(t, filepath.Join(dir, file)) != content {
			t.Errorf("rollback did not restore %s", file)
		}
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(filepath.Join(dir, asset))
		if err != nil || info.Mode().Perm() != 0o755 {
			t.Fatalf("rollback did not restore executable mode: %v, %v", info, err)
		}
	}
}

func TestRemoveModuleRejectsLinkAndStalePlan(t *testing.T) {
	t.Parallel()
	dir := create(t, t.TempDir())
	addModule(t, dir, "widget")
	req := Request{Dir: dir, Catalog: newCatalog()}
	plan, err := RemoveModule(req, "widget", true)
	if err != nil {
		t.Fatal(err)
	}
	lateFile := "internal/modules/widget/late.txt"
	writeRemovalFixture(t, dir, lateFile, "edited after planning", 0o644)
	if err := plan.Apply(); err == nil || !strings.Contains(err.Error(), "changed after planning") {
		t.Fatalf("applying stale removal: %v", err)
	}
	if !mustLoad(t, dir).HasModule("widget") || readFile(t, filepath.Join(dir, lateFile)) != "edited after planning" {
		t.Fatal("stale plan changed the project")
	}
	link := filepath.Join(dir, "internal/modules/widget/link")
	if err := os.Symlink(filepath.Join(dir, project.ManifestName), link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := RemoveModule(req, "widget", true); err == nil || !strings.Contains(err.Error(), "non-regular file") {
		t.Fatalf("removing module with a link: %v", err)
	}
}

func TestRemoveModuleKeepsOwnedFilesOutsideModule(t *testing.T) {
	t.Parallel()
	dir := create(t, t.TempDir())
	addModule(t, dir, "widget")
	migration := "db/migrations/001_create_widgets.sql"
	sql := "CREATE TABLE widgets (id bigint);\n"
	writeRemovalFixture(t, dir, migration, sql, 0o644)
	manifest := mustLoad(t, dir)
	manifest.SetFile(migration, "module:widget", project.RoleSeam, []byte(sql))
	encoded, err := manifest.Encode()
	if err != nil {
		t.Fatal(err)
	}
	writeRemovalFixture(t, dir, project.ManifestName, string(encoded), 0o644)
	plan, err := RemoveModule(Request{Dir: dir, Catalog: newCatalog()}, "widget", true)
	if err != nil {
		t.Fatal(err)
	}
	if err := plan.Apply(); err != nil {
		t.Fatal(err)
	}
	if !mustLoad(t, dir).Owns(migration) || readFile(t, filepath.Join(dir, migration)) != sql {
		t.Fatal("removal lost the migration or its manifest entry")
	}
}

func writeRemovalFixture(t *testing.T, dir, file, content string, mode os.FileMode) {
	t.Helper()
	absolute := filepath.Join(dir, file)
	if err := os.MkdirAll(filepath.Dir(absolute), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(absolute, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}
