package scaffold

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Xwudao/weld/internal/project"
)

// Planning is the dry-run boundary: a conflict must be detected without running
// Loom or changing either the live sources or their recorded baseline.
func TestDIPlanningProtectsGeneratedSources(t *testing.T) {
	for _, source := range []string{"di.go", "di_test.go"} {
		for _, baseline := range []string{"modified", "unknown", "legacy"} {
			if source == "di_test.go" && baseline == "legacy" {
				continue // A legacy manifest rejects the first changed source, di.go.
			}
			t.Run(source+"/"+baseline, func(t *testing.T) {
				dir := t.TempDir()
				manifest := project.NewManifest("demo", "example.com/demo", "test")
				present := map[string]bool{"loom": true, "config": true, "http": true}
				req := Request{Dir: dir, Catalog: newCatalog()}
				var operations []project.Operation
				upsert := func(op project.Operation) { operations = append(operations, op) }
				if err := reconcileDI(req, manifest, present, map[string][]byte{}, upsert, &Result{}); err != nil {
					t.Fatal(err)
				}
				if err := project.Apply(dir, operations); err != nil {
					t.Fatal(err)
				}
				path := "internal/di/" + source
				original := readFile(t, filepath.Join(dir, path))
				live := original
				switch baseline {
				case "modified":
					live += "\n// business wiring must survive\n"
					edited(t, dir, path, live)
				case "unknown":
					for i := range manifest.Files {
						if manifest.Files[i].Path == path {
							manifest.Files[i].Baseline = false
						}
					}
				case "legacy":
					manifest.LegacyBaseline = true
				}
				// cron changes both generated sources, even though business code
				// could still compile after losing its handwritten dependencies.
				present["cron"] = true
				err := reconcileDI(req, manifest, present, map[string][]byte{}, func(project.Operation) {}, &Result{})
				if err == nil || !strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), "stable provider seams") {
					t.Fatalf("want actionable source conflict for %s, got %v", path, err)
				}
				if got := readFile(t, filepath.Join(dir, path)); got != live {
					t.Fatal("planning overwrote live source")
				}
				// Reconciliation may have planned another, unmodified source,
				// but it must never absorb the conflicted source's hash.
				if baseline == "modified" {
					for _, entry := range manifest.Files {
						if entry.Path == path && entry.SHA256 != project.HashContent([]byte(original)) {
							t.Fatal("planning absorbed a project edit into the baseline")
						}
					}
				}
			})
		}
	}
}

func TestDIPlanningRestoresMissingSource(t *testing.T) {
	dir := t.TempDir()
	manifest := project.NewManifest("demo", "example.com/demo", "test")
	req := Request{Dir: dir, Catalog: newCatalog()}
	present := map[string]bool{"loom": true, "config": true, "http": true}
	plan := func() []project.Operation {
		t.Helper()
		var operations []project.Operation
		if err := reconcileDI(req, manifest, present, map[string][]byte{}, func(op project.Operation) {
			operations = append(operations, op)
		}, &Result{}); err != nil {
			t.Fatal(err)
		}
		return operations
	}
	if err := project.Apply(dir, plan()); err != nil {
		t.Fatal(err)
	}
	if got := plan(); len(got) != 0 {
		t.Fatalf("unchanged graph has %d operations", len(got))
	}
	if err := os.Remove(filepath.Join(dir, "internal/di/di.go")); err != nil {
		t.Fatal(err)
	}
	if got := plan(); len(got) != 1 || got[0].Path != "internal/di/di.go" {
		t.Fatalf("missing source plan = %+v", got)
	}
}
