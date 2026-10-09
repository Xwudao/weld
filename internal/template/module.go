package template

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"strings"

	weldtemplate "github.com/Xwudao/weld/internal/weldtemplate"
)

// ModuleFile is one payload of the per-name business-module or command template.
type ModuleFile struct {
	// Path is the project-relative target; it may contain the __modname__ and
	// __ModName__ tokens, which the planner substitutes per module or command.
	Path   string `json:"path"`
	Source string `json:"source"`
	// When lists capabilities that must all be installed for the file to apply.
	// It lets a payload add a file only for a capability-aware project, such as
	// the command-specific Loom graph a Loom project gains.
	When []string `json:"when,omitempty"`
	// Shared marks a file several capabilities contribute identically, such as
	// the neutral openapi contract package both api and module depend on. The
	// first capability installed writes it and a later one leaves it untouched,
	// so either install order works.
	Shared bool `json:"shared,omitempty"`
}

// ModuleTemplate is the payload `weld add module <name>` writes.
//
// Unlike a capability it declares no fixed file set: the same template is
// rendered once per module name, so it carries the shared package files. Every
// module is served through the generated Loom graph, which registers it on the
// composed mux, so the template carries no separate route seam.
type ModuleTemplate struct {
	Version string       `json:"version"`
	Files   []ModuleFile `json:"files"`

	fsys fs.FS
}

// DIModule is one installed business module's dependency-graph contribution,
// rendered into a capability's DI graph.
type DIModule struct {
	// Name is the module package name and URL segment.
	Name string
	// Title is Name with its first letter upper-cased.
	Title string
}

// LoadModules returns the business-module payload embedded in the
// embedded weldtemplate package.
func LoadModules() (*ModuleTemplate, error) {
	root := weldtemplate.FS()
	raw, err := fs.ReadFile(root, "modules/module.json")
	if err != nil {
		return nil, fmt.Errorf("module template: read descriptor: %w", err)
	}
	module := &ModuleTemplate{}
	if err := json.Unmarshal(raw, module); err != nil {
		return nil, fmt.Errorf("module template: invalid descriptor: %w", err)
	}
	if module.Version == "" {
		return nil, fmt.Errorf("module template: missing version")
	}
	if len(module.Files) == 0 {
		return nil, fmt.Errorf("module template: no files")
	}
	sub, err := fs.Sub(root, "modules")
	if err != nil {
		return nil, fmt.Errorf("module template: %w", err)
	}
	module.fsys = sub
	for _, file := range module.Files {
		if file.Path == "" || file.Source == "" {
			return nil, fmt.Errorf("module template: file entry needs path and source")
		}
		if err := module.requireSource(file.Source); err != nil {
			return nil, err
		}
	}
	return module, nil
}

func (m *ModuleTemplate) requireSource(source string) error {
	if _, err := fs.Stat(m.fsys, source); err != nil {
		return fmt.Errorf("module template: payload %q: %w", source, err)
	}
	return nil
}

// ReadFile returns the raw payload for a module file.
func (m *ModuleTemplate) ReadFile(file ModuleFile) ([]byte, error) {
	return m.read(file.Source)
}

func (m *ModuleTemplate) read(source string) ([]byte, error) {
	raw, err := fs.ReadFile(m.fsys, source)
	if err != nil {
		return nil, fmt.Errorf("module template: read %s: %w", source, err)
	}
	return raw, nil
}

// ExportName upper-cases the first byte of a module name so it can prefix an
// exported identifier (installWidgetRoute).
func ExportName(name string) string {
	if name == "" {
		return ""
	}
	return strings.ToUpper(name[:1]) + name[1:]
}
