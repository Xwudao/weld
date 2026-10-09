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
//
// With req.Command it additionally generates a root command group backed by the
// same Service interface and NewService constructor, recorded under
// command:<name>. On a module that is already installed it writes only that
// command facet and never rewrites the user's module service or handler.
func AddModule(req Request, name string) (*Result, error) {
	if err := ValidateModuleName(name); err != nil {
		return nil, err
	}
	manifest, err := project.Load(req.Dir)
	if err != nil {
		return nil, err
	}

	result := &Result{Dir: req.Dir}
	// An installed module owns its name: its files are written once and a repeat
	// add is a no-op. With --command on an existing module only the command facet
	// may still be missing, and that is the one upgrade this command performs.
	if manifest.HasModule(name) {
		if !req.Command || manifest.HasCommand(name) {
			result.Notes = alreadyInstalledModuleNotes(req.Dir, manifest, name)
			return result, nil
		}
		if err := upgradeModuleCommand(req, manifest, result, name); err != nil {
			return nil, err
		}
		return result, nil
	}
	if manifest.HasCommand(name) {
		return nil, &project.ConflictError{
			Path:   "internal/modules/" + name,
			Reason: fmt.Sprintf("command %q is installed; rename the command or add a module command with `weld add module %s --command`", name, name),
		}
	}

	moduleTemplate, err := template.LoadModules()
	if err != nil {
		return nil, err
	}

	// A module is served over HTTP through the Loom graph, so http (with the loom
	// and config it requires) is installed first when absent. An already-present
	// http is not reinstalled.
	httpCapability, err := req.Catalog.Get("http")
	if err != nil {
		return nil, err
	}
	order, err := installOrder(req.Catalog, manifest, httpCapability, map[string]bool{})
	if err != nil {
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
		rendered := template.Render(content, vars)
		if file.Shared {
			if err := p.planSharedFile(path, capability, rendered); err != nil {
				return nil, err
			}
			continue
		}
		if err := p.planNewFile(path, capability, rendered); err != nil {
			return nil, err
		}
	}

	manifest.AddModule(project.ModuleRef{Name: name, Version: moduleTemplate.Version})
	if req.Command {
		if err := p.planModuleCommandFacet(manifest, name); err != nil {
			return nil, err
		}
	}

	if err := p.finish(); err != nil {
		return nil, err
	}
	return result, nil
}

// upgradeModuleCommand plans the command facet for a module that is already
// installed. It writes only the command files and records the command; the
// module's HTTP files, and any user edits to them, are never touched.
func upgradeModuleCommand(req Request, manifest *project.Manifest, result *Result, name string) error {
	p := newPlanner(req, manifest, result)
	if err := p.planModuleCommandFacet(manifest, name); err != nil {
		return err
	}
	return p.finish()
}

// planModuleCommandFacet writes the module-backed command group and records the
// command. It shares the command target paths with the generic group, so a
// module and an independent command can never be created for the same name.
// When Loom is installed it also plans the command-specific Loom graph and asks
// for the generated initializer to be refreshed.
func (p *planner) planModuleCommandFacet(manifest *project.Manifest, name string) error {
	commandTemplate, err := template.LoadCommands()
	if err != nil {
		return err
	}
	vars := commandVars(manifest, template.CapabilitySet(p.present), commandTemplate.Version, name)
	if err := p.planCommandFacet(commandTemplate, commandTemplate.Module, vars, "command:"+name); err != nil {
		return err
	}
	manifest.AddCommand(project.CommandRef{Name: name, Version: commandTemplate.Version})
	if p.present["loom"] {
		p.result.GenerateLoom = true
		p.result.DIPackage = "internal/di"
	}
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
