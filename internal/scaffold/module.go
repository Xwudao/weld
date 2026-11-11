package scaffold

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/Xwudao/weld/internal/project"
	"github.com/Xwudao/weld/internal/template"
)

// moduleNamePattern matches a canonical business-module name. The name is both
// the generated Go package name and the URL segment under /api/, so it must be a
// valid, lower-case Go identifier.
var moduleNamePattern = regexp.MustCompile(`^[a-z][a-z0-9]*$`)

// reservedModuleNames are the built-in names a business module may not claim.
// They are reserved whether or not any capability is installed, so adding a
// capability later can never collide with a module already on disk.
var reservedModuleNames = map[string]string{
	"api": "the /api/ namespace is the root of the module URL space",
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
// It requires http (and the loom and config http requires). A missing
// requirement is an error by default; Request.WithDependencies installs it in
// the same plan. With Loom installed the module is wired through the regenerated
// dependency graph; without Loom, through a httpserver route and the weld:routes
// extension point. The module files are written once and recorded in the
// manifest under module:<name>, so a later `weld add` never rewrites them.
//
// With req.Adopt it registers an existing hand-written package instead: nothing
// under internal/modules/<name> is written or recorded as a managed file, the
// package must already declare the module contract (see CheckModuleContract),
// and the module is recorded and wired exactly like a generated one.
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
	moduleDir := filepath.Join(req.Dir, ModuleDir(name))
	if req.Adopt {
		if err := CheckModuleContract(moduleDir); err != nil {
			return nil, err
		}
	} else if hasGoFiles(moduleDir) {
		return nil, &project.ConflictError{
			Path:   ModuleDir(name),
			Reason: fmt.Sprintf("a hand-written package already exists; register it with `weld add module %s --adopt`", name),
		}
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
	if len(order) > 0 && !req.WithDependencies {
		return nil, &MissingRequirementsError{Subject: fmt.Sprintf("module %q", name), Missing: capabilityNames(order)}
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
		if req.Adopt {
			break
		}
		content, err := moduleTemplate.ReadFile(file)
		if err != nil {
			return nil, err
		}
		path := string(template.Render([]byte(file.Path), vars))
		rendered := template.Render(content, vars)
		if err := p.planNewFile(path, capability, "", project.RoleSeam, rendered); err != nil {
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

// ModuleDir is the project-relative directory of the business module name.
func ModuleDir(name string) string {
	return filepath.ToSlash(filepath.Join("internal", "modules", name))
}

// moduleContract is what the generated dependency graph references in a module
// package: Name for the mount prefix, Service as the injected type, NewService
// as its Loom provider and Register to install the routes.
var moduleContract = []string{"Name", "Register", "NewService", "Service"}

// CheckModuleContract reports why the package in dir cannot be wired as a
// module: it is missing, has no Go files, does not parse, or lacks a top-level
// declaration the generated graph references. It checks names only; the Loom
// generation and build that follow an add catch a mismatched signature.
func CheckModuleContract(dir string) error {
	fset := token.NewFileSet()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("adopt module: %s does not exist", filepath.ToSlash(dir))
		}
		return err
	}
	declared := map[string]bool{}
	files := 0
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, parser.SkipObjectResolution)
		if err != nil {
			return fmt.Errorf("adopt module: %w", err)
		}
		files++
		for _, decl := range file.Decls {
			switch decl := decl.(type) {
			case *ast.FuncDecl:
				if decl.Recv == nil {
					declared[decl.Name.Name] = true
				}
			case *ast.GenDecl:
				for _, spec := range decl.Specs {
					switch spec := spec.(type) {
					case *ast.TypeSpec:
						declared[spec.Name.Name] = true
					case *ast.ValueSpec:
						for _, ident := range spec.Names {
							declared[ident.Name] = true
						}
					}
				}
			}
		}
	}
	if files == 0 {
		return fmt.Errorf("adopt module: %s has no Go files", filepath.ToSlash(dir))
	}
	var missing []string
	for _, name := range moduleContract {
		if !declared[name] {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("adopt module: %s does not declare %s; a module needs Name, Service, NewService and Register(*httpx.Router, Service)", filepath.ToSlash(dir), strings.Join(missing, ", "))
	}
	return nil
}

// UnregisteredModules lists the packages under internal/modules that weld.json
// does not record as modules, sorted. Their routes and providers are wired by
// hand, outside the generated graph.
func UnregisteredModules(root string, manifest *project.Manifest) ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(root, "internal", "modules"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var names []string
	for _, entry := range entries {
		if entry.IsDir() && !manifest.HasModule(entry.Name()) && hasGoFiles(filepath.Join(root, "internal", "modules", entry.Name())) {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

// hasGoFiles reports whether dir directly holds a Go file.
func hasGoFiles(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".go") {
			return true
		}
	}
	return false
}
