package scaffold

import (
	"fmt"
	"go/token"

	"github.com/Xwudao/weld/internal/project"
	"github.com/Xwudao/weld/internal/template"
)

// reservedCommandNames are the root command names a generated command group may
// not claim. The base registers `version` and the http capability registers
// `serve`; Cobra adds `help`. Reserving them keeps a generated group from
// shadowing a built-in or the server lifecycle, whichever order it is added in.
var reservedCommandNames = map[string]string{
	"serve":   "the HTTP server lifecycle is owned by the serve command",
	"help":    "help is provided by the command tree",
	"version": "version is provided by the base command tree",
}

// ValidateCommandName reports why a command-group name is unusable. A usable
// name is a canonical Go package name, is not a Go keyword, and is not a
// reserved root command.
func ValidateCommandName(name string) error {
	if !moduleNamePattern.MatchString(name) {
		return fmt.Errorf("invalid command name %q: use lowercase letters and digits starting with a letter (a valid Go package name)", name)
	}
	if token.IsKeyword(name) {
		return fmt.Errorf("invalid command name %q: it is a Go keyword", name)
	}
	if reason, reserved := reservedCommandNames[name]; reserved {
		return fmt.Errorf("invalid command name %q: %s", name, reason)
	}
	return nil
}

// AddCommand plans installing an independent root command group named name.
//
// It generates internal/commands/<name> and the registration file
// internal/app/<name>_command.go, which registers the group through the app
// package's RegisterCommand seam. The group is independent of the HTTP server,
// the configuration and the database, and a bare invocation shows help rather
// than starting any long-running work. The files are written once and recorded
// in the manifest under command:<name>, so a later `weld add` never rewrites
// them and a repeated add is a no-op.
//
// A name already used by a business module is a conflict: a module owns its
// command group through `weld add module <name> --command`, so the two cannot
// share a name.
func AddCommand(req Request, name string) (*Result, error) {
	if err := ValidateCommandName(name); err != nil {
		return nil, err
	}
	manifest, err := project.Load(req.Dir)
	if err != nil {
		return nil, err
	}
	if manifest.HasModule(name) {
		return nil, &project.ConflictError{
			Path:   "internal/commands/" + name,
			Reason: fmt.Sprintf("module %q is installed; add its command group with `weld add module %s --command`", name, name),
		}
	}
	result := &Result{Dir: req.Dir}
	if manifest.HasCommand(name) {
		result.Notes = alreadyInstalledCommandNotes(req.Dir, manifest, name)
		return result, nil
	}
	if err := checkCLICompatibility(req.Dir, nil, true); err != nil {
		return nil, err
	}
	commandTemplate, err := template.LoadCommands()
	if err != nil {
		return nil, err
	}

	p := newPlanner(req, manifest, result)
	vars := commandVars(manifest, commandTemplate.Version, name)
	if err := p.planCommandFacet(commandTemplate, commandTemplate.Generic, vars, "command:"+name); err != nil {
		return nil, err
	}
	manifest.AddCommand(project.CommandRef{Name: name, Version: commandTemplate.Version})
	if err := p.finish(); err != nil {
		return nil, err
	}
	return result, nil
}

// commandVars is the substitution context for a per-name command payload. It
// shares the module tokens (__modname__/__ModName__) with the module payload so
// one Render call substitutes both.
func commandVars(manifest *project.Manifest, version, name string) template.Vars {
	return template.Vars{
		Name:     manifest.Name,
		Module:   manifest.Module,
		Version:  version,
		Mod:      name,
		ModTitle: template.ExportName(name),
	}
}

// planCommandFacet writes one command variant's files, rendering the per-name
// paths and contents. The variant is either the independent generic group or
// the module-backed group; both declare the same target paths.
func (p *planner) planCommandFacet(commandTemplate *template.CommandTemplate, variant template.CommandVariant, vars template.Vars, capability string) error {
	for _, file := range variant.Files {
		content, err := commandTemplate.ReadFile(file)
		if err != nil {
			return err
		}
		path := string(template.Render([]byte(file.Path), vars))
		if err := p.planNewFile(path, capability, template.Render(content, vars)); err != nil {
			return err
		}
	}
	return nil
}

// alreadyInstalledCommandNotes explains a repeated command add and reports any
// managed file the user changed since it was generated.
func alreadyInstalledCommandNotes(root string, manifest *project.Manifest, name string) []string {
	notes := []string{fmt.Sprintf("command %s %s is already installed", name, manifest.CommandVersion(name))}
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
