package template

import (
	"strings"
	"testing"
)

func TestCatalogListsBaseAndWeb(t *testing.T) {
	catalog := Load()
	names, err := catalog.Names()
	if err != nil {
		t.Fatalf("Names: %v", err)
	}
	want := map[string]bool{"base": false, "http": false, "web": false, "api": false, "db": false}
	for _, name := range names {
		if _, ok := want[name]; ok {
			want[name] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("capability %q missing from %v", name, names)
		}
	}
}

func TestBaseCapabilityLoads(t *testing.T) {
	capability, err := Load().Get("base")
	if err != nil {
		t.Fatalf("Get base: %v", err)
	}
	if capability.Kind != KindBase {
		t.Errorf("kind = %q, want %q", capability.Kind, KindBase)
	}
	if len(capability.Files) == 0 {
		t.Fatal("base has no files")
	}
	for _, file := range capability.Files {
		content, err := capability.ReadFile(file)
		if err != nil {
			t.Errorf("read %s: %v", file.Path, err)
			continue
		}
		if len(content) == 0 {
			t.Errorf("payload for %s is empty", file.Path)
		}
	}
}

func TestWebCapabilityLoads(t *testing.T) {
	capability, err := Load().Get("web")
	if err != nil {
		t.Fatalf("Get web: %v", err)
	}
	if capability.Kind != KindAdd {
		t.Errorf("kind = %q, want %q", capability.Kind, KindAdd)
	}
	if len(capability.Requires) != 1 || capability.Requires[0] != "http" {
		t.Errorf("requires = %v, want [http]", capability.Requires)
	}
	if len(capability.Patches) != 3 {
		t.Errorf("patches = %d, want 3", len(capability.Patches))
	}
}

func TestHTTPCapabilityLoads(t *testing.T) {
	capability, err := Load().Get("http")
	if err != nil {
		t.Fatalf("Get http: %v", err)
	}
	if capability.Kind != KindAdd {
		t.Errorf("kind = %q, want %q", capability.Kind, KindAdd)
	}
	if len(capability.Requires) != 1 || capability.Requires[0] != "base" {
		t.Errorf("requires = %v, want [base]", capability.Requires)
	}
}

func TestAPICapabilityLoads(t *testing.T) {
	capability, err := Load().Get("api")
	if err != nil {
		t.Fatalf("Get api: %v", err)
	}
	if capability.Kind != KindAdd {
		t.Errorf("kind = %q, want %q", capability.Kind, KindAdd)
	}
	if len(capability.Requires) != 1 || capability.Requires[0] != "http" {
		t.Errorf("requires = %v, want [http]", capability.Requires)
	}
	if len(capability.Patches) != 2 {
		t.Errorf("patches = %d, want 2 (go.mod deps and httpserver routes)", len(capability.Patches))
	}
}

// TestDBCapabilityLoads pins the db capability's shape: it is additive, needs
// only base (not http), and patches exactly the go.mod dependency region and the
// Makefile db region.
func TestDBCapabilityLoads(t *testing.T) {
	capability, err := Load().Get("db")
	if err != nil {
		t.Fatalf("Get db: %v", err)
	}
	if capability.Kind != KindAdd {
		t.Errorf("kind = %q, want %q", capability.Kind, KindAdd)
	}
	if len(capability.Requires) != 1 || capability.Requires[0] != "base" {
		t.Errorf("requires = %v, want [base]", capability.Requires)
	}
	if len(capability.Patches) != 2 {
		t.Errorf("patches = %d, want 2 (go.mod deps and Makefile db)", len(capability.Patches))
	}
}

func TestUnknownCapabilityErrors(t *testing.T) {
	if _, err := Load().Get("nope"); err == nil {
		t.Fatal("expected error for unknown capability")
	}
}

func TestRenderSubstitutesTokens(t *testing.T) {
	got := string(Render([]byte("module __module__ name __name__ v__version__ {{keep}}"), Vars{
		Name:    "demo",
		Module:  "example.com/demo",
		Version: "9.9.9",
	}))
	want := "module example.com/demo name demo v9.9.9 {{keep}}"
	if got != want {
		t.Fatalf("Render = %q, want %q", got, want)
	}
}

func TestRenderLeavesBracesUntouched(t *testing.T) {
	in := []byte(`const x = { a: { b: 1 } }`)
	out := string(Render(in, Vars{Name: "demo"}))
	if strings.Contains(out, "\x00") {
		t.Fatal("render corrupted braces")
	}
	if out != string(in) {
		t.Fatalf("Render changed brace-heavy content: %q", out)
	}
}

// TestLoomCapabilityLoads pins the loom capability's shape: it requires http,
// declares a capability-aware DI graph, and replaces the go directive region.
func TestLoomCapabilityLoads(t *testing.T) {
	capability, err := Load().Get("loom")
	if err != nil {
		t.Fatalf("Get loom: %v", err)
	}
	if capability.Kind != KindAdd {
		t.Errorf("kind = %q, want %q", capability.Kind, KindAdd)
	}
	if len(capability.Requires) != 1 || capability.Requires[0] != "http" {
		t.Errorf("requires = %v, want [http]", capability.Requires)
	}
	if capability.DI == nil || capability.DI.Dir != "internal/di" || capability.DI.Source == "" {
		t.Fatalf("loom DI spec = %+v", capability.DI)
	}
	var goversion, serve bool
	for _, patch := range capability.Patches {
		switch {
		case patch.Path == "go.mod" && patch.Marker == "goversion":
			goversion = patch.Mode == "replace"
		case patch.Path == "internal/app/serve.go" && patch.Marker == "serve":
			serve = patch.Mode == "replace"
		}
	}
	if !goversion {
		t.Error("loom does not replace the go.mod goversion region")
	}
	if !serve {
		t.Error("loom does not replace the serve registration region")
	}
}

// TestRenderDIGraphIsCapabilityAware checks that the same template produces a
// different, gofmt-clean graph for different installed capability sets.
func TestRenderDIGraphIsCapabilityAware(t *testing.T) {
	capability, err := Load().Get("loom")
	if err != nil {
		t.Fatalf("Get loom: %v", err)
	}
	vars := DITemplateVars{
		Name:    "demo",
		Module:  "example.com/demo",
		Version: "0.1.0",
		Caps:    CapabilitySet{"base": true, "http": true, "loom": true},
	}
	httpOnly, err := capability.RenderDIGraph(vars)
	if err != nil {
		t.Fatalf("RenderDIGraph(http): %v", err)
	}
	if !strings.Contains(string(httpOnly), "NewServer") {
		t.Errorf("http-only graph has no server:\n%s", httpOnly)
	}
	if strings.Contains(string(httpOnly), "NewPool") || strings.Contains(string(httpOnly), "NewAPIService") {
		t.Errorf("http-only graph binds db/api:\n%s", httpOnly)
	}

	vars.Caps["db"] = true
	vars.Caps["api"] = true
	withDB, err := capability.RenderDIGraph(vars)
	if err != nil {
		t.Fatalf("RenderDIGraph(db,api): %v", err)
	}
	for _, want := range []string{"NewPool", "NewRepository", "NewAPIService", "data.Repository", "repositoryService"} {
		if !strings.Contains(string(withDB), want) {
			t.Errorf("db+api graph is missing %q:\n%s", want, withDB)
		}
	}
}
