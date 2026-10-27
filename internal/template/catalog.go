// Package template loads weld capability payloads bundled by the
// internal/weldtemplate package and substitutes their placeholders.
package template

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"

	"github.com/Xwudao/weld/internal/migration"
	"github.com/Xwudao/weld/internal/project"
	"github.com/Xwudao/weld/internal/semver"
	weldtemplate "github.com/Xwudao/weld/internal/weldtemplate"
)

// Kind classifies how a capability applies to a project.
type Kind string

const (
	// KindBase scaffolds a brand new project.
	KindBase Kind = "base"
	// KindAdd additively extends an existing project.
	KindAdd Kind = "add"
)

// Role classifies how `weld upgrade` may treat a payload file. It is the
// ownership vocabulary the manifest records, declared here per file; the empty
// role means project.RolePayload.
//
// The role exists because "weld wrote this file once" and "weld owns this file"
// are different claims, and only the capability author knows which one is true.
// Treating every recorded file as ownerless tool output is what let an earlier
// weld overwrite a project's edited middleware chain.
type Role = project.Role

const (
	// RolePayload is the default: a file weld owns outright. Upgrade rewrites it
	// when the project has not changed it since weld last wrote it, and hands
	// over a diff when it has.
	RolePayload = project.RolePayload
	// RolePatched is a file weld owns that also carries marker regions another
	// capability extends (internal/config/config.go). Upgrade renders the newer
	// payload skeleton and splices the live marker regions into it, so the
	// payload change and everything a capability or the project added inside a
	// region both survive. A path that is a payload file of one capability and a
	// patch target of another is upgraded as patched even without a declaration,
	// because that is the only safe reading.
	RolePatched = project.RolePatched
	// RoleSeam is a file weld writes once and the project then owns: the
	// middleware and policy seams, the *_providers.go wiring, the register.go
	// registration files and the project's Makefile. Upgrade never writes it.
	// When the shipped version changes, the project gets the diff and decides how
	// to fold it in, so an edit to a seam can never be lost.
	RoleSeam = project.RoleSeam
	// RoleGenerated is a file a code generator rewrites from the dependency graph
	// (internal/di/di.go and its test). It is re-rendered on every plan that
	// touches the graph, never merged and never retired.
	RoleGenerated = project.RoleGenerated
)

// File is a payload written into a project.
type File struct {
	Path   string `json:"path"`
	Source string `json:"source"`
	// Role is how upgrade treats the file: RolePayload (the default),
	// RolePatched or RoleSeam. RoleGenerated is implied by the di entry and must
	// not be declared.
	Role Role `json:"role,omitempty"`
	// When lists capabilities that must all be installed for the file to apply;
	// WhenAbsent lists capabilities that must all be absent. They let a payload
	// vary deterministically with the installed capability set.
	When       []string `json:"when,omitempty"`
	WhenAbsent []string `json:"whenAbsent,omitempty"`
	// Versioned marks a goose migration. Its project path carries a version
	// chosen when the capability is installed, so the file sorts after every
	// migration the project already has. Path names the file without a version
	// ("db/migrations/create_users.sql") and the payload is written as
	// "<version>_create_users.sql".
	Versioned bool `json:"versioned,omitempty"`
}

// ProjectPath returns the project path a file is written to. For a versioned
// file, version is the goose version chosen at install time; every other file
// ignores it and keeps its declared path.
func (f File) ProjectPath(version string) string {
	if !f.Versioned {
		return f.Path
	}
	return path.Join(path.Dir(f.Path), version+"_"+path.Base(f.Path))
}

// ShipsPath reports whether target is a project path this file produces.
//
// A versioned file matches a sibling whose name is its own with a goose version
// prefix, because the version was chosen when the file was installed and is not
// known in advance. That is how upgrade finds the migration it wrote instead of
// allocating a second version for the same schema change.
func (f File) ShipsPath(target string) bool {
	if !f.Versioned {
		return f.Path == target
	}
	if path.Dir(f.Path) != path.Dir(target) {
		return false
	}
	base := path.Base(target)
	if !strings.HasSuffix(base, "_"+path.Base(f.Path)) {
		return false
	}
	_, ok := migration.Parse(base)
	return ok
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
	// PostInstall carries human-readable next steps shown after a successful
	// install, for a capability whose payload needs a project generator to run
	// (for example sqlc) before the project builds.
	PostInstall []string `json:"postInstall,omitempty"`
	// DI declares a capability's Loom dependency-graph contribution. It is
	// rendered per installed capability set when the capability is installed, so
	// the composed application follows the installed capabilities without a
	// runtime registry.
	DI *DISpec `json:"di,omitempty"`
	// Changes is the capability's changelog. `weld upgrade` prints every entry
	// newer than the version a project recorded, so whoever folds a seam diff in
	// — a person or an agent — reads why the payload moved and which manual steps
	// it implies, not only what the bytes are. A diff cannot say "this key was
	// renamed" or "call this new constructor from your mounts"; an entry can.
	Changes []Change `json:"changes,omitempty"`

	fsys fs.FS
}

// Change is one version's changelog entry.
type Change struct {
	// Version is the capability version that shipped the change.
	Version string `json:"version"`
	// Summary says what changed, in one sentence.
	Summary string `json:"summary"`
	// Actions lists the steps an existing project takes by hand, because weld
	// never rewrites what the project owns (a seam signature, a renamed config
	// key, a new call a project file has to make).
	Actions []string `json:"actions,omitempty"`
}

// ChangesSince returns the changelog entries newer than from, oldest first. An
// empty or unparseable from returns every entry up to the current version, which
// is the conservative answer for a project that does not know its version.
func (c *Capability) ChangesSince(from string) []Change {
	var changes []Change
	for _, change := range c.Changes {
		if from != "" {
			if newer, err := semver.IsNewer(change.Version, from); err == nil && !newer {
				continue
			}
		}
		changes = append(changes, change)
	}
	sort.SliceStable(changes, func(i, j int) bool {
		order, err := semver.Compare(changes[i].Version, changes[j].Version)
		return err == nil && order < 0
	})
	return changes
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

// Catalog is the set of capabilities embedded in this weld module.
type Catalog struct {
	root fs.FS
}

// Load returns the catalog embedded in this weld module.
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
		switch file.Role {
		case "", RolePayload, RolePatched, RoleSeam:
		case RoleGenerated:
			return fmt.Errorf("capability %q: file %s: role %q is implied by the di entry and must not be declared", c.Name, file.Path, RoleGenerated)
		default:
			return fmt.Errorf("capability %q: file %s: unknown role %q", c.Name, file.Path, file.Role)
		}
		if file.Versioned {
			if c.Kind == KindBase {
				return fmt.Errorf("capability %q: file %s: a base capability cannot ship a versioned file", c.Name, file.Path)
			}
			if path.Dir(file.Path) == "." || !strings.HasSuffix(file.Path, ".sql") {
				return fmt.Errorf("capability %q: file %s: a versioned file must be a .sql file inside a directory", c.Name, file.Path)
			}
			if _, ok := migration.Parse(file.Path); ok {
				return fmt.Errorf("capability %q: file %s: a versioned file must not carry a version prefix", c.Name, file.Path)
			}
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
	for _, change := range c.Changes {
		if !semver.Valid(change.Version) || change.Summary == "" {
			return fmt.Errorf("capability %q: a changes entry needs a version and a summary", c.Name)
		}
		if newer, err := semver.IsNewer(change.Version, c.Version); err != nil || newer {
			return fmt.Errorf("capability %q: changes entry %s is newer than the capability version %s", c.Name, change.Version, c.Version)
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

// DIPaths lists the generated sources the capability's di entry renders.
func (c *Capability) DIPaths() []string {
	if c.DI == nil {
		return nil
	}
	paths := []string{path.Join(c.DI.Dir, "di.go")}
	if c.DI.Test != "" {
		paths = append(paths, path.Join(c.DI.Dir, "di_test.go"))
	}
	return paths
}

// PayloadPaths lists every path the capability declares: its file entries in
// payload order, then its generated DI sources. A versioned file contributes its
// declared path, which is the payload's identity; the project path it was written
// to depends on the version chosen at install time and is recorded in the
// manifest.
//
// It is the one enumeration install, upgrade and prune consult, so a file that
// ships through the di entry (internal/di/di.go) is never mistaken for one the
// capability stopped shipping — which is what made `weld upgrade --prune` try
// to delete the project's dependency graph.
func (c *Capability) PayloadPaths() []string {
	paths := make([]string, 0, len(c.Files)+2)
	for _, file := range c.Files {
		paths = append(paths, file.Path)
	}
	return append(paths, c.DIPaths()...)
}

// FileFor returns the file entry that ships target, matching a versioned file by
// its name and version prefix rather than by an exact path.
func (c *Capability) FileFor(target string) (File, bool) {
	for _, file := range c.Files {
		if file.ShipsPath(target) {
			return file, true
		}
	}
	return File{}, false
}

// RoleOf returns the upgrade role of a path the capability ships. A generated DI
// source is always RoleGenerated; otherwise the declared role applies, with
// RolePayload as the default.
func (c *Capability) RoleOf(target string) Role {
	for _, generated := range c.DIPaths() {
		if generated == target {
			return RoleGenerated
		}
	}
	file, ok := c.FileFor(target)
	if !ok || file.Role == "" {
		return RolePayload
	}
	return file.Role
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
