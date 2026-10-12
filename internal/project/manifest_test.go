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
	manifest.SetFile(path, "loom", RolePayload, older)

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

// TestLoadMarksALegacyBaseline proves a manifest written by an older weld — the
// one that refreshed every hash after every add — is loaded with LegacyBaseline
// set and with no per-file baseline, so upgrade knows the hashes cannot rule out
// a project edit. A current manifest carries neither signal by accident.
func TestLoadMarksALegacyBaseline(t *testing.T) {
	dir := t.TempDir()
	legacy := `{
  "kind": "weld-project",
  "manifestVersion": 1,
  "name": "demo",
  "module": "example.com/demo",
  "base": {"name": "base", "version": "0.1.0"},
  "files": [
    {"path": "internal/httpserver/middleware.go", "capability": "http", "sha256": "abc"}
  ]
}
`
	if err := os.WriteFile(filepath.Join(dir, ManifestName), []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	manifest, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !manifest.LegacyBaseline {
		t.Fatal("a version 1 manifest was loaded as a trusted baseline")
	}
	if manifest.ManifestVersion != manifestVersion {
		t.Fatalf("manifest version = %d, want %d", manifest.ManifestVersion, manifestVersion)
	}
	entry, ok := manifest.FileEntry("internal/httpserver/middleware.go")
	if !ok {
		t.Fatal("the recorded file was dropped")
	}
	if entry.Baseline {
		t.Fatal("a version 1 file was loaded as a trusted baseline")
	}
}

// TestRefreshManifestOnlyTouchesNamedPaths is the regression guard for the
// laundering bug: refreshing every recorded hash after every add absorbed project
// edits into the baseline, and an upgrade then overwrote them. Only the paths
// weld's own tooling rewrote may be refreshed.
func TestRefreshManifestOnlyTouchesNamedPaths(t *testing.T) {
	dir := t.TempDir()
	writeFile := func(path, content string) {
		t.Helper()
		full := filepath.Join(dir, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	manifest := NewManifest("demo", "example.com/demo", "weld 0.1.0")
	manifest.SetFile("go.mod", "base", RolePatched, []byte("module example.com/demo\n"))
	manifest.SetFile("internal/httpserver/middleware.go", "http", RoleSeam, []byte("package httpserver\n// as weld wrote it\n"))
	encoded, err := manifest.Encode()
	if err != nil {
		t.Fatal(err)
	}
	writeFile(ManifestName, string(encoded))

	// The project edits the seam, and `go mod tidy` rewrites go.mod.
	tidied := "module example.com/demo\n\nrequire github.com/spf13/cobra v1.9.1\n"
	writeFile("go.mod", tidied)
	writeFile("internal/httpserver/middleware.go", "package httpserver\n\n// the project's own chain\n")

	if err := RefreshManifest(dir, "go.mod"); err != nil {
		t.Fatalf("RefreshManifest: %v", err)
	}
	reloaded, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	entry, _ := reloaded.FileEntry("go.mod")
	if entry.SHA256 != HashContent([]byte(tidied)) {
		t.Fatal("go.mod was not re-baselined")
	}
	if !entry.Baseline {
		t.Fatal("a refreshed file is not marked as a trusted baseline")
	}
	seam, _ := reloaded.FileEntry("internal/httpserver/middleware.go")
	if seam.SHA256 != HashContent([]byte("package httpserver\n// as weld wrote it\n")) {
		t.Fatal("RefreshManifest absorbed an edit to a file it was not asked to refresh")
	}
}

// TestSetPatchedFileKeepsThePayloadOwner proves a marker patch extends a shared
// file without taking it over: the recorded owner stays the capability that ships
// the payload, which is what keeps internal/config/config.go upgradable by config
// after redis patched it.
func TestSetPatchedFileKeepsThePayloadOwner(t *testing.T) {
	manifest := NewManifest("demo", "example.com/demo", "weld 0.1.0")
	manifest.SetFile("internal/config/config.go", "config", RolePatched, []byte("package config\n"))

	patched := []byte("package config\n\n// weld:redis:installed 0.1.0\ntype Redis struct{}\n")
	manifest.SetPatchedFile("internal/config/config.go", "redis", RolePatched, patched)

	entry, ok := manifest.FileEntry("internal/config/config.go")
	if !ok {
		t.Fatal("the patched file is not recorded")
	}
	if entry.Capability != "config" {
		t.Fatalf("owner = %q, want config", entry.Capability)
	}
	if entry.SHA256 != HashContent(patched) {
		t.Fatal("the patch did not update the recorded hash")
	}
	if len(manifest.Files) != 1 {
		t.Fatalf("files = %+v, want one entry", manifest.Files)
	}
}
