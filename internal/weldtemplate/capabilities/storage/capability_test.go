// Package storage holds the descriptor contract test for the storage
// capability. The payloads under files/ are Go templates for a generated
// project, so their tests are the templates themselves; this test guards the
// capability descriptor and its relationship to the config markers it patches.
package storage

import (
	"encoding/json"
	"io/fs"
	"path"
	"strings"
	"testing"

	weldtemplate "github.com/Xwudao/weld/internal/weldtemplate"
)

type descriptor struct {
	Name     string   `json:"name"`
	Version  string   `json:"version"`
	Kind     string   `json:"kind"`
	Summary  string   `json:"summary"`
	Requires []string `json:"requires"`
	Files    []struct {
		Path   string   `json:"path"`
		Source string   `json:"source"`
		When   []string `json:"when"`
	} `json:"files"`
	Patches []struct {
		Path      string   `json:"path"`
		Marker    string   `json:"marker"`
		Source    string   `json:"source"`
		Mode      string   `json:"mode"`
		Bootstrap string   `json:"bootstrap"`
		When      []string `json:"when"`
	} `json:"patches"`
}

func read(t *testing.T) descriptor {
	t.Helper()
	raw, err := fs.ReadFile(weldtemplate.FS(), "capabilities/storage/capability.json")
	if err != nil {
		t.Fatalf("read descriptor: %v", err)
	}
	var d descriptor
	if err := json.Unmarshal(raw, &d); err != nil {
		t.Fatalf("invalid descriptor: %v", err)
	}
	return d
}

// TestStorageCapabilityRequiresLoomWithoutHTTP guards the storage contract: it
// adopts Loom (which brings config) but stays independent of the HTTP surface,
// ships no HTTP route and no bucket creation, and reaches the configuration only
// through the shared marker regions.
func TestStorageCapabilityRequiresLoomWithoutHTTP(t *testing.T) {
	d := read(t)
	if d.Kind != "add" {
		t.Fatalf("kind = %q, want add", d.Kind)
	}
	if strings.Join(d.Requires, ",") != "loom" {
		t.Fatalf("requires = %v, want [loom]", d.Requires)
	}
	for _, forbidden := range []string{"http", "web", "api", "db"} {
		for _, required := range d.Requires {
			if required == forbidden {
				t.Errorf("storage requires %q; the client must stay off the HTTP surface", forbidden)
			}
		}
	}

	paths := map[string]string{}
	for _, file := range d.Files {
		paths[file.Path] = file.Source
	}
	for _, want := range []string{
		"internal/config/storage.go",
		"internal/config/storage_test.go",
		"internal/objectstore/objectstore.go",
		"internal/objectstore/objectstore_test.go",
		"internal/objectstore/README.md",
		"internal/di/storage_provider.go",
	} {
		if _, ok := paths[want]; !ok {
			t.Errorf("storage does not ship %s", want)
		}
	}
	if got := d.Files[len(d.Files)-1]; got.Path != "internal/di/storage_provider.go" ||
		len(got.When) != 1 || got.When[0] != "loom" {
		t.Errorf("the Loom provider seam is not guarded by when: [loom]: %+v", got)
	}
	// The client is explicit: no route file is shipped and no payload mentions
	// creating a bucket.
	for _, file := range d.Files {
		if strings.Contains(file.Path, "route") || strings.Contains(file.Path, "handler") {
			t.Errorf("storage ships an HTTP payload %q", file.Path)
		}
		raw, err := fs.ReadFile(weldtemplate.FS(), path.Join("capabilities/storage", file.Source))
		if err != nil {
			t.Fatalf("read payload %q: %v", file.Source, err)
		}
		if strings.Contains(string(raw), "CreateBucket") {
			t.Errorf("payload %q creates a bucket; the client must not", file.Source)
		}
	}
}

// TestStoragePatchesTheConfigMarkers proves the typed configuration is added
// through the config capability's extension points, that the local and example
// YAML both receive the section, and that the go directive is raised (never
// appended) for the AWS SDK's Go floor.
func TestStoragePatchesTheConfigMarkers(t *testing.T) {
	d := read(t)
	markers := map[string]string{}
	for _, patch := range d.Patches {
		markers[patch.Path+"|"+patch.Marker] = patch.Source
		switch {
		case patch.Path == "config.yml" && patch.Marker == "config":
			if patch.Bootstrap != "config.example.yml" {
				t.Errorf("config.yml bootstrap = %q, want config.example.yml", patch.Bootstrap)
			}
		case patch.Path == "go.mod" && patch.Marker == "goversion":
			if patch.Mode != "replace" {
				t.Errorf("goversion mode = %q, want replace", patch.Mode)
			}
		}
	}
	for _, want := range []string{
		"go.mod|deps",
		"go.mod|goversion",
		"config.yml|config",
		"config.example.yml|config",
		"internal/config/config.go|configfields",
		"internal/config/config.go|configenv",
		"internal/config/config.go|configdefaults",
	} {
		if _, ok := markers[want]; !ok {
			t.Errorf("storage does not patch %s", want)
		}
	}

	goversion, err := fs.ReadFile(weldtemplate.FS(), "capabilities/storage/files/goversion.snippet")
	if err != nil {
		t.Fatalf("read goversion snippet: %v", err)
	}
	// The floor is the maximum of the AWS SDK's 1.24 and Loom's 1.25, so a
	// replace-mode patch can never lower it whichever capability is last.
	if !strings.Contains(string(goversion), "go 1.25.0") {
		t.Errorf("goversion snippet does not raise the floor to 1.25.0:\n%s", goversion)
	}
}
