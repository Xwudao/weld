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
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/Xwudao/weld/internal/loomgen"
	"github.com/Xwudao/weld/internal/migration"
	"github.com/Xwudao/weld/internal/project"
	"github.com/Xwudao/weld/internal/semver"
	"github.com/Xwudao/weld/internal/template"
)

// The Go module files are owned by the toolchain, not by weld: `go mod tidy`,
// every build and `go get` rewrite them, and a project may add requirements
// outside the weld:deps region on purpose (the region is regenerated, so
// hand-written requirements have to live elsewhere). `weld upgrade` therefore
// never rewrites them — it only extends them through a marker patch at install
// time — and their recorded hash is refreshed after a generator may have touched
// them, so toolchain churn is never reported as drift.
const (
	goModPath = "go.mod"
	goSumPath = "go.sum"
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
	// WithDependencies installs a request's missing requirements in the same plan.
	// Without it a missing requirement is an error, so `weld add` never installs a
	// capability the caller did not ask for silently.
	WithDependencies bool
	// Verify is the go command that checks the project after the plan is written,
	// as a subcommand and its arguments (for example {"vet", "./..."}). An empty
	// list writes the plan unchecked, which is the library default; the CLI sets
	// it, so `weld upgrade` cannot leave a project that does not compile.
	Verify []string
	// Now overrides the clock used to version a capability migration. A nil clock
	// uses time.Now; tests set it so a planned migration filename is predictable.
	Now func() time.Time
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
	// Verify is the post-apply check, copied from the request. When it is set,
	// Apply runs it before and after the write: a project that already fails the
	// check is written with a note (the check cannot attribute a failure to this
	// plan), and a project that fails it afterwards is rolled back.
	Verify []string
}

// Apply writes the planned result. When the plan changes the dependency graph it
// also regenerates the Loom initializer; if generation, the manifest refresh or
// the post-apply check fails, the whole plan is rolled back, so a project is
// never left with a graph that does not match its generated code, a manifest
// that does not match the files, or code that does not build.
func (r *Result) Apply() error {
	if len(r.Operations) == 0 && !r.GenerateLoom {
		return nil
	}
	// The check decides whether a failure is this plan's fault, so the diagnostics
	// that already exist before the plan is written are collected first: a project
	// that does not build yet stays upgradable, and only a diagnostic this plan
	// introduces is a failure.
	var before []string
	if len(r.Verify) > 0 {
		if output, err := goCheck(r.Dir, r.Verify); err != nil {
			before = diagnosticLines(output)
			r.Notes = append(r.Notes, fmt.Sprintf(
				"the project already fails `go %s` before this change; only a diagnostic this upgrade introduces is treated as a failure:\n%s",
				strings.Join(r.Verify, " "), strings.TrimSpace(string(output))))
		}
	}

	paths := make([]string, 0, len(r.Operations)+3)
	for _, operation := range r.Operations {
		paths = append(paths, operation.Path)
	}
	if r.GenerateLoom {
		paths = append(paths, filepath.Join(r.DIPackage, "loom_gen.go"), goModPath, goSumPath)
	}
	snapshot, err := project.TakeSnapshot(r.Dir, paths)
	if err != nil {
		return err
	}
	if err := project.Apply(r.Dir, r.Operations); err != nil {
		return err
	}
	var generated []string
	if r.GenerateLoom {
		if err := loomgen.Generate(r.Dir, r.DIPackage); err != nil {
			return r.rollback(snapshot, err)
		}
		generated = append(generated, path.Join(r.DIPackage, "loom_gen.go"))
	}
	if r.GenerateLoom || len(r.Verify) > 0 {
		// The generator and the check both run with -mod=mod, so they may rewrite
		// the module files as they resolve the module graph. Record what they
		// wrote — and only what they wrote, because the recorded hash is the
		// baseline `weld upgrade` trusts.
		generated = append(generated, goModPath, goSumPath)
	}
	if len(r.Verify) > 0 {
		output, err := goCheck(r.Dir, r.Verify)
		if err != nil {
			if introduced := newDiagnostics(before, output); len(introduced) > 0 {
				return r.rollback(snapshot, fmt.Errorf("go %s: %w\n%s",
					strings.Join(r.Verify, " "), err, strings.Join(introduced, "\n")))
			}
			r.Notes = append(r.Notes, fmt.Sprintf(
				"`go %s` still fails after this change, with the same diagnostics as before it, so the upgrade was kept",
				strings.Join(r.Verify, " ")))
		}
	}
	if err := project.RefreshManifest(r.Dir, generated...); err != nil {
		return r.rollback(snapshot, fmt.Errorf("refresh manifest: %w", err))
	}
	return nil
}

// rollback restores the snapshot after a failed apply and reports the original
// failure, saying so when the rollback itself failed.
func (r *Result) rollback(snapshot *project.Snapshot, failure error) error {
	if restoreErr := snapshot.Restore(r.Dir); restoreErr != nil {
		return fmt.Errorf("%w (rollback also failed: %v)", failure, restoreErr)
	}
	return fmt.Errorf("%w (changes rolled back)", failure)
}

// diagnosticLines returns the non-empty, trimmed lines of a check's output, which
// is the unit a before/after comparison works on.
func diagnosticLines(output []byte) []string {
	var lines []string
	for _, line := range strings.Split(string(output), "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			lines = append(lines, trimmed)
		}
	}
	return lines
}

// newDiagnostics returns the lines of output that did not appear in before, in
// order. It is what turns "does the check pass?" into "did this plan break
// something?", so a project that was already failing is not held hostage by it.
func newDiagnostics(before []string, output []byte) []string {
	known := make(map[string]bool, len(before))
	for _, line := range before {
		known[line] = true
	}
	var introduced []string
	for _, line := range diagnosticLines(output) {
		if !known[line] {
			introduced = append(introduced, line)
		}
	}
	return introduced
}

// goCheck runs a go subcommand inside the project. It mirrors the generator's
// environment: the project's own nested tool modules and any workspace file must
// not change what the check resolves.
func goCheck(dir string, args []string) ([]byte, error) {
	if len(args) == 0 {
		return nil, nil
	}
	cmd := exec.Command("go", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=-mod=mod")
	return cmd.CombinedOutput()
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
		manifest.SetFile(file.Path, base.Name, base.RoleOf(file.Path), rendered)
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
	// migrations allocates goose versions per migration directory, created on
	// first use so a plan that ships no migration never reads the project.
	migrations map[string]*migration.Allocator
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
func (p *planner) planNewFile(path, capability, source string, role project.Role, content []byte) error {
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
	p.manifest.SetPayloadFile(path, capability, source, role, content)
	return nil
}

// payloadPath returns the project path for a capability file.
//
// A versioned file (a goose migration) gets a version allocated now, so an
// installed capability never writes a migration that sorts before one the project
// already has; every other file keeps its declared path.
func (p *planner) payloadPath(capability string, file template.File) (string, error) {
	if !file.Versioned {
		return file.Path, nil
	}
	dir := path.Dir(file.Path)
	// A migration with the payload's own name already in the project is not weld's
	// to duplicate: the same schema change is probably applied already, and a second
	// migration that creates the same table would fail when goose runs it.
	matches, err := filepath.Glob(filepath.Join(p.req.Dir, dir, "*_"+path.Base(file.Path)))
	if err != nil {
		return "", fmt.Errorf("inspect %s: %w", dir, err)
	}
	if len(matches) > 0 {
		existing, err := filepath.Rel(p.req.Dir, matches[0])
		if err != nil {
			existing = matches[0]
		}
		return "", &project.ConflictError{
			Path:   existing,
			Reason: fmt.Sprintf("a migration named %s already exists; capability %q would run the same change again, so move or remove it first", path.Base(file.Path), capability),
		}
	}
	if p.migrations == nil {
		p.migrations = map[string]*migration.Allocator{}
	}
	allocator, ok := p.migrations[dir]
	if !ok {
		allocator = migration.NewAllocator(filepath.Join(p.req.Dir, dir), p.req.Now)
		p.migrations[dir] = allocator
	}
	version, err := allocator.Next()
	if err != nil {
		return "", fmt.Errorf("read %s: %w", dir, err)
	}
	return file.ProjectPath(migration.Format(version)), nil
}

// planMarker inserts a snippet into the named marker region of a managed file,
// keyed by capability for idempotency. It is the dynamic counterpart of
// applyPatch for a per-name addition that has no capability payload: the snippet
// is built in code and recorded under owner.
func (p *planner) planMarker(path, marker, capability, owner string, role project.Role, snippet []byte) error {
	original, bootstrapped, err := patchTarget(p.req.Dir, template.Patch{Path: path, Marker: marker}, p.planned, p.manifest)
	if err != nil {
		return err
	}
	updated, already, err := project.PatchMarker(original, marker, capability, snippet)
	if err != nil {
		return &project.ConflictError{Path: path, Reason: err.Error()}
	}
	if already && !bootstrapped {
		return nil
	}
	p.planned[path] = updated
	p.upsert(project.Operation{Path: path, Content: updated, Overwrite: true})
	p.manifest.SetFile(path, owner, role, updated)
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
		role := file.Role
		if role == "" {
			role = project.RolePayload
		}
		target, err := p.payloadPath(capability.Name, file)
		if err != nil {
			return err
		}
		if err := p.planNewFile(target, capability.Name, file.Source, role, rendered); err != nil {
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
	for _, note := range capability.PostInstall {
		p.result.Notes = append(p.result.Notes, string(template.Render([]byte(note), vars)))
	}
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
	// A patch extends a file another capability ships, so it records the new
	// content without claiming ownership: the manifest keeps the shipping
	// capability and its declared role, and only the hash moves.
	p.manifest.SetPatchedFile(patch.Path, capability.Name, p.payloadRole(patch.Path), updated)
	return nil
}

// payloadRole returns the role the capability that ships target declares for it,
// so a marker patch records a shared file as its owner declares it (the Makefile
// stays a seam) instead of flattening every patch target into RolePatched. A
// target no installed capability ships is a patch-only file, which is patched.
func (p *planner) payloadRole(target string) project.Role {
	names, err := p.req.Catalog.Names()
	if err != nil {
		return project.RolePatched
	}
	for _, name := range names {
		if !p.present[name] {
			continue
		}
		capability, err := p.req.Catalog.Get(name)
		if err != nil {
			continue
		}
		if file, ok := capability.FileFor(target); ok {
			if file.Role == "" {
				return project.RolePayload
			}
			return file.Role
		}
	}
	return project.RolePatched
}

// finish renders the dependency graph for the resulting installed set and
// writes the manifest.
func (p *planner) finish() error {
	// Before regenerating the graph, make sure the stable provider seams it will
	// reference in its own package exist: a project scaffolded by an older weld
	// (or one adding a module) may lack a file a newer weld introduced, and
	// regenerating di.go without it would not compile.
	if err := p.restoreMissingDIFiles(); err != nil {
		return err
	}
	if err := reconcileDI(p.req, p.manifest, p.present, p.planned, p.upsert, p.result); err != nil {
		return err
	}
	return p.writeManifest()
}

// writeManifest records the manifest as a planned write, without touching the
// dependency graph. It is finish for a plan (such as an enum) that does not
// affect the graph. An unchanged manifest plans nothing, so a repeat run stays a
// no-op while a manifest that gained an entry is still written.
func (p *planner) writeManifest() error {
	encoded, err := p.manifest.Encode()
	if err != nil {
		return err
	}
	if planned, ok := p.planned[project.ManifestName]; ok {
		if bytes.Equal(planned, encoded) {
			return nil
		}
	} else if current, err := os.ReadFile(filepath.Join(p.req.Dir, project.ManifestName)); err == nil && bytes.Equal(current, encoded) {
		return nil
	}
	p.planned[project.ManifestName] = encoded
	p.upsert(project.Operation{Path: project.ManifestName, Content: encoded, Overwrite: true})
	return nil
}

// Add plans installing one or more additive capabilities into an existing
// project.
//
// The capabilities share one plan, so a requirement two of them need is
// installed once and requested capabilities are installed in argument order. A
// missing requirement is an error by default, naming the capabilities that
// would be installed; Request.WithDependencies opts into installing them in the
// same plan. Capabilities already recorded in the manifest are excluded, so a
// repeat add is a no-op that only refreshes the generated graph.
func Add(req Request, names ...string) (*Result, error) {
	if len(names) == 0 {
		return nil, errors.New("at least one capability is required")
	}
	manifest, err := project.Load(req.Dir)
	if err != nil {
		return nil, err
	}
	requested, err := requestedCapabilities(req.Catalog, names)
	if err != nil {
		return nil, err
	}

	result := &Result{Dir: req.Dir, Capability: requested[0]}
	var installed, pending []*template.Capability
	for _, capability := range requested {
		if manifest.HasCapability(capability.Name) {
			installed = append(installed, capability)
			continue
		}
		pending = append(pending, capability)
	}
	if len(installed) > 0 {
		result.Notes = append(result.Notes, alreadyInstalledNotes(req.Dir, manifest, installed...)...)
	}
	// An installed capability owns its files: when every requested capability is
	// already installed, the repeat add is a no-op that only refreshes the
	// generated graph, and it never installs a requirement.
	if len(pending) == 0 {
		return result, reconcileInstalled(req, manifest, result)
	}

	order, err := requiredCapabilities(req.Catalog, manifest, pending...)
	if err != nil {
		return nil, err
	}
	// Dependencies are never installed silently: a missing requirement is an
	// error unless the caller opted in with WithDependencies. A requested
	// capability that another request needs is part of the plan, not missing.
	if missing := capabilityNamesExcept(order, pending...); len(missing) > 0 && !req.WithDependencies {
		return nil, &MissingRequirementsError{Subject: capabilitySubject(pending), Missing: missing}
	}
	for _, capability := range order {
		if !containsCapability(pending, capability.Name) {
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

// requestedCapabilities resolves names to additive capabilities, preserving
// argument order and dropping duplicates so one plan never installs a
// capability twice.
func requestedCapabilities(catalog *template.Catalog, names []string) ([]*template.Capability, error) {
	seen := make(map[string]bool, len(names))
	requested := make([]*template.Capability, 0, len(names))
	for _, name := range names {
		if seen[name] {
			continue
		}
		seen[name] = true
		capability, err := catalog.Get(name)
		if err != nil {
			return nil, err
		}
		if capability.Kind != template.KindAdd {
			return nil, fmt.Errorf("capability %q is not an additive capability", name)
		}
		requested = append(requested, capability)
	}
	return requested, nil
}

// capabilitySubject names the requested capabilities in a missing-requirement
// error, keeping the single-capability wording unchanged.
func capabilitySubject(requested []*template.Capability) string {
	if len(requested) == 1 {
		return fmt.Sprintf("capability %q", requested[0].Name)
	}
	return fmt.Sprintf("capabilities %s", quoteNames(capabilityNames(requested)))
}

// reconcileInstalled re-renders the generated dependency graph for an unchanged
// installed set, so `weld add <installed>` refreshes a project whose generated
// files were written by an older weld. It returns without writing anything when
// the rendered graph already matches what is on disk, so a repeat add stays a
// no-op.
func reconcileInstalled(req Request, manifest *project.Manifest, result *Result) error {
	p := newPlanner(req, manifest, result)
	if err := p.restoreMissingDIFiles(); err != nil {
		return err
	}
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

// restoreMissingDIFiles plans a capability's generated-package Go provider
// seam that is absent on disk.
//
// The regenerated graph references the stable seams by name (for example the
// loom capability's projectModule in internal/di/providers.go), so
// a project scaffolded by an older weld must gain such a file before di.go is
// regenerated to reference it, and adding a module to such a project regenerates
// di.go the same way. The rule is narrow on purpose: only a capability that
// declares the DI graph is considered, only the files it ships under that
// graph's own directory and only its Go files, so an arbitrary project-deleted
// file (a README, a config patch target, another capability's provider) is never
// resurrected. A file that already exists is never planned, so a project's edits
// to a stable seam survive.
func (p *planner) restoreMissingDIFiles() error {
	names, err := p.req.Catalog.Names()
	if err != nil {
		return err
	}
	for _, name := range names {
		if !p.present[name] {
			continue
		}
		capability, err := p.req.Catalog.Get(name)
		if err != nil {
			return err
		}
		if capability.DI == nil {
			continue
		}
		for _, file := range capability.Files {
			if path.Dir(file.Path) != capability.DI.Dir || !strings.HasSuffix(file.Path, ".go") {
				continue
			}
			if !entryApplies(p.present, file.When, file.WhenAbsent) {
				continue
			}
			if _, planned := p.planned[file.Path]; planned {
				continue
			}
			if _, err := os.Stat(filepath.Join(p.req.Dir, file.Path)); !errors.Is(err, os.ErrNotExist) {
				continue
			}
			content, err := capability.ReadFile(file)
			if err != nil {
				return err
			}
			vars := template.Vars{Name: p.manifest.Name, Module: p.manifest.Module, Version: capability.Version}
			role := file.Role
			if role == "" {
				role = project.RolePayload
			}
			if err := p.planNewFile(file.Path, capability.Name, "", role, template.Render(content, vars)); err != nil {
				return err
			}
		}
	}
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

func alreadyInstalledNotes(root string, manifest *project.Manifest, capabilities ...*template.Capability) []string {
	var notes []string
	for _, capability := range capabilities {
		recorded := manifest.CapabilityVersion(capability.Name)
		notes = append(notes, fmt.Sprintf("%s %s is already installed", capability.Name, recorded))
		// A repeat add never upgrades: the payload that would move the project
		// forward is `weld upgrade`, so point at it rather than leaving the newer
		// version undiscovered.
		if newer, err := semver.IsNewer(capability.Version, recorded); err == nil && newer {
			notes = append(notes, fmt.Sprintf("this weld ships %s %s; run: weld upgrade %s", capability.Name, capability.Version, capability.Name))
		}
	}
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
			// A generated source is not an editable seam, but silently replacing a
			// hand-written dependency can compile while dropping business behavior.
			// Check the original baseline before recording the newly rendered one.
			if _, alreadyPlanned := planned[source.path]; !alreadyPlanned && current != nil {
				for _, entry := range manifest.Files {
					if entry.Path == source.path && (manifest.LegacyBaseline || !entry.Baseline || project.HashContent(current) != entry.SHA256) {
						return &project.ConflictError{Path: source.path, Reason: "generated DI source was modified or has no trustworthy baseline; move business wiring to stable provider seams, create modules with weld add module <name>, then restore the generated source before retrying; for a legacy source without a trustworthy baseline, review and preserve its wiring before removing only that source to regenerate it\n" + project.UnifiedDiff(source.path, current, source.content)}
					}
				}
			}
			_, statErr := os.Stat(filepath.Join(req.Dir, source.path))
			planned[source.path] = source.content
			upsert(project.Operation{Path: source.path, Content: source.content, Overwrite: statErr == nil})
			manifest.SetFile(source.path, capability.Name, project.RoleGenerated, source.content)
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
