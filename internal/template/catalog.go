// Package template loads weld capability payloads bundled by the
// weld-template module and substitutes their placeholders.
package template

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"sort"

	weldtemplate "github.com/Xwudao/weld-template"
)

// Kind classifies how a capability applies to a project.
type Kind string

const (
	// KindBase scaffolds a brand new project.
	KindBase Kind = "base"
	// KindAdd additively extends an existing project.
	KindAdd Kind = "add"
)

// File is a payload written into a project.
type File struct {
	Path   string `json:"path"`
	Source string `json:"source"`
	// When lists capabilities that must all be installed for the file to apply;
	// WhenAbsent lists capabilities that must all be absent. They let a payload
	// vary deterministically with the installed capability set.
	When       []string `json:"when,omitempty"`
	WhenAbsent []string `json:"whenAbsent,omitempty"`
}

// Patch is a snippet inserted at a named extension point in an existing file.
type Patch struct {
	Path   string `json:"path"`
	Marker string `json:"marker"`
	Source string `json:"source"`
	// Mode selects how the snippet joins the region: "" or "append" appends it
	// (the default), "replace" rewrites the region's content.
	Mode string `json:"mode,omitempty"`
	// Bootstrap names a tracked file to restore Path from when Path is absent.
	// A git-ignored local file (config.yml) is legitimately missing on a fresh
	// clone while its committed example (config.example.yml) remains, so the
	// patch declares where to restore the target before patching it.
	Bootstrap  string   `json:"bootstrap,omitempty"`
	When       []string `json:"when,omitempty"`
	WhenAbsent []string `json:"whenAbsent,omitempty"`
}

// Capability is a declarative scaffold payload.
type Capability struct {
	Name     string   `json:"name"`
	Version  string   `json:"version"`
	Kind     Kind     `json:"kind"`
	Summary  string   `json:"summary"`
	Requires []string `json:"requires"`
	Files    []File   `json:"files"`
	Patches  []Patch  `json:"patches"`
	// DI declares a capability's Loom dependency-graph contribution. It is
	// rendered per installed capability set when the capability is installed, so
	// the composed application follows the installed capabilities without a
	// runtime registry.
	DI *DISpec `json:"di,omitempty"`

	fsys fs.FS
}

// DISpec is a capability's Loom dependency-graph contribution.
type DISpec struct {
	// Dir is the package directory the rendered sources are written to.
	Dir string `json:"dir"`
	// Source is the graph source template.
	Source string `json:"source"`
	// Test is an optional capability-aware test template.
	Test string `json:"test,omitempty"`
}

// Catalog is the set of capabilities bundled by the template module.
type Catalog struct {
	root fs.FS
}

// Load returns the catalog embedded in the weld-template module.
func Load() *Catalog {
	return &Catalog{root: weldtemplate.FS()}
}

// Names lists available capability names in lexical order.
func (c *Catalog) Names() ([]string, error) {
	entries, err := fs.ReadDir(c.root, "capabilities")
	if err != nil {
		return nil, fmt.Errorf("read capabilities: %w", err)
	}
	var names []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if _, err := fs.Stat(c.root, path.Join("capabilities", entry.Name(), "capability.json")); err != nil {
			continue
		}
		names = append(names, entry.Name())
	}
	sort.Strings(names)
	return names, nil
}

// Get loads a single capability by name.
func (c *Catalog) Get(name string) (*Capability, error) {
	dir := path.Join("capabilities", name)
	raw, err := fs.ReadFile(c.root, path.Join(dir, "capability.json"))
	if err != nil {
		return nil, fmt.Errorf("capability %q not found in template", name)
	}
	capability := &Capability{}
	if err := json.Unmarshal(raw, capability); err != nil {
		return nil, fmt.Errorf("capability %q: invalid descriptor: %w", name, err)
	}
	if capability.Name != name {
		return nil, fmt.Errorf("capability %q: descriptor declares name %q", name, capability.Name)
	}
	sub, err := fs.Sub(c.root, dir)
	if err != nil {
		return nil, fmt.Errorf("capability %q: %w", name, err)
	}
	capability.fsys = sub
	if err := capability.validate(); err != nil {
		return nil, err
	}
	return capability, nil
}

func (c *Capability) validate() error {
	if c.Version == "" {
		return fmt.Errorf("capability %q: missing version", c.Name)
	}
	switch c.Kind {
	case KindBase, KindAdd:
	default:
		return fmt.Errorf("capability %q: unknown kind %q", c.Name, c.Kind)
	}
	for _, file := range c.Files {
		if file.Path == "" || file.Source == "" {
			return fmt.Errorf("capability %q: file entry needs path and source", c.Name)
		}
		if err := c.requireSource(file.Source); err != nil {
			return err
		}
	}
	for _, patch := range c.Patches {
		if patch.Path == "" || patch.Marker == "" || patch.Source == "" {
			return fmt.Errorf("capability %q: patch entry needs path, marker and source", c.Name)
		}
		switch patch.Mode {
		case "", "append", "replace":
		default:
			return fmt.Errorf("capability %q: unknown patch mode %q", c.Name, patch.Mode)
		}
		if err := c.requireSource(patch.Source); err != nil {
			return err
		}
	}
	if c.DI != nil {
		if c.DI.Dir == "" || c.DI.Source == "" {
			return fmt.Errorf("capability %q: di entry needs dir and source", c.Name)
		}
		if err := c.requireSource(c.DI.Source); err != nil {
			return err
		}
		if c.DI.Test != "" {
			if err := c.requireSource(c.DI.Test); err != nil {
				return err
			}
		}
	}
	return nil
}

func (c *Capability) requireSource(source string) error {
	if _, err := fs.Stat(c.fsys, source); err != nil {
		return fmt.Errorf("capability %q: payload %q: %w", c.Name, source, err)
	}
	return nil
}

// ReadFile returns the raw payload for a capability file.
func (c *Capability) ReadFile(file File) ([]byte, error) {
	return c.read(file.Source)
}

// ReadPatch returns the raw snippet for a capability patch.
func (c *Capability) ReadPatch(patch Patch) ([]byte, error) {
	return c.read(patch.Source)
}

func (c *Capability) read(source string) ([]byte, error) {
	raw, err := fs.ReadFile(c.fsys, source)
	if err != nil {
		return nil, fmt.Errorf("capability %q: read %s: %w", c.Name, source, err)
	}
	return raw, nil
}
