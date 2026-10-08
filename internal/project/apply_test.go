package project

import (
	"os"
	"path/filepath"
	"testing"
)

const sample = `target: dep
# weld:web:begin
# weld:web:end
other: y
`

func TestPatchMarkerInsertsSnippet(t *testing.T) {
	snippet := []byte("# weld:web:installed 0.1.0\n\tnpm run build\n")
	got, already, err := PatchMarker([]byte(sample), "web", snippet)
	if err != nil {
		t.Fatalf("PatchMarker: %v", err)
	}
	if already {
		t.Fatal("already = true, want false")
	}
	want := "target: dep\n# weld:web:begin\n# weld:web:installed 0.1.0\n\tnpm run build\n# weld:web:end\nother: y\n"
	if string(got) != want {
		t.Fatalf("got:\n%q\nwant:\n%q", got, want)
	}
}

func TestPatchMarkerIsIdempotent(t *testing.T) {
	snippet := []byte("# weld:web:installed 0.1.0\n\tnpm run build\n")
	once, _, err := PatchMarker([]byte(sample), "web", snippet)
	if err != nil {
		t.Fatalf("PatchMarker: %v", err)
	}
	twice, already, err := PatchMarker(once, "web", snippet)
	if err != nil {
		t.Fatalf("PatchMarker: %v", err)
	}
	if !already {
		t.Fatal("already = false, want true")
	}
	if string(twice) != string(once) {
		t.Fatal("second patch changed the file")
	}
}

func TestPatchMarkerMissingPoint(t *testing.T) {
	_, _, err := PatchMarker([]byte("no markers here\n"), "web", []byte("x\n"))
	if err == nil {
		t.Fatal("expected error for missing extension point")
	}
}

func TestApplyRollsBackOnConflict(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "existing.txt"), []byte("user\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	operations := []Operation{
		{Path: "new/file.txt", Content: []byte("weld\n")},
		{Path: "existing.txt", Content: []byte("clobber\n")},
	}
	if err := Apply(root, operations); err == nil {
		t.Fatal("expected conflict error")
	}
	if _, err := os.Stat(filepath.Join(root, "new/file.txt")); !os.IsNotExist(err) {
		t.Fatal("created file was not rolled back")
	}
	got, err := os.ReadFile(filepath.Join(root, "existing.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "user\n" {
		t.Fatalf("existing file changed: %q", got)
	}
}

func TestManifestRoundTrip(t *testing.T) {
	root := t.TempDir()
	manifest := NewManifest("demo", "example.com/demo", "weld 0.1.0")
	manifest.Base = CapabilityRef{Name: "base", Version: "0.1.0"}
	manifest.SetFile("main.go", "base", []byte("package main\n"))
	manifest.AddCapability(CapabilityRef{Name: "web", Version: "0.1.0"})

	encoded, err := manifest.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ManifestName), encoded, 0o644); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.HasCapability("web") || !loaded.HasCapability("base") {
		t.Fatalf("capabilities not recorded: %+v", loaded)
	}
	if loaded.CapabilityVersion("web") != "0.1.0" {
		t.Fatalf("web version = %q", loaded.CapabilityVersion("web"))
	}
	if len(loaded.Drift(root)) != 1 {
		t.Fatalf("expected 1 drift (missing main.go), got %v", loaded.Drift(root))
	}
}

func TestLoadReportsNonProject(t *testing.T) {
	if _, err := Load(t.TempDir()); err == nil {
		t.Fatal("expected error for directory without manifest")
	}
}
