package template

import (
	"path"
	"strings"
	"testing"
)

func TestLoadCommandsIsWellFormed(t *testing.T) {
	command, err := LoadCommands()
	if err != nil {
		t.Fatalf("LoadCommands: %v", err)
	}
	if command.Version == "" {
		t.Fatal("command template has no version")
	}
	for _, variant := range []struct {
		name  string
		files []ModuleFile
	}{{"generic", command.Generic.Files}, {"module", command.Module.Files}} {
		if len(variant.files) == 0 {
			t.Fatalf("%s variant declares no files", variant.name)
		}
		for _, file := range variant.files {
			if !strings.Contains(file.Path, "__modname__") {
				t.Errorf("%s path %q does not carry the __modname__ token", variant.name, file.Path)
			}
			if _, err := command.ReadFile(file); err != nil {
				t.Errorf("%s payload %q: %v", variant.name, file.Source, err)
			}
		}
	}
}

// TestCommandVariantsShareTargets proves the generic and module variants write
// the same project paths for a name, so a name is one command group whichever
// command created it.
func TestCommandVariantsShareTargets(t *testing.T) {
	command, err := LoadCommands()
	if err != nil {
		t.Fatalf("LoadCommands: %v", err)
	}
	generic := command.fileTargets(command.Generic.Files)
	module := command.fileTargets(command.Module.Files)
	if len(generic) != len(module) {
		t.Fatalf("generic writes %d files, module writes %d", len(generic), len(module))
	}
	for target := range generic {
		if _, ok := module[target]; !ok {
			t.Errorf("module variant does not write %q", target)
		}
	}
}

func (m *CommandTemplate) fileTargets(files []ModuleFile) map[string]bool {
	targets := map[string]bool{}
	for _, file := range files {
		targets[path.Clean(file.Path)] = true
	}
	return targets
}
