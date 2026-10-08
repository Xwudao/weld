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
func Add(req Request, name string) (*Result, error) {
	manifest, err := project.Load(req.Dir)
	if err != nil {
		return nil, err
	}
	capability, err := req.Catalog.Get(name)
	if err != nil {
		return nil, err
	}
	if capability.Kind != template.KindAdd {
		return nil, fmt.Errorf("capability %q is not an additive capability", name)
	}
	for _, required := range capability.Requires {
		if !manifest.HasCapability(required) {
			return nil, fmt.Errorf("capability %q requires %q", name, required)
		}
	}

	if manifest.HasCapability(name) {
		return &Result{
			Dir:        req.Dir,
			Capability: capability,
			Notes:      alreadyInstalledNotes(req.Dir, manifest, capability),
		}, nil
	}

	vars := template.Vars{Name: manifest.Name, Module: manifest.Module, Version: capability.Version}
	var operations []project.Operation

	for _, file := range capability.Files {
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
		operations = append(operations, project.Operation{Path: file.Path, Content: rendered})
		manifest.SetFile(file.Path, capability.Name, rendered)
	}

	for _, patch := range capability.Patches {
		original, err := os.ReadFile(filepath.Join(req.Dir, patch.Path))
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil, &project.ConflictError{Path: patch.Path, Reason: "extension point file is missing"}
			}
			return nil, err
		}
		snippet, err := capability.ReadPatch(patch)
		if err != nil {
			return nil, err
		}
		updated, already, err := project.PatchMarker(original, patch.Marker, template.Render(snippet, vars))
		if err != nil {
			return nil, &project.ConflictError{Path: patch.Path, Reason: err.Error()}
		}
		if already {
			continue
		}
		operations = append(operations, project.Operation{Path: patch.Path, Content: updated, Overwrite: true})
		manifest.SetFile(patch.Path, capability.Name, updated)
	}

	manifest.AddCapability(project.CapabilityRef{Name: capability.Name, Version: capability.Version})
	encoded, err := manifest.Encode()
	if err != nil {
		return nil, err
	}
	operations = append(operations, project.Operation{
		Path:      project.ManifestName,
		Content:   encoded,
		Overwrite: true,
	})

	return &Result{Dir: req.Dir, Operations: operations, Capability: capability}, nil
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
