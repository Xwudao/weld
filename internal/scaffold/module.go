package scaffold

import (
	"fmt"
	"go/token"
	"regexp"

	"github.com/Xwudao/weld/internal/project"
	"github.com/Xwudao/weld/internal/template"
)

// moduleNamePattern matches a canonical business-module name. The name is both
// the generated Go package name and the URL segment under /api/, so it must be a
// valid, lower-case Go identifier.
var moduleNamePattern = regexp.MustCompile(`^[a-z][a-z0-9]*$`)

// reservedModuleNames are the built-in API paths a module may not claim. They
// are reserved whether or not the api capability is installed, so adding api
// later can never collide with a module already on disk.
var reservedModuleNames = map[string]string{
	"api":     "the /api/ namespace is owned by the built-in api capability",
	"items":   "GET/POST /api/items is the built-in api capability's resource",
	"openapi": "GET /api/openapi.json is the built-in api capability's document",
}

// ValidateModuleName reports why a business-module name is unusable. A usable
// name is a canonical Go package name and a safe URL segment, is not a Go
// keyword, and is not one of the reserved built-in API paths.
func ValidateModuleName(name string) error {
	if !moduleNamePattern.MatchString(name) {
		return fmt.Errorf("invalid module name %q: use lowercase letters and digits starting with a letter (a valid Go package name and URL segment)", name)
	}
	if token.IsKeyword(name) {
		return fmt.Errorf("invalid module name %q: it is a Go keyword", name)
	}
	if reason, reserved := reservedModuleNames[name]; reserved {
		return fmt.Errorf("invalid module name %q: %s", name, reason)
	}
	return nil
}

// AddModule plans installing a business module named name: an internal/modules
// package with a typed request/response and a Service seam, registered on the
// HTTP surface under /api/<name>.
//
// It installs http, and the config that http requires, automatically when
// absent. With Loom installed the module is wired through the regenerated
// dependency graph; without Loom, through a httpserver route and the weld:routes
// extension point. The module files are written once and recorded in the
// manifest under module:<name>, so a later `weld add` never rewrites them.
func AddModule(req Request, name string) (*Result, error) {
	if err := ValidateModuleName(name); err != nil {
		return nil, err
	}
	manifest, err := project.Load(req.Dir)
	if err != nil {
		return nil, err
	}
	moduleTemplate, err := template.LoadModules()
	if err != nil {
		return nil, err
	}

	result := &Result{Dir: req.Dir}
	if manifest.HasModule(name) {
		result.Notes = alreadyInstalledModuleNotes(req.Dir, manifest, name)
		return result, nil
	}

	// A module is served over HTTP, so http (and its config dependency) is
	// installed first when absent. An already-present http is not reinstalled.
	httpCapability, err := req.Catalog.Get("http")
	if err != nil {
		return nil, err
	}
	order, err := installOrder(req.Catalog, manifest, httpCapability)
	if err != nil {
		return nil, err
	}
	if err := checkCLICompatibility(req.Dir, order); err != nil {
		return nil, err
	}
	for _, capability := range order {
		result.Notes = append(result.Notes, fmt.Sprintf("installing required capability %q", capability.Name))
	}

	p := newPlanner(req, manifest, result)
	for _, capability := range order {
		if err := p.installCapability(capability); err != nil {
			return nil, err
		}
	}

	capability := "module:" + name
	vars := template.Vars{
		Name:     manifest.Name,
		Module:   manifest.Module,
		Version:  moduleTemplate.Version,
		Mod:      name,
		ModTitle: template.ExportName(name),
	}
	for _, file := range moduleTemplate.Files {
		content, err := moduleTemplate.ReadFile(file)
		if err != nil {
			return nil, err
		}
		path := string(template.Render([]byte(file.Path), vars))
		if err := p.planNewFile(path, capability, template.Render(content, vars)); err != nil {
			return nil, err
		}
	}

	if !p.present["loom"] {
		if err := p.planModuleRoute(moduleTemplate, vars, capability); err != nil {
			return nil, err
		}
	}

	manifest.AddModule(project.ModuleRef{Name: name, Version: moduleTemplate.Version})
	if err := p.finish(); err != nil {
		return nil, err
	}
	return result, nil
}

// planModuleRoute writes the non-Loom route seam and appends it to the
// httpserver weld:routes extension point. A project with Loom installed skips
// this: the regenerated graph registers the module on the composed mux.
func (p *planner) planModuleRoute(moduleTemplate *template.ModuleTemplate, vars template.Vars, capability string) error {
	content, err := moduleTemplate.ReadRoute()
	if err != nil {
		return err
	}
	path := string(template.Render([]byte(moduleTemplate.Route.Path), vars))
	if err := p.planNewFile(path, capability, template.Render(content, vars)); err != nil {
		return err
	}

	const httpPath = "internal/httpserver/http.go"
	original, managed, err := currentContent(p.req.Dir, httpPath, p.planned, p.manifest)
	if err != nil {
		return err
	}
	if !managed {
		return &project.ConflictError{Path: httpPath, Reason: "httpserver composition is not managed by weld"}
	}
	snippet, err := moduleTemplate.ReadRouteSnippet()
	if err != nil {
		return err
	}
	updated, already, err := project.PatchMarker(original, "routes", capability, template.Render(snippet, vars))
	if err != nil {
		return &project.ConflictError{Path: httpPath, Reason: err.Error()}
	}
	if already {
		return nil
	}
	p.planned[httpPath] = updated
	p.upsert(project.Operation{Path: httpPath, Content: updated, Overwrite: true})
	p.manifest.SetFile(httpPath, capability, updated)
	return nil
}

// alreadyInstalledModuleNotes explains a repeated `weld add module <name>` and
// reports any managed file the user changed since it was generated.
func alreadyInstalledModuleNotes(root string, manifest *project.Manifest, name string) []string {
	notes := []string{fmt.Sprintf("module %s %s is already installed", name, manifest.ModuleVersion(name))}
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
