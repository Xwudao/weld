package scaffold

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/Xwudao/weld/internal/project"
)

// createWith plans `weld new demo --with names...` under root.
func createWith(t *testing.T, root string, names ...string) (*Result, error) {
	t.Helper()
	return CreateWith(Request{Dir: root, Name: testName, Module: testModule, Version: "weld 0.1.0", Catalog: newCatalog()}, names...)
}

// appliedAt and migrationVersion are the two deliberate byte-instabilities of a
// generated project; a comparison of two projects masks them.
var (
	appliedAt        = regexp.MustCompile(`"appliedAt": "[^"]*"`)
	migrationVersion = regexp.MustCompile(`[0-9]{14}_`)
)

// TestCreateWithMatchesNewThenAdd proves `weld new --with` is exactly `weld new`
// followed by `weld add`: the same file set, the same bytes and the same
// manifest, and the composed project builds and tests.
func TestCreateWithMatchesNewThenAdd(t *testing.T) {
	t.Parallel()
	composedRoot := t.TempDir()
	result, err := createWith(t, composedRoot, "http", "db")
	if err != nil {
		t.Fatalf("CreateWith: %v", err)
	}
	if got, want := strings.Join(result.Installed, ","), "config,loom,http,db"; got != want {
		t.Fatalf("installed = %q, want %q", got, want)
	}
	for _, operation := range result.Operations {
		if operation.Overwrite || operation.Delete {
			t.Errorf("%s is planned as an overwrite or delete in a new directory", operation.Path)
		}
	}
	if err := result.Apply(); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	composed := filepath.Join(composedRoot, testName)

	sequential := create(t, t.TempDir())
	add(t, sequential, "http", "db")

	want := snapshotTree(t, sequential)
	got := snapshotTree(t, composed)
	for path, content := range want {
		if got[path] != content {
			t.Errorf("%s differs from new + add", path)
		}
	}
	for path := range got {
		if _, ok := want[path]; !ok {
			t.Errorf("new --with wrote %s, which new + add does not", path)
		}
	}
	if drift := mustLoad(t, composed).Drift(composed); len(drift) != 0 {
		t.Fatalf("drift after new --with: %+v", drift)
	}
	if !buildAndTestGeneratedProject(t, composed) {
		t.Skip("cannot resolve the generated project's dependencies (network/module cache unavailable)")
	}
}

// snapshotTree reads every regular file under dir, keyed by its slash path with
// the install-time timestamps masked.
func snapshotTree(t *testing.T, dir string) map[string]string {
	t.Helper()
	files := map[string]string{}
	err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		key := migrationVersion.ReplaceAllString(filepath.ToSlash(rel), "V_")
		files[key] = migrationVersion.ReplaceAllString(appliedAt.ReplaceAllString(string(raw), ""), "V_")
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", dir, err)
	}
	return files
}

// TestCreateWithPlanWritesNothing proves the plan is built without touching the
// target: --dry-run must be truthful, so the directory stays absent until Apply.
func TestCreateWithPlanWritesNothing(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	result, err := createWith(t, root, "redis")
	if err != nil {
		t.Fatalf("CreateWith: %v", err)
	}
	if len(result.Operations) == 0 || !result.NewDir {
		t.Fatalf("result = %+v, want a new-directory plan", result)
	}
	if _, err := os.Stat(filepath.Join(root, testName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("planning created the project directory: %v", err)
	}
}

// TestCreateWithRejectsBadInput proves an unknown or non-additive capability and
// an existing directory fail before anything is written.
func TestCreateWithRejectsBadInput(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	for _, names := range [][]string{{"nope"}, {"http", "base"}} {
		if _, err := createWith(t, root, names...); err == nil {
			t.Errorf("CreateWith %v succeeded, want an error", names)
		}
	}
	if entries, _ := os.ReadDir(root); len(entries) != 0 {
		t.Fatalf("a rejected plan wrote %d entries", len(entries))
	}

	if err := os.Mkdir(filepath.Join(root, testName), 0o755); err != nil {
		t.Fatal(err)
	}
	var conflict *project.ConflictError
	if _, err := createWith(t, root, "http"); !errors.As(err, &conflict) {
		t.Fatalf("CreateWith over an existing directory = %v, want a conflict", err)
	}
}

// TestNewDirApplyRefusesAndCleansUp proves a new-directory plan never writes
// into a directory that appeared after planning (and never removes it), and that
// a failed apply removes the directory it created rather than leaving the empty
// tree a file rollback would.
func TestNewDirApplyRefusesAndCleansUp(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	result, err := createWith(t, root)
	if err != nil {
		t.Fatalf("CreateWith: %v", err)
	}
	target := filepath.Join(root, testName)
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(target, "keep.txt")
	if err := os.WriteFile(sentinel, []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	var conflict *project.ConflictError
	if err := result.Apply(); !errors.As(err, &conflict) {
		t.Fatalf("Apply over a directory that appeared = %v, want a conflict", err)
	}
	if _, err := os.Stat(sentinel); err != nil {
		t.Fatalf("Apply removed a directory it did not create: %v", err)
	}

	failing, err := createWith(t, t.TempDir())
	if err != nil {
		t.Fatalf("CreateWith: %v", err)
	}
	// A path that escapes into a file cannot be written, so Apply fails after it
	// has created the directory and some of its files.
	failing.Operations = append(failing.Operations, project.Operation{Path: "main.go/broken", Content: []byte("x")})
	if err := failing.Apply(); err == nil {
		t.Fatal("Apply of a broken plan succeeded")
	}
	if _, err := os.Stat(failing.Dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a failed new-directory apply left %s behind: %v", failing.Dir, err)
	}
}
