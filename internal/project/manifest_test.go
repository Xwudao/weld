package project

import (
	"os"
	"path/filepath"
	"testing"
)

// TestManifestModifiedDistinguishesEdits proves Modified reports a user edit but
// not generated content: a file weld wrote from an older template still matches
// its recorded hash and must keep being regenerated, while a locally changed
// file, an already-preserved file and a missing or unmanaged path are handled
// distinctly.
func TestManifestModifiedDistinguishesEdits(t *testing.T) {
	dir := t.TempDir()
	const path = "internal/di/di_test.go"
	if err := os.MkdirAll(filepath.Join(dir, "internal", "di"), 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, path), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	manifest := NewManifest("demo", "example.com/demo", "weld 0.1.0")
	older := []byte("package di\n")
	manifest.SetFile(path, "loom", older)

	// The content weld wrote from an older template is not an edit, even though
	// the current template would render differently.
	write(string(older))
	if modified, err := manifest.Modified(dir, path); err != nil || modified {
		t.Fatalf("Modified = %v, %v; want false, nil for content weld wrote", modified, err)
	}

	// A missing managed file is regenerated, not preserved.
	if err := os.Remove(filepath.Join(dir, path)); err != nil {
		t.Fatal(err)
	}
	if modified, err := manifest.Modified(dir, path); err != nil || modified {
		t.Fatalf("Modified = %v, %v; want false, nil for a missing file", modified, err)
	}

	// A user edit no longer matches the recorded hash.
	write(string(older) + "// user assertion\n")
	if modified, err := manifest.Modified(dir, path); err != nil || !modified {
		t.Fatalf("Modified = %v, %v; want true, nil after a user edit", modified, err)
	}

	// Preserve records that the edit was accepted, so the file stays modified
	// even once it matches the recorded hash again.
	manifest.PreserveFile(path)
	write(string(older))
	if modified, err := manifest.Modified(dir, path); err != nil || !modified {
		t.Fatalf("Modified = %v, %v; want true, nil for a preserved file", modified, err)
	}

	if modified, err := manifest.Modified(dir, "internal/other.go"); err != nil || modified {
		t.Fatalf("Modified = %v, %v; want false, nil for an unmanaged path", modified, err)
	}
}
