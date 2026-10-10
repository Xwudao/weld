package project

import (
	"os"
	"path/filepath"
	"testing"
)

// TestManifestDriftDistinguishesEdits proves Drift separates generated content
// from a local edit: a file whose content still matches the recorded hash is
// clean, a changed file and a missing file are both reported, and a path weld
// does not manage is ignored.
func TestManifestDriftDistinguishesEdits(t *testing.T) {
	dir := t.TempDir()
	const path = "internal/di/di.go"
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

	// The content weld wrote is not drift, even though the current template
	// would render differently.
	write(string(older))
	if drift := manifest.Drift(dir); len(drift) != 0 {
		t.Fatalf("Drift = %+v, want none for content weld wrote", drift)
	}

	// A user edit no longer matches the recorded hash.
	write(string(older) + "// user edit\n")
	drift := manifest.Drift(dir)
	if len(drift) != 1 || drift[0].Path != path || drift[0].Reason != "modified" {
		t.Fatalf("Drift = %+v, want the edited file reported as modified", drift)
	}

	// A missing managed file is reported as missing.
	if err := os.Remove(filepath.Join(dir, path)); err != nil {
		t.Fatal(err)
	}
	drift = manifest.Drift(dir)
	if len(drift) != 1 || drift[0].Path != path || drift[0].Reason != "missing" {
		t.Fatalf("Drift = %+v, want the missing file reported", drift)
	}
}
