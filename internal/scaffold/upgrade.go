package scaffold

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Xwudao/weld/internal/project"
	"github.com/Xwudao/weld/internal/semver"
	"github.com/Xwudao/weld/internal/template"
)

// Advisory is a change a newer capability payload implies for a file `weld
// upgrade` refuses to write on its own, either because the file belongs to the
// project or because the project changed it. It carries the unified diff the
// project applies by hand, so an upgrade never loses an edit.
type Advisory struct {
	Path       string
	Capability string
	// Reason is one of:
	//
	//	"seam"            the file is project-owned and weld never rewrites it;
	//	                  the shipped version changed, so fold it in yourself;
	//	"modified"        a payload file the project changed since weld wrote it;
	//	"missing"         a payload file the project deleted, never resurrected;
	//	"retired"         the payload no longer ships the file, which the project
	//	                  also changed, so it is not deleted either;
	//	"orphaned-region" the newer payload no longer declares a marker region the
	//	                  live file fills, so splicing would drop live content.
	Reason string
	// Detail explains an advisory that has no diff (a seam weld cannot compare, an
	// orphaned region). It is empty when Diff carries the explanation.
	Detail string
	// Diff is the unified diff from the project's content to what the newer
	// payload implies. It is advisory text, never applied.
	Diff string
}

// CapabilityDelta records one capability moving to a newer template version.
type CapabilityDelta struct {
	Name string
	From string
	To   string
	// Files lists the paths this upgrade writes for the capability, in payload
	// order.
	Files []string
}

// UpgradeResult is a planned `weld upgrade`.
type UpgradeResult struct {
	*Result
	// Upgraded lists the capabilities whose recorded version moved forward.
	Upgraded []CapabilityDelta
	// Advisories lists files weld will not rewrite on its own.
	Advisories []Advisory
	// Current lists installed capabilities already at this weld's version.
	Current []string
	// Unknown lists capabilities the manifest records that this weld build no
	// longer ships. They are reported and skipped, never silently forgotten.
	Unknown []string
}

// Behind reports whether any installed capability is older than this weld build.
func (r *UpgradeResult) Behind() bool { return len(r.Upgraded) > 0 }

// Upgrade plans moving installed capabilities to the versions this weld ships.
//
// It is deliberately conservative, because it runs over a project a user has
// been editing. Every file a capability ships has a role, declared by the
// capability that ships it (see template.Role), and the role decides what an
// upgrade may do:
//
//   - payload: a file weld owns outright. It is rewritten while the project has
//     not changed it since weld wrote it, and reported with a diff when it has;
//   - patched: a file weld owns that also carries marker regions another
//     capability extends (internal/config/config.go). The newer payload supplies
//     the skeleton and the live file supplies every region body, so the payload
//     change and everything inside a region both survive;
//   - seam: a file weld writes once and the project then owns (middleware.go,
//     policy.go, the *_providers.go and register.go files, the Makefile). It is
//     never written: the project gets the diff and folds the change in itself;
//   - generated: a file the Loom generator rewrites from the graph. It is
//     re-rendered, never merged and never pruned.
//
// The Go module files are owned by the toolchain and are never rewritten, and a
// file a newer payload no longer ships is deleted only with prune, only when it
// is a plain payload file and only when it is unmodified.
//
// Marker patches are not re-applied as snippets: a shared file is moved forward
// by splicing the live region bodies into the newer skeleton, so a patch
// snippet's own content changes are still a manual step. A changed snippet is
// reported as an advisory rather than silently re-inserted.
//
// names selects capabilities to upgrade; an empty list considers every installed
// capability. Planning reads the project and writes nothing; Result.Apply
// performs the writes, and regenerates the Loom graph when a payload changed it.
func Upgrade(req Request, names []string, prune bool) (*UpgradeResult, error) {
	manifest, err := project.Load(req.Dir)
	if err != nil {
		return nil, err
	}
	ordered, unknown, err := upgradeTargets(req.Catalog, manifest, names)
	if err != nil {
		return nil, err
	}

	result := &Result{Dir: req.Dir, Verify: req.Verify}
	upgrade := &UpgradeResult{Result: result, Unknown: unknown}
	for _, name := range unknown {
		result.Notes = append(result.Notes, fmt.Sprintf("capability %q is recorded but this weld no longer ships it; skipped", name))
	}
	if manifest.LegacyBaseline {
		result.Notes = append(result.Notes, "weld.json was written by an older weld, which recorded every file's hash after every `weld add`; a matching hash therefore cannot rule out a project edit. Weld stays conservative: project-owned seams are never rewritten, a shared file keeps its marker regions, and a file the project changed is reported instead of overwritten.")
	}
	p := newPlanner(req, manifest, result)
	// A marker patch records the patching capability as the file's writer, so the
	// manifest's "files of a capability" also contains shared files (go.mod, the
	// Makefile, .gitignore, config.yml, config.go). Those are never payload files
	// the capability could retire, so the retired-file pass excludes them.
	shared := patchedPaths(req.Catalog, p.present)
	shippers := pathShippers(req.Catalog, p.present)

	for _, capability := range ordered {
		recorded := manifest.CapabilityVersion(capability.Name)
		newer, err := semver.IsNewer(capability.Version, recorded)
		if err != nil {
			return nil, fmt.Errorf("capability %q: %w", capability.Name, err)
		}
		if !newer {
			upgrade.Current = append(upgrade.Current, capability.Name)
			result.Notes = append(result.Notes, driftNotes(req.Catalog, req.Dir, manifest, capability.Name)...)
			continue
		}
		files, advisories, err := p.upgradeCapability(capability, prune, shared, shippers)
		if err != nil {
			return nil, err
		}
		manifest.SetCapabilityVersion(capability.Name, capability.Version)
		upgrade.Upgraded = append(upgrade.Upgraded, CapabilityDelta{
			Name:  capability.Name,
			From:  recorded,
			To:    capability.Version,
			Files: files,
		})
		upgrade.Advisories = append(upgrade.Advisories, advisories...)
	}
	if err := p.finish(); err != nil {
		return nil, err
	}
	return upgrade, nil
}

// upgradeTargets resolves the capabilities an upgrade considers, in a
// deterministic order: the requested names in the order given, or every recorded
// capability with base first.
//
// A requested capability that is not installed is an error, because the caller
// asked for it. A recorded capability this weld build no longer ships is returned
// as unknown so the plan can report it instead of failing.
func upgradeTargets(catalog *template.Catalog, manifest *project.Manifest, names []string) ([]*template.Capability, []string, error) {
	if len(names) > 0 {
		var selected []*template.Capability
		seen := map[string]bool{}
		for _, name := range names {
			if !manifest.HasCapability(name) {
				if manifest.HasModule(name) || manifest.HasCommand(name) {
					return nil, nil, fmt.Errorf("%q is a business module or command group: weld writes those once and never regenerates them, so it is not upgradable", name)
				}
				return nil, nil, fmt.Errorf("capability %q is not installed in this project", name)
			}
			if seen[name] {
				continue
			}
			seen[name] = true
			capability, err := catalog.Get(name)
			if err != nil {
				return nil, nil, err
			}
			selected = append(selected, capability)
		}
		return selected, nil, nil
	}

	var recorded []string
	if manifest.Base.Name != "" {
		recorded = append(recorded, manifest.Base.Name)
	}
	for _, capability := range manifest.Capabilities {
		recorded = append(recorded, capability.Name)
	}

	var installed []*template.Capability
	var unknown []string
	for _, name := range recorded {
		capability, err := catalog.Get(name)
		if err != nil {
			unknown = append(unknown, name)
			continue
		}
		installed = append(installed, capability)
	}
	return installed, unknown, nil
}

// patchedPaths returns every path an installed capability extends through a
// marker patch.
//
// Such a file is shared: several capabilities write into its regions, so "the
// files recorded under this capability" is not the same as "the files this
// capability ships". Treating a shared file as a retired payload file would let
// --prune delete go.mod, the Makefile or config.yml.
func patchedPaths(catalog *template.Catalog, present map[string]bool) map[string]bool {
	shared := map[string]bool{}
	for name := range present {
		capability, err := catalog.Get(name)
		if err != nil {
			continue
		}
		for _, patch := range capability.Patches {
			shared[patch.Path] = true
		}
	}
	return shared
}

// pathShippers maps every installed capability payload path to the capability
// that ships it. It is how an upgrade knows a declared path really belongs to
// the capability it is upgrading, rather than to whichever capability patched it
// last — the manifest's recorded owner of a shared file is a historical accident
// that must not veto the update.
func pathShippers(catalog *template.Catalog, present map[string]bool) map[string]string {
	shippers := map[string]string{}
	names, err := catalog.Names()
	if err != nil {
		return shippers
	}
	for _, name := range names {
		if !present[name] {
			continue
		}
		capability, err := catalog.Get(name)
		if err != nil {
			continue
		}
		for _, path := range capability.PayloadPaths() {
			if _, seen := shippers[path]; !seen {
				shippers[path] = capability.Name
			}
		}
	}
	return shippers
}

// upgradeCapability classifies every file a capability's newer payload declares
// against what the manifest recorded and what is on disk, planning the writes
// that are safe and reporting the rest. It returns the paths it plans. shared is
// the set of marker-patched paths a retired-file pass must leave alone.
func (p *planner) upgradeCapability(capability *template.Capability, prune bool, shared map[string]bool, shippers map[string]string) ([]string, []Advisory, error) {
	root := p.req.Dir
	vars := template.Vars{Name: p.manifest.Name, Module: p.manifest.Module, Version: capability.Version}

	declared := map[string]bool{}
	var planned []string
	var advisories []Advisory

	// Every path the capability ships counts as declared, including its generated
	// DI sources, so the retired-file pass below cannot mistake internal/di/di.go
	// for a file the capability stopped shipping.
	for _, path := range capability.PayloadPaths() {
		declared[path] = true
	}

	for _, file := range capability.Files {
		if !entryApplies(p.present, file.When, file.WhenAbsent) {
			continue
		}
		if file.Path == goModPath || file.Path == goSumPath {
			// The toolchain owns the module files: `go mod tidy`, every build and
			// `go get` rewrite them, and a project legitimately keeps its own
			// requirements outside the weld:deps region. A payload upgrade must not
			// rewrite them.
			continue
		}
		if owner, ok := shippers[file.Path]; ok && owner != capability.Name {
			// Two payloads declare one path. That is a template bug, and resolving
			// it by letting "whichever capability is upgraded last" win would make
			// the project's content depend on the order.
			continue
		}
		target, err := p.payloadTarget(capability, file)
		if err != nil {
			return nil, nil, err
		}
		// The project path a versioned migration was installed under is declared
		// too, so the retired-file pass below never mistakes it for a file the
		// payload stopped shipping.
		declared[target] = true
		content, err := capability.ReadFile(file)
		if err != nil {
			return nil, nil, err
		}
		rendered := template.Render(content, vars)

		if current, ok := p.planned[target]; ok {
			if bytes.Equal(current, rendered) {
				continue
			}
			return nil, nil, &project.ConflictError{Path: target, Reason: "planned by another capability"}
		}

		onDisk, readErr := os.ReadFile(filepath.Join(root, target))
		exists := readErr == nil
		if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
			return nil, nil, readErr
		}
		entry, recorded := p.manifest.FileEntry(target)

		role := capability.RoleOf(target)
		if role == project.RolePayload && shared[target] {
			role = project.RolePatched
		}

		candidate, advisory, err := p.upgradeCandidate(target, capability.Name, role, rendered, onDisk, exists, entry, recorded)
		if err != nil {
			return nil, nil, err
		}
		if advisory != nil {
			advisories = append(advisories, *advisory)
			continue
		}
		if candidate == nil {
			continue
		}
		if !exists {
			if err := p.planNewFile(target, capability.Name, file.Source, role, candidate); err != nil {
				return nil, nil, err
			}
		} else {
			p.planManagedOverwrite(target, capability.Name, file.Source, role, candidate)
		}
		planned = append(planned, target)
	}

	// Files the manifest records under this capability that the newer payload no
	// longer declares. A shared file is not a payload file however the manifest
	// labels it, a seam belongs to the project now, and a generated file is the
	// generator's business, so none of them may be pruned.
	for _, entry := range p.manifest.ManagedFiles(capability.Name) {
		if declared[entry.Path] || shared[entry.Path] || p.seamElsewhere(entry.Path) {
			continue
		}
		switch entry.Role {
		case project.RoleSeam, project.RolePatched, project.RoleGenerated:
			continue
		}
		onDisk, err := os.ReadFile(filepath.Join(root, entry.Path))
		switch {
		case errors.Is(err, os.ErrNotExist):
			// Both sides agree the file is gone: drop the stale record.
			p.planManagedDelete(entry.Path)
		case err != nil:
			return nil, nil, err
		case project.HashContent(onDisk) != entry.SHA256:
			advisories = append(advisories, Advisory{
				Path:       entry.Path,
				Capability: capability.Name,
				Reason:     "retired",
				Diff:       project.UnifiedDiff(entry.Path, onDisk, nil),
			})
		case prune:
			p.planManagedDelete(entry.Path)
			planned = append(planned, entry.Path)
		default:
			p.result.Notes = append(p.result.Notes, fmt.Sprintf(
				"%s no longer ships %s; re-run with --prune to delete it", capability.Name, entry.Path))
		}
	}
	return planned, advisories, nil
}

// upgradeCandidate decides what an upgrade may write for one payload file.
//
// It returns the content to write (nil for nothing to do) or an advisory
// explaining why weld will not write it. rendered is the newer payload, and for
// a shared or project-owned file the candidate is the newer skeleton with the
// live marker regions spliced in.
func (p *planner) upgradeCandidate(path, capability string, role project.Role, rendered, onDisk []byte, exists bool, entry project.ManagedFile, recorded bool) ([]byte, *Advisory, error) {
	candidate := rendered
	if exists && (role == project.RoleSeam || role == project.RolePatched) {
		spliced, orphans, err := project.SpliceMarkerRegions(rendered, onDisk)
		if err != nil {
			return nil, nil, &project.ConflictError{Path: path, Reason: err.Error()}
		}
		if len(orphans) > 0 {
			return nil, &Advisory{
				Path:       path,
				Capability: capability,
				Reason:     "orphaned-region",
				Detail: fmt.Sprintf("the newer payload no longer declares the marker region(s) %s, which hold live content, so weld did not splice the file",
					strings.Join(orphans, ", ")),
			}, nil
		}
		candidate = spliced
	}

	switch role {
	case project.RoleGenerated:
		// The Loom regeneration owns these; nothing to do here.
		return nil, nil, nil

	case project.RoleSeam:
		if !recorded {
			// A project file weld never wrote: leave it alone, silently.
			return nil, nil, nil
		}
		if !exists {
			return nil, &Advisory{
				Path:       path,
				Capability: capability,
				Reason:     "missing",
				Diff:       project.UnifiedDiff(path, nil, candidate),
			}, nil
		}
		if seamUnchanged(entry, rendered, onDisk, candidate) {
			return nil, nil, nil
		}
		return nil, &Advisory{
			Path:       path,
			Capability: capability,
			Reason:     "seam",
			Detail:     "weld wrote this project-owned file once and never rewrites it; the diff below is your file -> the version this weld ships, so fold the change in yourself",
			Diff:       project.UnifiedDiff(path, onDisk, candidate),
		}, nil

	case project.RolePatched:
		if !recorded {
			if !exists {
				return candidate, nil, nil
			}
			return nil, nil, &project.ConflictError{Path: path, Reason: "unmanaged file already exists"}
		}
		if !exists {
			return nil, &Advisory{
				Path:       path,
				Capability: capability,
				Reason:     "missing",
				Diff:       project.UnifiedDiff(path, nil, candidate),
			}, nil
		}
		if bytes.Equal(onDisk, candidate) {
			return nil, nil, nil
		}
		// A shared file's regions are where every capability and the project write,
		// so an edit inside one is expected and must not block the skeleton update.
		// Outside them weld owns the file, and a recorded baseline still matching
		// the live file is what proves the project did not add anything there.
		if entry.Baseline && project.HashContent(onDisk) != entry.SHA256 {
			return nil, &Advisory{
				Path:       path,
				Capability: capability,
				Reason:     "modified",
				Diff:       project.UnifiedDiff(path, onDisk, candidate),
			}, nil
		}
		if project.HashContent(candidate) == entry.SHA256 {
			return nil, nil, nil
		}
		return candidate, nil, nil

	default:
		if !recorded {
			if !exists {
				return candidate, nil, nil
			}
			return nil, nil, &project.ConflictError{Path: path, Reason: "unmanaged file already exists"}
		}
		if !exists {
			return nil, &Advisory{
				Path:       path,
				Capability: capability,
				Reason:     "missing",
				Diff:       project.UnifiedDiff(path, nil, candidate),
			}, nil
		}
		if project.HashContent(onDisk) != entry.SHA256 {
			return nil, &Advisory{
				Path:       path,
				Capability: capability,
				Reason:     "modified",
				Diff:       project.UnifiedDiff(path, onDisk, candidate),
			}, nil
		}
		if project.HashContent(candidate) == entry.SHA256 || bytes.Equal(onDisk, candidate) {
			// The shipped version has not moved since weld wrote the file.
			return nil, nil, nil
		}
		return candidate, nil, nil
	}
}

// seamUnchanged reports whether a project-owned file needs no attention.
//
// A trusted baseline answers that exactly: the shipped version is what weld
// wrote. A legacy manifest cannot, so weld asks the weaker question the evidence
// supports — does the payload's skeleton already appear in the project's file? A
// project that added its own middleware or ignore rule outside a marker region is
// not a payload change, and reporting one on every upgrade would be noise.
func seamUnchanged(entry project.ManagedFile, rendered, onDisk, candidate []byte) bool {
	if entry.Baseline {
		return project.HashContent(candidate) == entry.SHA256
	}
	if bytes.Equal(onDisk, candidate) {
		return true
	}
	return project.SkeletonCoveredBy(rendered, onDisk)
}

// roleOf returns the role of a recorded file: what the manifest recorded, or, for
// a manifest written before roles existed, what the capability that recorded the
// file declares for it today.
func roleOf(catalog *template.Catalog, entry project.ManagedFile) project.Role {
	if entry.Role != "" {
		return entry.Role
	}
	capability, err := catalog.Get(entry.Capability)
	if err != nil {
		return project.RolePayload
	}
	return capability.RoleOf(entry.Path)
}

// seamElsewhere reports whether a path any installed capability ships is a
// project-owned seam, so the retired-file pass leaves it alone even when the
// manifest recorded it under the capability being upgraded.
func (p *planner) seamElsewhere(target string) bool {
	names, err := p.req.Catalog.Names()
	if err != nil {
		return false
	}
	for _, name := range names {
		if !p.present[name] {
			continue
		}
		capability, err := p.req.Catalog.Get(name)
		if err != nil {
			continue
		}
		if capability.RoleOf(target) == project.RoleSeam {
			return true
		}
	}
	return false
}

// payloadTarget returns the project path an upgrade writes a payload file to.
//
// A migration keeps the path it was installed with: renaming a migration that may
// already have been applied would corrupt the project. Only a migration that is
// not in the project yet gets a freshly allocated version.
func (p *planner) payloadTarget(capability *template.Capability, file template.File) (string, error) {
	if !file.Versioned {
		return file.Path, nil
	}
	if target, ok, err := p.installedMigration(capability, file); err != nil || ok {
		return target, err
	}
	return p.payloadPath(capability.Name, file)
}

// installedMigration finds the migration this project already holds for a
// versioned payload file.
//
// The manifest's recorded source is authoritative. A manifest written before
// sources were recorded is matched by the payload's file name and goose version
// prefix, which also covers a project that installed the capability when its
// migration still carried a fixed number. An ambiguous match is a conflict rather
// than a guess about which of two migrations is the payload's.
func (p *planner) installedMigration(capability *template.Capability, file template.File) (string, bool, error) {
	if entry, ok := p.manifest.FileEntryForSource(capability.Name, file.Source); ok {
		return entry.Path, true, nil
	}
	var matches []string
	for _, entry := range p.manifest.ManagedFiles(capability.Name) {
		if file.ShipsPath(entry.Path) {
			matches = append(matches, entry.Path)
		}
	}
	switch len(matches) {
	case 0:
		return "", false, nil
	case 1:
		return matches[0], true, nil
	default:
		sort.Strings(matches)
		return "", false, &project.ConflictError{
			Path:   matches[0],
			Reason: fmt.Sprintf("capability %q recorded %d migrations named %s; weld cannot tell which one it installed", capability.Name, len(matches), path.Base(file.Path)),
		}
	}
}

// planManagedOverwrite rewrites a file weld owns whose content still matches the
// manifest, so the write cannot lose a project edit.
func (p *planner) planManagedOverwrite(path, capability, source string, role project.Role, content []byte) {
	p.planned[path] = content
	p.upsert(project.Operation{Path: path, Content: content, Overwrite: true})
	p.manifest.SetPayloadFile(path, capability, source, role, content)
}

// planManagedDelete removes a file weld owns and drops its manifest record. The
// caller has already established that the file is unmodified or already gone.
func (p *planner) planManagedDelete(path string) {
	delete(p.planned, path)
	p.upsert(project.Operation{Path: path, Delete: true, Overwrite: true})
	p.manifest.RemoveFile(path)
}

// driftNotes describes the managed files of an already-current capability that no
// longer match the manifest, so an upgrade reports drift it deliberately does not
// repair. Project-owned seams are left out: the project is supposed to edit them,
// so reporting that edit as drift on every run would be noise.
func driftNotes(catalog *template.Catalog, root string, manifest *project.Manifest, capability string) []string {
	var drifted []project.Drift
	for _, item := range manifest.Drift(root) {
		entry, ok := manifest.FileEntry(item.Path)
		if !ok || entry.Capability != capability {
			continue
		}
		switch roleOf(catalog, entry) {
		case project.RoleSeam, project.RoleGenerated:
			continue
		}
		drifted = append(drifted, item)
	}
	if len(drifted) == 0 {
		return nil
	}
	notes := []string{fmt.Sprintf("%s is up to date; %d managed file(s) changed since install:", capability, len(drifted))}
	for _, item := range drifted {
		notes = append(notes, fmt.Sprintf("  %s (%s)", item.Path, item.Reason))
	}
	return notes
}
