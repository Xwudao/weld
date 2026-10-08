// Package scaffold builds the plans behind `weld new` and `weld add`.
//
// Planning and applying are separate: a plan is fully built and conflict-checked
// against the project before anything is written, which is what makes dry-run
// and safe failure possible.
package scaffold

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Xwudao/weld/internal/project"
	"github.com/Xwudao/weld/internal/template"
)

// Request describes a scaffold action.
type Request struct {
	Dir     string
	Name    string
	Module  string
	Version string
	Catalog *template.Catalog
}

// Result is a planned scaffold action.
type Result struct {
	// Dir is the project directory operations are relative to.
	Dir        string
	Operations []project.Operation
	Capability *template.Capability
	// Installed lists the capabilities this plan applies, dependencies first.
	Installed []string
	// Notes carries human-readable observations such as "already installed".
	Notes []string
}

// Apply writes the planned result.
func (r *Result) Apply() error {
	if len(r.Operations) == 0 {
		return nil
	}
	return project.Apply(r.Dir, r.Operations)
}

// Create plans a new project from the base capability.
func Create(req Request) (*Result, error) {
	if strings.TrimSpace(req.Name) == "" {
		return nil, errors.New("project name is required")
	}
	target := filepath.Join(req.Dir, req.Name)
	if _, err := os.Stat(target); err == nil {
		return nil, &project.ConflictError{Path: target, Reason: "already exists"}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}

	base, err := req.Catalog.Get("base")
	if err != nil {
		return nil, err
	}
	if base.Kind != template.KindBase {
		return nil, fmt.Errorf("capability %q is not a base scaffold", base.Name)
	}

	module := req.Module
	if module == "" {
		module = "example.com/" + req.Name
	}
	vars := template.Vars{Name: req.Name, Module: module, Version: base.Version}
	manifest := project.NewManifest(req.Name, module, req.Version)
	manifest.Base = project.CapabilityRef{Name: base.Name, Version: base.Version}

	var operations []project.Operation
	for _, file := range base.Files {
		content, err := base.ReadFile(file)
		if err != nil {
			return nil, err
		}
		rendered := template.Render(content, vars)
		operations = append(operations, project.Operation{Path: file.Path, Content: rendered})
		manifest.SetFile(file.Path, base.Name, rendered)
	}

	encoded, err := manifest.Encode()
	if err != nil {
		return nil, err
	}
	operations = append(operations, project.Operation{Path: project.ManifestName, Content: encoded})

	return &Result{Dir: target, Operations: operations, Capability: base}, nil
}

// Add plans installing an additive capability into an existing project.
//
// Missing requirements are resolved and installed first, so `weld add web`
// installs the http capability it needs without the user adding it explicitly,
// and capabilities can be installed in either order.
func Add(req Request, name string) (*Result, error) {
	manifest, err := project.Load(req.Dir)
	if err != nil {
		return nil, err
	}
	requested, err := req.Catalog.Get(name)
	if err != nil {
		return nil, err
	}
	if requested.Kind != template.KindAdd {
		return nil, fmt.Errorf("capability %q is not an additive capability", name)
	}

	order, err := installOrder(req.Catalog, manifest, requested)
	if err != nil {
		return nil, err
	}
	result := &Result{Dir: req.Dir, Capability: requested}
	if len(order) == 0 {
		result.Notes = alreadyInstalledNotes(req.Dir, manifest, requested)
		return result, nil
	}
	for _, capability := range order {
		if capability.Name != requested.Name {
			result.Notes = append(result.Notes, fmt.Sprintf("installing required capability %q", capability.Name))
		}
	}

	// planned holds the content a file will have once this plan is applied. A
	// capability patching a file an earlier capability just planned reads it
	// from here instead of the (not yet written) disk.
	planned := map[string][]byte{}
	index := map[string]int{}
	upsert := func(operation project.Operation) {
		if i, ok := index[operation.Path]; ok {
			result.Operations[i] = operation
			return
		}
		index[operation.Path] = len(result.Operations)
		result.Operations = append(result.Operations, operation)
	}

	for _, capability := range order {
		vars := template.Vars{Name: manifest.Name, Module: manifest.Module, Version: capability.Version}
		for _, file := range capability.Files {
			if _, exists := planned[file.Path]; exists {
				return nil, &project.ConflictError{Path: file.Path, Reason: "planned by another capability"}
			}
			if _, err := os.Stat(filepath.Join(req.Dir, file.Path)); err == nil {
				return nil, &project.ConflictError{Path: file.Path, Reason: "unmanaged file already exists"}
			} else if !errors.Is(err, os.ErrNotExist) {
				return nil, err
			}
			content, err := capability.ReadFile(file)
			if err != nil {
				return nil, err
			}
			rendered := template.Render(content, vars)
			planned[file.Path] = rendered
			upsert(project.Operation{Path: file.Path, Content: rendered})
			manifest.SetFile(file.Path, capability.Name, rendered)
		}

		for _, patch := range capability.Patches {
			original, err := patchTarget(req.Dir, patch.Path, planned, manifest)
			if err != nil {
				return nil, err
			}
			snippet, err := capability.ReadPatch(patch)
			if err != nil {
				return nil, err
			}
			updated, already, err := project.PatchMarker(original, patch.Marker, capability.Name, template.Render(snippet, vars))
			if err != nil {
				return nil, &project.ConflictError{Path: patch.Path, Reason: err.Error()}
			}
			if already {
				continue
			}
			planned[patch.Path] = updated
			upsert(project.Operation{Path: patch.Path, Content: updated, Overwrite: true})
			manifest.SetFile(patch.Path, capability.Name, updated)
		}
		manifest.AddCapability(project.CapabilityRef{Name: capability.Name, Version: capability.Version})
		result.Installed = append(result.Installed, capability.Name)
	}

	encoded, err := manifest.Encode()
	if err != nil {
		return nil, err
	}
	upsert(project.Operation{Path: project.ManifestName, Content: encoded, Overwrite: true})
	return result, nil
}

// installOrder returns the capabilities to install for requested, dependencies
// first. A capability already recorded in the manifest, or reached twice, is
// skipped. requested is appended last so a dependency install never obscures
// it.
func installOrder(catalog *template.Catalog, manifest *project.Manifest, requested *template.Capability) ([]*template.Capability, error) {
	var order []*template.Capability
	visited := map[string]bool{}
	var visit func(capability *template.Capability) error
	visit = func(capability *template.Capability) error {
		if manifest.HasCapability(capability.Name) || visited[capability.Name] {
			return nil
		}
		visited[capability.Name] = true
		for _, required := range capability.Requires {
			dependency, err := catalog.Get(required)
			if err != nil {
				return fmt.Errorf("capability %q requires %q: %w", capability.Name, required, err)
			}
			if err := visit(dependency); err != nil {
				return err
			}
		}
		order = append(order, capability)
		return nil
	}
	if err := visit(requested); err != nil {
		return nil, err
	}
	return order, nil
}

// patchTarget returns the content of an extension point file as it will be once
// this plan is applied. It prefers a file already planned in this run so a
// capability can patch a file an earlier capability just planned. It refuses to
// patch a file weld does not manage, so user-owned files are never modified.
func patchTarget(root, path string, planned map[string][]byte, manifest *project.Manifest) ([]byte, error) {
	if content, ok := planned[path]; ok {
		return content, nil
	}
	if _, err := os.Stat(filepath.Join(root, path)); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, &project.ConflictError{Path: path, Reason: "extension point file is missing"}
		}
		return nil, err
	}
	if !manifest.Owns(path) {
		return nil, &project.ConflictError{Path: path, Reason: "extension point file is not managed by weld"}
	}
	content, err := os.ReadFile(filepath.Join(root, path))
	if err != nil {
		return nil, err
	}
	return content, nil
}

func alreadyInstalledNotes(root string, manifest *project.Manifest, capability *template.Capability) []string {
	notes := []string{fmt.Sprintf("%s %s is already installed", capability.Name, manifest.CapabilityVersion(capability.Name))}
	drift := manifest.Drift(root)
	if len(drift) == 0 {
		return notes
	}
	notes = append(notes, fmt.Sprintf("%d managed file(s) changed since install:", len(drift)))
	for _, item := range drift {
		notes = append(notes, fmt.Sprintf("  %s (%s)", item.Path, item.Reason))
	}
	return notes
}
