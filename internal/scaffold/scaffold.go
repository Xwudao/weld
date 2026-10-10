// Package scaffold builds the plans behind `weld new` and `weld add`.
//
// Planning and applying are separate: a plan is fully built and conflict-checked
// against the project before anything is written, which is what makes dry-run
// and safe failure possible.
package scaffold

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/Xwudao/weld/internal/loomgen"
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
	// Command requests the optional Cobra command group for `weld add module
	// <name> --command`. It is ignored by `weld new` and `weld add <capability>`.
	Command bool
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
	// GenerateLoom requests a Loom regeneration after the plan is written,
	// because an installed capability changed the dependency graph.
	GenerateLoom bool
	// DIPackage is the package directory to regenerate, relative to Dir.
	DIPackage string
}

// Apply writes the planned result. When the plan changes the dependency graph it
// also regenerates the Loom initializer; if generation fails the whole plan is
// rolled back, so a project is never left with a graph that does not match its
// generated code.
func (r *Result) Apply() error {
	if len(r.Operations) == 0 && !r.GenerateLoom {
		return nil
	}
	if !r.GenerateLoom {
		return project.Apply(r.Dir, r.Operations)
	}
	return r.applyAndGenerate()
}

func (r *Result) applyAndGenerate() error {
	paths := make([]string, 0, len(r.Operations)+3)
	for _, operation := range r.Operations {
		paths = append(paths, operation.Path)
	}
	paths = append(paths, filepath.Join(r.DIPackage, "loom_gen.go"), "go.mod", "go.sum")
	snapshot, err := project.TakeSnapshot(r.Dir, paths)
	if err != nil {
		return err
	}
	if err := project.Apply(r.Dir, r.Operations); err != nil {
		return err
	}
	if err := loomgen.Generate(r.Dir, r.DIPackage); err != nil {
		if restoreErr := snapshot.Restore(r.Dir); restoreErr != nil {
			return fmt.Errorf("%w (rollback also failed: %v)", err, restoreErr)
		}
		return fmt.Errorf("%w (changes rolled back)", err)
	}
	// The generator runs with -mod=mod, so it may rewrite go.mod as it resolves
	// the module graph. Record what it wrote rather than reporting it as drift.
	if err := project.RefreshManifest(r.Dir); err != nil {
		if restoreErr := snapshot.Restore(r.Dir); restoreErr != nil {
			return fmt.Errorf("refresh manifest: %w (rollback also failed: %v)", err, restoreErr)
		}
		return fmt.Errorf("refresh manifest: %w (changes rolled back)", err)
	}
	return nil
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

// planner accumulates one `weld add` plan. It renders capability payloads and,
// for `weld add module`, module payloads, tracking which paths are already
// planned so a later payload can patch a file an earlier one produced and no
// path is written twice.
type planner struct {
	req      Request
	manifest *project.Manifest
	result   *Result
	// planned holds the content a file will have once this plan is applied.
	planned map[string][]byte
	// index maps a planned path to its operation, so a later payload replacing
	// the same file updates it in place.
	index map[string]int
	// present is the installed capability set guards and the DI graph are
	// rendered against. It starts from the manifest and grows as dependencies are
	// planned, so a capability's conditional payload follows the set that will
	// exist once the plan is applied.
	present map[string]bool
}

func newPlanner(req Request, manifest *project.Manifest, result *Result) *planner {
	return &planner{
		req:      req,
		manifest: manifest,
		result:   result,
		planned:  map[string][]byte{},
		index:    map[string]int{},
		present:  installedSet(manifest),
	}
}

// upsert records an operation, replacing an existing operation for the same path.
func (p *planner) upsert(operation project.Operation) {
	if i, ok := p.index[operation.Path]; ok {
		p.result.Operations[i] = operation
		return
	}
	p.index[operation.Path] = len(p.result.Operations)
	p.result.Operations = append(p.result.Operations, operation)
}

// planNewFile plans a file that must not already exist. A path planned twice by
// two payloads, and a path that exists without weld owning it, are both
// conflicts, so a user file is never overwritten.
func (p *planner) planNewFile(path, capability string, content []byte) error {
	if _, exists := p.planned[path]; exists {
		return &project.ConflictError{Path: path, Reason: "planned by another capability"}
	}
	if _, err := os.Stat(filepath.Join(p.req.Dir, path)); err == nil {
		return &project.ConflictError{Path: path, Reason: "unmanaged file already exists"}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	p.planned[path] = content
	p.upsert(project.Operation{Path: path, Content: content})
	p.manifest.SetFile(path, capability, content)
	return nil
}

// installCapability plans one capability's files and patches, then records it.
func (p *planner) installCapability(capability *template.Capability) error {
	p.present[capability.Name] = true
	vars := template.Vars{Name: p.manifest.Name, Module: p.manifest.Module, Version: capability.Version}
	for _, file := range capability.Files {
		if !entryApplies(p.present, file.When, file.WhenAbsent) {
			continue
		}
		content, err := capability.ReadFile(file)
		if err != nil {
			return err
		}
		rendered := template.Render(content, vars)
		if err := p.planNewFile(file.Path, capability.Name, rendered); err != nil {
			return err
		}
	}
	for _, patch := range capability.Patches {
		if !entryApplies(p.present, patch.When, patch.WhenAbsent) {
			continue
		}
		if err := p.applyPatch(capability, patch, vars); err != nil {
			return err
		}
	}
	p.manifest.AddCapability(project.CapabilityRef{Name: capability.Name, Version: capability.Version})
	p.result.Installed = append(p.result.Installed, capability.Name)
	return nil
}

// applyPatch inserts one capability's snippet at a marker region, leaving every
// byte outside it untouched. A restored file is written even when the region
// already carries this capability's sentinel, because the file was absent, so
// skipping the write would leave it missing.
func (p *planner) applyPatch(capability *template.Capability, patch template.Patch, vars template.Vars) error {
	original, bootstrapped, err := patchTarget(p.req.Dir, patch, p.planned, p.manifest)
	if err != nil {
		return err
	}
	snippet, err := capability.ReadPatch(patch)
	if err != nil {
		return err
	}
	rendered := template.Render(snippet, vars)
	var updated []byte
	var already bool
	if patch.Mode == "replace" {
		updated, err = project.ReplaceMarker(original, patch.Marker, rendered)
	} else {
		updated, already, err = project.PatchMarker(original, patch.Marker, capability.Name, rendered)
	}
	if err != nil {
		return &project.ConflictError{Path: patch.Path, Reason: err.Error()}
	}
	if already && !bootstrapped {
		return nil
	}
	p.planned[patch.Path] = updated
	p.upsert(project.Operation{Path: patch.Path, Content: updated, Overwrite: true})
	p.manifest.SetFile(patch.Path, capability.Name, updated)
	return nil
}

// finish renders the dependency graph for the resulting installed set and
// writes the manifest.
func (p *planner) finish() error {
	if err := reconcileDI(p.req, p.manifest, p.present, p.planned, p.upsert, p.result); err != nil {
		return err
	}
	encoded, err := p.manifest.Encode()
	if err != nil {
		return err
	}
	p.upsert(project.Operation{Path: project.ManifestName, Content: encoded, Overwrite: true})
	return nil
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

	order, err := installOrder(req.Catalog, manifest, requested, map[string]bool{})
	if err != nil {
		return nil, err
	}
	// Every capability except the bare base CLI is developed on Loom: adding
	// anything plans Loom (and the config it requires) unless it is already
	// installed, so a project is always either the base CLI or a Loom project.
	// http and the capabilities that require it pull Loom through the dependency
	// graph; this covers config, the one leaf Loom itself requires.
	if !manifest.HasCapability("loom") && !containsCapability(order, "loom") {
		loomCapability, err := req.Catalog.Get("loom")
		if err != nil {
			return nil, err
		}
		loomOrder, err := installOrder(req.Catalog, manifest, loomCapability, selected(order))
		if err != nil {
			return nil, err
		}
		order = append(order, loomOrder...)
	}
	result := &Result{Dir: req.Dir, Capability: requested}
	if len(order) == 0 {
		result.Notes = alreadyInstalledNotes(req.Dir, manifest, requested)
		// An installed set that did not grow can still be out of date: the
		// generated graph follows the current templates, so re-adding an installed
		// capability refreshes di.go and di_test.go for a project scaffolded by an
		// older weld. Nothing is planned when the rendered graph already matches.
		return result, reconcileInstalled(req, manifest, result)
	}
	for _, capability := range order {
		if capability.Name != requested.Name {
			result.Notes = append(result.Notes, fmt.Sprintf("installing required capability %q", capability.Name))
		}
	}

	p := newPlanner(req, manifest, result)
	for _, capability := range order {
		if err := p.installCapability(capability); err != nil {
			return nil, err
		}
	}
	if err := p.finish(); err != nil {
		return nil, err
	}
	return result, nil
}

// reconcileInstalled re-renders the generated dependency graph for an unchanged
// installed set, so `weld add <installed>` refreshes a project whose generated
// files were written by an older weld. It returns without writing anything when
// the rendered graph already matches what is on disk, so a repeat add stays a
// no-op.
func reconcileInstalled(req Request, manifest *project.Manifest, result *Result) error {
	p := newPlanner(req, manifest, result)
	if err := reconcileDI(req, manifest, p.present, p.planned, p.upsert, result); err != nil {
		return err
	}
	if len(result.Operations) == 0 {
		return nil
	}
	encoded, err := manifest.Encode()
	if err != nil {
		return err
	}
	p.upsert(project.Operation{Path: project.ManifestName, Content: encoded, Overwrite: true})
	return nil
}

// containsCapability reports whether order already includes name.
func containsCapability(order []*template.Capability, name string) bool {
	for _, capability := range order {
		if capability.Name == name {
			return true
		}
	}
	return false
}

// selected returns the set of capability names in order, so a later installOrder
// call does not plan one twice.
func selected(order []*template.Capability) map[string]bool {
	names := make(map[string]bool, len(order))
	for _, capability := range order {
		names[capability.Name] = true
	}
	return names
}

// installOrder returns the capabilities to install for requested, dependencies
// first. A capability already recorded in the manifest, already selected by an
// earlier root in this plan, or reached twice is skipped. selected is shared
// across the roots of one plan so a capability that satisfies two of them is
// planned once. requested is appended last so a dependency install never
// obscures it.
func installOrder(catalog *template.Catalog, manifest *project.Manifest, requested *template.Capability, selected map[string]bool) ([]*template.Capability, error) {
	var order []*template.Capability
	visited := map[string]bool{}
	var visit func(capability *template.Capability) error
	visit = func(capability *template.Capability) error {
		if manifest.HasCapability(capability.Name) || selected[capability.Name] || visited[capability.Name] {
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
		selected[capability.Name] = true
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
//
// A missing target the manifest still records as weld-managed is the fresh-clone
// case: a git-ignored file such as config.yml is legitimately absent while its
// committed example remains. When the patch declares a bootstrap source the
// target is restored from it, and the second return reports that the file must
// be written even if the patch itself turns out to be a no-op.
func patchTarget(root string, patch template.Patch, planned map[string][]byte, manifest *project.Manifest) (content []byte, bootstrapped bool, err error) {
	if content, ok := planned[patch.Path]; ok {
		return content, false, nil
	}
	if _, statErr := os.Stat(filepath.Join(root, patch.Path)); statErr != nil {
		if !errors.Is(statErr, os.ErrNotExist) {
			return nil, false, statErr
		}
		if patch.Bootstrap == "" {
			return nil, false, &project.ConflictError{Path: patch.Path, Reason: "extension point file is missing"}
		}
		restored, err := bootstrapContent(root, patch.Path, patch, manifest)
		if err != nil {
			return nil, false, err
		}
		return restored, true, nil
	}
	if !manifest.Owns(patch.Path) {
		return nil, false, &project.ConflictError{Path: patch.Path, Reason: "extension point file is not managed by weld"}
	}
	content, err = os.ReadFile(filepath.Join(root, patch.Path))
	if err != nil {
		return nil, false, err
	}
	return content, false, nil
}

// bootstrapContent restores a missing, weld-managed patch target from the
// tracked source the patch declares (config.yml from config.example.yml).
//
// The source must be weld-managed and must carry the target's extension point;
// anything else is an actionable error rather than a silent default, so a fresh
// clone either restores the local file or names exactly how to. The absent local
// file has no content to preserve by definition, so a bootstrap can never clobber
// a local value or comment.
func bootstrapContent(root, path string, patch template.Patch, manifest *project.Manifest) ([]byte, error) {
	if !manifest.Owns(path) {
		return nil, &project.ConflictError{Path: path, Reason: "missing extension point file is not managed by weld"}
	}
	if !manifest.Owns(patch.Bootstrap) {
		return nil, &project.ConflictError{Path: path, Reason: fmt.Sprintf(
			"is missing and cannot be restored: %s is not managed by weld", patch.Bootstrap)}
	}
	content, err := os.ReadFile(filepath.Join(root, patch.Bootstrap))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, &project.ConflictError{Path: path, Reason: fmt.Sprintf(
				"is missing and cannot be restored: %s is not present (restore it with: git checkout %s)",
				patch.Bootstrap, patch.Bootstrap)}
		}
		return nil, err
	}
	if !project.HasMarkerRegion(content, patch.Marker) {
		return nil, &project.ConflictError{Path: path, Reason: fmt.Sprintf(
			"is missing and cannot be restored: %s has no weld:%s:begin/%s:end region (restore it with: git checkout %s)",
			patch.Bootstrap, patch.Marker, patch.Marker, patch.Bootstrap)}
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

// installedSet returns the capabilities already recorded in the manifest.
func installedSet(manifest *project.Manifest) map[string]bool {
	present := map[string]bool{manifest.Base.Name: true}
	for _, capability := range manifest.Capabilities {
		present[capability.Name] = true
	}
	return present
}

// entryApplies reports whether a conditional payload entry applies to the
// installed capability set. An empty guard always applies.
func entryApplies(present map[string]bool, when, whenAbsent []string) bool {
	for _, name := range when {
		if !present[name] {
			return false
		}
	}
	for _, name := range whenAbsent {
		if present[name] {
			return false
		}
	}
	return true
}

// diModules turns the manifest's business modules into the graph contribution a
// capability's DI template renders. Modules are recorded separately from
// capabilities (they carry their own name), so the graph follows them without a
// runtime registry, and the order is the install order so a regenerated graph is
// deterministic.
func diModules(manifest *project.Manifest) []template.DIModule {
	if len(manifest.Modules) == 0 {
		return nil
	}
	modules := make([]template.DIModule, 0, len(manifest.Modules))
	for _, module := range manifest.Modules {
		modules = append(modules, template.DIModule{Name: module.Name, Title: template.ExportName(module.Name)})
	}
	return modules
}

// reconcileDI renders the dependency graph of every installed capability that
// declares one, against the installed capability set.
//
// It runs whenever a plan is built, so adding a capability that feeds the graph
// (for example db) regenerates the graph deterministically instead of appending
// an order-dependent fragment. When the rendered source is unchanged it plans
// nothing, so a repeat add is a no-op.
func reconcileDI(req Request, manifest *project.Manifest, present map[string]bool, planned map[string][]byte, upsert func(project.Operation), result *Result) error {
	names, err := req.Catalog.Names()
	if err != nil {
		return err
	}
	for _, name := range names {
		if !present[name] {
			continue
		}
		capability, err := req.Catalog.Get(name)
		if err != nil {
			return err
		}
		if capability.DI == nil {
			continue
		}
		vars := template.DITemplateVars{
			Name:    manifest.Name,
			Module:  manifest.Module,
			Version: capability.Version,
			Caps:    template.CapabilitySet(present),
			Modules: diModules(manifest),
		}
		graph, err := capability.RenderDIGraph(vars)
		if err != nil {
			return err
		}
		test, err := capability.RenderDITest(vars)
		if err != nil {
			return err
		}
		sources := []struct {
			path    string
			content []byte
		}{
			{path.Join(capability.DI.Dir, "di.go"), graph},
			{path.Join(capability.DI.Dir, "di_test.go"), test},
		}
		for _, source := range sources {
			if source.content == nil {
				continue
			}
			current, managed, err := currentContent(req.Dir, source.path, planned, manifest)
			if err != nil {
				return err
			}
			if !managed {
				return &project.ConflictError{Path: source.path, Reason: "unmanaged file already exists"}
			}
			if bytes.Equal(current, source.content) {
				continue
			}
			_, statErr := os.Stat(filepath.Join(req.Dir, source.path))
			planned[source.path] = source.content
			upsert(project.Operation{Path: source.path, Content: source.content, Overwrite: statErr == nil})
			manifest.SetFile(source.path, capability.Name, source.content)
			result.GenerateLoom = true
			result.DIPackage = capability.DI.Dir
		}
	}
	return nil
}

// currentContent returns the content path will have once this plan is applied.
// managed reports whether weld may write it: a file already planned, a file that
// does not exist, or a file the manifest records as weld-owned.
func currentContent(root, path string, planned map[string][]byte, manifest *project.Manifest) (content []byte, managed bool, err error) {
	if content, ok := planned[path]; ok {
		return content, true, nil
	}
	raw, readErr := os.ReadFile(filepath.Join(root, path))
	switch {
	case readErr == nil:
		return raw, manifest.Owns(path), nil
	case errors.Is(readErr, os.ErrNotExist):
		return nil, true, nil
	default:
		return nil, false, readErr
	}
}
