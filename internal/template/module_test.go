package template

import (
	"strings"
	"testing"
)

func TestLoadModulesIsWellFormed(t *testing.T) {
	module, err := LoadModules()
	if err != nil {
		t.Fatalf("LoadModules: %v", err)
	}
	if module.Version == "" {
		t.Fatalf("module template is missing a version: %+v", module)
	}
	if len(module.Files) == 0 {
		t.Fatal("module template declares no files")
	}
	for _, file := range module.Files {
		content, err := module.ReadFile(file)
		if err != nil {
			t.Errorf("ReadFile %s: %v", file.Source, err)
			continue
		}
		if len(content) == 0 {
			t.Errorf("payload for %s is empty", file.Source)
		}
		if !strings.Contains(file.Path, "__modname__") {
			t.Errorf("module file path %q does not carry __modname__", file.Path)
		}
	}
	if _, err := module.ReadRoute(); err != nil {
		t.Errorf("ReadRoute: %v", err)
	}
	snippet, err := module.ReadRouteSnippet()
	if err != nil {
		t.Fatalf("ReadRouteSnippet: %v", err)
	}
	if !strings.Contains(string(snippet), "weld:module:__modname__:installed") {
		t.Errorf("route snippet is missing the module sentinel:\n%s", snippet)
	}
}

func TestRenderSubstitutesModuleTokens(t *testing.T) {
	vars := Vars{Name: "demo", Module: "example.com/demo", Version: "0.1.0", Mod: "widget", ModTitle: "Widget"}
	got := string(Render([]byte("package __modname__\n// __ModName__ in __name__ (__module__ v__version__)\n"), vars))
	want := "package widget\n// Widget in demo (example.com/demo v0.1.0)\n"
	if got != want {
		t.Fatalf("Render = %q, want %q", got, want)
	}
	for _, token := range []string{"__modname__", "__ModName__", "__name__", "__module__", "__version__"} {
		if strings.Contains(got, token) {
			t.Errorf("token %s was not substituted: %q", token, got)
		}
	}
}

func TestExportName(t *testing.T) {
	cases := map[string]string{"widget": "Widget", "orders": "Orders", "a": "A", "": ""}
	for name, want := range cases {
		if got := ExportName(name); got != want {
			t.Errorf("ExportName(%q) = %q, want %q", name, got, want)
		}
	}
}
