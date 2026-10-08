package project

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sample = `target: dep
# weld:web:begin
# weld:web:end
other: y
`

// routeSample is a Go extension point: markers use the host file's comment
// syntax, and user code sits both before and after the region.
const routeSample = `package httpserver

func Handler() []Route {
	var routes []Route
	// weld:routes:begin
	// weld:routes:end
	return routes
}
`

var (
	webSnippet = []byte("\t// weld:web:installed 0.1.0\n\troutes = append(routes, installWebRoute)\n")
	apiSnippet = []byte("\t// weld:api:installed 0.1.0\n\troutes = append(routes, installAPIRoute)\n")
)

func TestPatchMarkerInsertsSnippet(t *testing.T) {
	snippet := []byte("# weld:web:installed 0.1.0\n\tnpm run build\n")
	got, already, err := PatchMarker([]byte(sample), "web", "web", snippet)
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
	once, _, err := PatchMarker([]byte(sample), "web", "web", snippet)
	if err != nil {
		t.Fatalf("PatchMarker: %v", err)
	}
	twice, already, err := PatchMarker(once, "web", "web", snippet)
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
	_, _, err := PatchMarker([]byte("no markers here\n"), "web", "web", []byte("x\n"))
	if err == nil {
		t.Fatal("expected error for missing extension point")
	}
}

func TestPatchMarkerAcceptsGoCommentSyntax(t *testing.T) {
	got, already, err := PatchMarker([]byte(routeSample), "routes", "web", webSnippet)
	if err != nil {
		t.Fatalf("PatchMarker: %v", err)
	}
	if already {
		t.Fatal("already = true, want false")
	}
	if !strings.Contains(string(got), "routes = append(routes, installWebRoute)") {
		t.Fatalf("install call missing:\n%s", got)
	}
}

// TestPatchMarkerAppendsDistinctCapabilities verifies that two capabilities can
// patch one region without one suppressing the other, in either order.
func TestPatchMarkerAppendsDistinctCapabilities(t *testing.T) {
	orders := []struct {
		name  string
		first struct {
			capability string
			snippet    []byte
		}
		second struct {
			capability string
			snippet    []byte
		}
	}{
		{"web then api", struct {
			capability string
			snippet    []byte
		}{"web", webSnippet}, struct {
			capability string
			snippet    []byte
		}{"api", apiSnippet}},
		{"api then web", struct {
			capability string
			snippet    []byte
		}{"api", apiSnippet}, struct {
			capability string
			snippet    []byte
		}{"web", webSnippet}},
	}
	for _, order := range orders {
		t.Run(order.name, func(t *testing.T) {
			first, already, err := PatchMarker([]byte(routeSample), "routes", order.first.capability, order.first.snippet)
			if err != nil {
				t.Fatalf("first patch: %v", err)
			}
			if already {
				t.Fatal("first patch reported already installed")
			}
			second, already, err := PatchMarker(first, "routes", order.second.capability, order.second.snippet)
			if err != nil {
				t.Fatalf("second patch: %v", err)
			}
			if already {
				t.Fatal("second patch reported already installed")
			}
			got := string(second)
			for _, want := range []string{
				"installWebRoute",
				"installAPIRoute",
				"weld:web:installed",
				"weld:api:installed",
			} {
				if !strings.Contains(got, want) {
					t.Errorf("%q missing from:\n%s", want, got)
				}
			}
			if strings.Count(got, "weld:routes:begin") != 1 || strings.Count(got, "weld:routes:end") != 1 {
				t.Errorf("marker pair duplicated:\n%s", got)
			}
		})
	}
}

func TestPatchMarkerMalformed(t *testing.T) {
	cases := map[string]string{
		"end before begin":  "// weld:routes:end\n// weld:routes:begin\n",
		"begin without end": "// weld:routes:begin\n",
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			if _, _, err := PatchMarker([]byte(content), "routes", "web", webSnippet); err == nil {
				t.Fatal("expected error for malformed extension point")
			}
		})
	}
}

func TestPatchMarkerPreservesUserTextOutsideRegion(t *testing.T) {
	withUserEdit := strings.Replace(routeSample, "func Handler() []Route {",
		"// user note: keep me\nfunc Handler() []Route {", 1)
	got, _, err := PatchMarker([]byte(withUserEdit), "routes", "web", webSnippet)
	if err != nil {
		t.Fatalf("PatchMarker: %v", err)
	}
	if !strings.Contains(string(got), "// user note: keep me") {
		t.Fatalf("user text was lost:\n%s", got)
	}
	if !strings.Contains(string(got), "\treturn routes\n}") {
		t.Fatalf("code after the region changed:\n%s", got)
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
