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

// Advisory reasons. Each is a change a newer payload implies that weld refuses
// to write on its own.
const (
	// ReasonSeam: the file is project-owned and weld never rewrites it; the
	// shipped version changed, so the project folds it in.
	ReasonSeam = "seam"
	// ReasonModified: a payload file the project changed since weld wrote it.
	ReasonModified = "modified"
	// ReasonMissing: a payload file the project deleted, never resurrected.
	ReasonMissing = "missing"
	// ReasonRetired: the payload no longer ships the file, which the project
	// also changed, so it is not deleted either.
	ReasonRetired = "retired"
	// ReasonOrphanedRegion: the newer payload no longer declares a marker region
	// the live file fills, so splicing would drop live content.
	ReasonOrphanedRegion = "orphaned-region"
	// ReasonPatch: a marker region lacks lines of the snippet the newer payload
	// patches into it. Snippets are never re-applied, so the project adds them.
	ReasonPatch = "patch"
)

// Advisory is a change a newer capability payload implies for a file `weld
// upgrade` refuses to write on its own, either because the file belongs to the
// project or because the project changed it.
//
// It carries everything needed to fold the change in without losing an edit:
// the content this weld would generate (Target), and — when the content weld
// last wrote can be recovered — what weld changed since (UpstreamDiff), what the
// project changed (LocalDiff) and a three-way merge of both (Merged). An
// advisory is recorded in weld.json until `weld upgrade resolve` accepts it, so
// it is reported on every run rather than once.
type Advisory struct {
	Path       string `json:"path"`
	Capability string `json:"capability"`
	// Role is the file's ownership role.
	Role project.Role `json:"role,omitempty"`
	// Reason is one of the Reason* constants.
	Reason string `json:"reason"`
	// Marker names the marker region of a ReasonPatch advisory.
	Marker string `json:"marker,omitempty"`
	// From and To are the capability versions the change spans: From is the
	// version the project held when the change was first reported.
	From string `json:"from,omitempty"`
	To   string `json:"to,omitempty"`
	// Pending reports that an earlier upgrade already reported the change and
	// the project has not resolved it yet.
	Pending bool `json:"pending,omitempty"`
	// Settled reports that the project file already matches Target (or, for a
	// retired file, is gone), so resolving it only records the fact.
	Settled bool `json:"settled,omitempty"`
	// Detail explains the advisory in a sentence, including one with no diff
	// (an orphaned region, a missing snippet line).
	Detail string `json:"detail,omitempty"`
	// Diff is the unified diff from the project's file to Target. It mixes the
	// project's own edits (as removals) with weld's change, so prefer
	// UpstreamDiff when it is present.
	Diff string `json:"diff,omitempty"`
	// Base names where the content weld last wrote came from (BaseFrom*), or is
	// empty when it could not be recovered and only Diff is available.
	Base string `json:"base,omitempty"`
	// BaseTrusted is false when the manifest predates trustworthy baselines, so
	// the recovered base may already contain project edits.
	BaseTrusted bool `json:"baseTrusted,omitempty"`
	// UpstreamDiff is weld's own change: the recorded base to Target.
	UpstreamDiff string `json:"upstreamDiff,omitempty"`
	// LocalDiff is the project's change: the recorded base to its file.
	LocalDiff string `json:"localDiff,omitempty"`
	// Merged is the three-way merge of the project file with weld's change,
	// conflict markers included. It is advisory: weld never writes it.
	Merged string `json:"merged,omitempty"`
	// Conflicts counts the conflicting blocks in Merged.
	Conflicts int `json:"conflicts,omitempty"`
	// Target is the full content this weld would give the file in a project
	// generated today with the same capabilities (for a seam, with the
	// project's marker regions kept). Empty for a retired file.
	Target string `json:"target,omitempty"`
	// Snippet is the snippet a ReasonPatch advisory's region should contain.
	Snippet string `json:"snippet,omitempty"`
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

// Changelog is the capability changelog an upgrade surfaces: the entries
// between the version the project holds (or held when a still-pending change was
// first reported) and the version this weld ships.
type Changelog struct {
	Capability string            `json:"capability"`
	From       string            `json:"from"`
	To         string            `json:"to"`
	Changes    []template.Change `json:"changes"`
}

// UpgradeResult is a planned `weld upgrade`.
type UpgradeResult struct {
	*Result
	// Upgraded lists the capabilities whose recorded version moved forward.
	Upgraded []CapabilityDelta
	// Advisories lists files weld will not rewrite on its own: the ones this
	// upgrade found and the pending ones earlier upgrades recorded.
	Advisories []Advisory
	// Changelog lists the changelog entries of every capability that moves or
	// still has a pending change, so the reason for a diff travels with it.
	Changelog []Changelog
	// Current lists installed capabilities already at this weld's version.
	Current []string
	// Unknown lists capabilities the manifest records that this weld build no
	// longer ships. They are reported and skipped, never silently forgotten.
	Unknown []string
}

// Behind reports whether any installed capability is older than this weld build.
func (r *UpgradeResult) Behind() bool { return len(r.Upgraded) > 0 }

// UpToDate reports whether the project needs nothing at all: no capability
// moves, nothing is written, and no change is waiting for the project.
func (r *UpgradeResult) UpToDate() bool {
	return len(r.Upgraded) == 0 && len(r.Operations) == 0 && len(r.Advisories) == 0
}

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
// by splicing the live region bodies into the newer skeleton. A region that
// lacks lines of the newer snippet is reported as a ReasonPatch advisory instead.
//
// Every advisory is recorded in the manifest as pending, and pending changes of
// capabilities that are already current are re-reported, so a change handed to
// the project is never forgotten until `weld upgrade resolve` accepts it.
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
			advisories, err := p.pendingAdvisories(capability, shared, shippers)
			if err != nil {
				return nil, err
			}
			if len(advisories) > 0 {
				upgrade.Advisories = append(upgrade.Advisories, advisories...)
				upgrade.addChangelog(capability, earliestFrom(advisories), capability.Version)
			}
			continue
		}
		files, advisories, err := p.upgradeCapability(capability, prune, shared, shippers)
		if err != nil {
			return nil, err
		}
		patches, err := p.patchAdvisories(capability)
		if err != nil {
			return nil, err
		}
		advisories = append(advisories, patches...)
		for i := range advisories {
			p.recordPending(&advisories[i], recorded, capability.Version)
		}
		manifest.SetCapabilityVersion(capability.Name, capability.Version)
		upgrade.Upgraded = append(upgrade.Upgraded, CapabilityDelta{
			Name:  capability.Name,
			From:  recorded,
			To:    capability.Version,
			Files: files,
		})
		upgrade.Advisories = append(upgrade.Advisories, advisories...)
		upgrade.addChangelog(capability, earliestFrom(advisories, recorded), capability.Version)
	}
	if err := p.finish(); err != nil {
		return nil, err
	}
	return upgrade, nil
}

// addChangelog records the changelog entries of capability newer than from. A
// capability whose changelog has nothing in the range adds nothing.
func (r *UpgradeResult) addChangelog(capability *template.Capability, from, to string) {
	changes := capability.ChangesSince(from)
	if len(changes) == 0 {
		return
	}
	r.Changelog = append(r.Changelog, Changelog{Capability: capability.Name, From: from, To: to, Changes: changes})
}

// earliestFrom returns the oldest From among advisories and fallbacks, so a
// changelog covers every version a still-pending change spans.
func earliestFrom(advisories []Advisory, fallbacks ...string) string {
	earliest := ""
	consider := func(version string) {
		if version == "" {
			return
		}
		if earliest == "" {
			earliest = version
			return
		}
		if older, err := semver.IsNewer(earliest, version); err == nil && older {
			earliest = version
		}
	}
	for _, fallback := range fallbacks {
		consider(fallback)
	}
	for _, advisory := range advisories {
		consider(advisory.From)
	}
	return earliest
}

// recordPending stores an advisory in the manifest as a pending change and
// stamps it with the version range it spans. A change already pending keeps the
// version it was first reported at.
func (p *planner) recordPending(advisory *Advisory, from, to string) {
	p.manifest.AddPending(project.PendingChange{
		Path:       advisory.Path,
		Capability: advisory.Capability,
		Reason:     advisory.Reason,
		Marker:     advisory.Marker,
		From:       from,
		To:         to,
	})
	for _, change := range p.manifest.Pending {
		if change.Path == advisory.Path && change.Marker == advisory.Marker {
			advisory.From, advisory.To = change.From, change.To
			if change.From != from {
				advisory.Pending = true
			}
		}
	}
}

// pendingAdvisories re-reports the pending changes of a capability that is
// already current, recomputed against the project as it is now: the diffs show
// what is still left to fold in, and Settled says when nothing is.
func (p *planner) pendingAdvisories(capability *template.Capability, shared map[string]bool, shippers map[string]string) ([]Advisory, error) {
	var advisories []Advisory
	for _, change := range p.manifest.Pending {
		if change.Capability != capability.Name {
			continue
		}
		advisory, err := p.pendingAdvisory(capability, change, shared, shippers)
		if err != nil {
			return nil, err
		}
		advisory.Pending = true
		advisory.From, advisory.To = change.From, change.To
		advisories = append(advisories, advisory)
	}
	return advisories, nil
}

// pendingAdvisory recomputes one pending change. A change the newer payload
// can no longer describe (the file left the payload, the region was settled) is
// reported as settled, so resolving it clears the record.
func (p *planner) pendingAdvisory(capability *template.Capability, change project.PendingChange, shared map[string]bool, shippers map[string]string) (Advisory, error) {
	settled := Advisory{Path: change.Path, Capability: change.Capability, Reason: change.Reason, Marker: change.Marker, Settled: true}
	switch change.Reason {
	case ReasonPatch:
		for _, patch := range capability.Patches {
			if patch.Path != change.Path || patch.Marker != change.Marker || !entryApplies(p.present, patch.When, patch.WhenAbsent) {
				continue
			}
			advisory, err := p.patchAdvisory(capability, patch)
			if err != nil {
				return Advisory{}, err
			}
			if advisory != nil {
				return *advisory, nil
			}
		}
		settled.Detail = "every line of the snippet is in the region now"
		return settled, nil

	case ReasonRetired:
		onDisk, err := os.ReadFile(filepath.Join(p.req.Dir, change.Path))
		if errors.Is(err, os.ErrNotExist) {
			settled.Detail = "the retired file is gone"
			return settled, nil
		}
		if err != nil {
			return Advisory{}, err
		}
		return Advisory{
			Path:       change.Path,
			Capability: change.Capability,
			Reason:     ReasonRetired,
			Detail:     retiredDetail,
			Diff:       project.UnifiedDiff(change.Path, onDisk, nil),
		}, nil
	}

	vars := template.Vars{Name: p.manifest.Name, Module: p.manifest.Module, Version: capability.Version}
	for _, file := range capability.Files {
		if !entryApplies(p.present, file.When, file.WhenAbsent) {
			continue
		}
		target, err := p.payloadTarget(capability, file)
		if err != nil {
			return Advisory{}, err
		}
		if target != change.Path {
			continue
		}
		assessed, err := p.assess(capability, file, target, vars, shared)
		if err != nil {
			return Advisory{}, err
		}
		if assessed.advisory != nil {
			return *assessed.advisory, nil
		}
		settled.Role = assessed.role
		settled.Detail = "the file no longer differs from what this weld ships"
		if assessed.exists && bytes.Equal(assessed.onDisk, assessed.target) {
			// Resolving records this as the baseline, so the file stops reading
			// as a project edit.
			settled.Target = string(assessed.target)
		}
		return settled, nil
	}
	settled.Detail = "this weld no longer ships the file"
	return settled, nil
}

// retiredDetail explains a retired-file advisory.
const retiredDetail = "this weld no longer ships the file and the project changed it, so weld did not delete it; delete it or keep it as your own, then run weld upgrade resolve"

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

		if current, ok := p.planned[target]; ok {
			content, err := capability.ReadFile(file)
			if err != nil {
				return nil, nil, err
			}
			if bytes.Equal(current, template.Render(content, vars)) {
				continue
			}
			return nil, nil, &project.ConflictError{Path: target, Reason: "planned by another capability"}
		}

		assessed, err := p.assess(capability, file, target, vars, shared)
		if err != nil {
			return nil, nil, err
		}
		if assessed.advisory != nil {
			advisories = append(advisories, *assessed.advisory)
			continue
		}
		if assessed.candidate == nil {
			continue
		}
		if !assessed.exists {
			if err := p.planNewFile(target, capability.Name, file.Source, assessed.role, assessed.candidate); err != nil {
				return nil, nil, err
			}
		} else {
			p.planManagedOverwrite(target, capability.Name, file.Source, assessed.role, assessed.candidate)
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
				Role:       roleOf(p.req.Catalog, entry),
				Reason:     ReasonRetired,
				Detail:     retiredDetail,
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

// assessment is what an upgrade may do with one payload file: write candidate,
// hand over advisory, or nothing.
type assessment struct {
	role      project.Role
	exists    bool
	onDisk    []byte
	candidate []byte
	advisory  *Advisory
	// target is what this weld gives the file — the payload, with the live
	// marker regions for a seam or shared file — whatever the decision was.
	target []byte
}

// assess renders one payload file at its project path and decides what an
// upgrade may do with it, with any advisory carrying the full three-way view.
func (p *planner) assess(capability *template.Capability, file template.File, target string, vars template.Vars, shared map[string]bool) (assessment, error) {
	content, err := capability.ReadFile(file)
	if err != nil {
		return assessment{}, err
	}
	rendered := template.Render(content, vars)

	onDisk, readErr := os.ReadFile(filepath.Join(p.req.Dir, target))
	exists := readErr == nil
	if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
		return assessment{}, readErr
	}
	entry, recorded := p.manifest.FileEntry(target)

	role := capability.RoleOf(target)
	if role == project.RolePayload && shared[target] {
		role = project.RolePatched
	}

	candidate, advisory, err := p.upgradeCandidate(target, capability.Name, role, rendered, onDisk, exists, entry, recorded)
	if err != nil {
		return assessment{}, err
	}
	if advisory != nil {
		advisory.Role = role
		p.enrich(advisory, entry, recorded, onDisk, exists)
	}
	shipped := rendered
	if exists && (role == project.RoleSeam || role == project.RolePatched) {
		if spliced, orphans, err := project.SpliceMarkerRegions(rendered, onDisk); err == nil && len(orphans) == 0 {
			shipped = spliced
		}
	}
	return assessment{role: role, exists: exists, onDisk: onDisk, candidate: candidate, advisory: advisory, target: shipped}, nil
}

// enrich completes an advisory with the three-way view: it recovers the content
// weld last wrote (from the file itself, the copy resolve kept, or the project's
// history) and derives weld's change, the project's change and their merge.
// Without a recoverable base the advisory keeps its two-way Diff, which is still
// correct, only harder to apply without undoing a project edit.
func (p *planner) enrich(advisory *Advisory, entry project.ManagedFile, recorded bool, onDisk []byte, exists bool) {
	if advisory.Reason == ReasonOrphanedRegion || advisory.Reason == ReasonRetired {
		return
	}
	target := []byte(advisory.Target)
	if exists && bytes.Equal(onDisk, target) {
		advisory.Settled = true
	}
	if !recorded {
		return
	}
	base, source := recordedContent(p.req.Dir, entry, onDisk, exists, p.req.History)
	if base == nil {
		return
	}
	if exists && (advisory.Role == project.RoleSeam || advisory.Role == project.RolePatched) {
		// A region body is the project's (or another capability's) and the target
		// already carries the live one, so the base gets it too: otherwise every
		// line added to a region since weld wrote the file would show up as weld's
		// change.
		if spliced, orphans, err := project.SpliceMarkerRegions(base, onDisk); err == nil && len(orphans) == 0 {
			base = spliced
		}
	}
	advisory.Base = source
	advisory.BaseTrusted = entry.Baseline && !p.manifest.LegacyBaseline
	advisory.UpstreamDiff = changeDiff(advisory.Path, base, target)
	if !exists {
		return
	}
	advisory.LocalDiff = changeDiff(advisory.Path, base, onDisk)
	if merged, ok := project.Merge3(base, onDisk, target); ok {
		advisory.Merged = string(merged.Content)
		advisory.Conflicts = merged.Conflicts
	}
}

// changeDiff is UnifiedDiff that says nothing when nothing changed.
func changeDiff(path string, before, after []byte) string {
	if bytes.Equal(before, after) {
		return ""
	}
	return project.UnifiedDiff(path, before, after)
}

// patchAdvisories reports the marker regions that lack lines of the snippet the
// capability's newer payload patches into them. Snippets are never re-applied —
// a region is the project's to edit — so this is how a new configuration key, a
// new Makefile target or a new ignore rule reaches an existing project.
func (p *planner) patchAdvisories(capability *template.Capability) ([]Advisory, error) {
	var advisories []Advisory
	for _, patch := range capability.Patches {
		if !entryApplies(p.present, patch.When, patch.WhenAbsent) {
			continue
		}
		advisory, err := p.patchAdvisory(capability, patch)
		if err != nil {
			return nil, err
		}
		if advisory != nil {
			advisories = append(advisories, *advisory)
		}
	}
	return advisories, nil
}

// patchAdvisory compares one marker patch's snippet with the live region. The Go
// module files belong to the toolchain, a "replace" patch owns its region
// wholesale, and an absent target (a git-ignored config.yml on a fresh clone) is
// not this check's business, so none of them is reported.
func (p *planner) patchAdvisory(capability *template.Capability, patch template.Patch) (*Advisory, error) {
	if patch.Path == goModPath || patch.Path == goSumPath || patch.Mode == "replace" {
		return nil, nil
	}
	live, ok := p.planned[patch.Path]
	if !ok {
		onDisk, err := os.ReadFile(filepath.Join(p.req.Dir, patch.Path))
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		live = onDisk
	}
	raw, err := capability.ReadPatch(patch)
	if err != nil {
		return nil, err
	}
	vars := template.Vars{Name: p.manifest.Name, Module: p.manifest.Module, Version: capability.Version}
	snippet := template.Render(raw, vars)
	advisory := &Advisory{
		Path:       patch.Path,
		Capability: capability.Name,
		Reason:     ReasonPatch,
		Marker:     patch.Marker,
		Snippet:    string(snippet),
	}
	if entry, ok := p.manifest.FileEntry(patch.Path); ok {
		advisory.Role = roleOf(p.req.Catalog, entry)
	}
	regions, _, err := project.MarkerRegions(live)
	if err != nil {
		advisory.Detail = fmt.Sprintf("the file's marker regions cannot be read (%v); restore the weld:%s region and add the snippet inside it", err, patch.Marker)
		return advisory, nil
	}
	body, found := regions[patch.Marker]
	if !found {
		advisory.Detail = fmt.Sprintf("the file has no weld:%s marker region; restore it and add the snippet inside it", patch.Marker)
		return advisory, nil
	}
	missing := missingSnippetLines(patch.Path, snippet, body)
	if len(missing) == 0 {
		return nil, nil
	}
	advisory.Detail = fmt.Sprintf("the weld:%s region lacks %d line(s) of the snippet %s %s ships; add them inside the region where they belong:\n%s",
		patch.Marker, len(missing), capability.Name, capability.Version, strings.Join(missing, "\n"))
	return advisory, nil
}

// missingSnippetLines returns the non-blank snippet lines the region does not
// contain, compared without indentation. A YAML line is compared by its key,
// because a project sets its own values in config.yml and that is not a
// missing line.
func missingSnippetLines(file string, snippet []byte, region []string) []string {
	yaml := strings.HasSuffix(file, ".yml") || strings.HasSuffix(file, ".yaml")
	normalize := func(line string) string {
		line = strings.TrimSpace(line)
		if yaml && !strings.HasPrefix(line, "#") {
			if key, _, ok := strings.Cut(line, ":"); ok {
				return key + ":"
			}
		}
		return line
	}
	have := map[string]bool{}
	for _, line := range region {
		have[normalize(line)] = true
	}
	var missing []string
	for _, line := range strings.Split(string(snippet), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if !have[normalize(line)] {
			missing = append(missing, line)
		}
	}
	return missing
}

// upgradeCandidate decides what an upgrade may write for one payload file.
//
// It returns the content to write (nil for nothing to do) or an advisory
// explaining why weld will not write it. rendered is the newer payload, and for
// a shared or project-owned file the candidate is the newer skeleton with the
// live marker regions spliced in. Every advisory but an orphaned region carries
// the candidate as its Target.
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
				Reason:     ReasonOrphanedRegion,
				Detail: fmt.Sprintf("the newer payload no longer declares the marker region(s) %s, which hold live content, so weld did not splice the file; move that content out of the region(s), then run weld upgrade again",
					strings.Join(orphans, ", ")),
			}, nil
		}
		candidate = spliced
	}
	missing := func() *Advisory {
		return &Advisory{
			Path:       path,
			Capability: capability,
			Reason:     ReasonMissing,
			Detail:     "the project deleted this file and weld never resurrects one; recreate it from target if the project needs it, then run weld upgrade resolve",
			Diff:       project.UnifiedDiff(path, nil, candidate),
			Target:     string(candidate),
		}
	}
	modified := func() *Advisory {
		return &Advisory{
			Path:       path,
			Capability: capability,
			Reason:     ReasonModified,
			Detail:     "the project changed this file since weld wrote it, so weld did not overwrite it; fold weld's change (upstreamDiff) into it, then run weld upgrade resolve",
			Diff:       project.UnifiedDiff(path, onDisk, candidate),
			Target:     string(candidate),
		}
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
			return nil, missing(), nil
		}
		if seamUnchanged(entry, rendered, onDisk, candidate) {
			return nil, nil, nil
		}
		return nil, &Advisory{
			Path:       path,
			Capability: capability,
			Reason:     ReasonSeam,
			Detail:     "weld wrote this project-owned file once and never rewrites it; fold weld's change (upstreamDiff, or the diff from your file to target) into it yourself, then run weld upgrade resolve",
			Diff:       project.UnifiedDiff(path, onDisk, candidate),
			Target:     string(candidate),
		}, nil

	case project.RolePatched:
		if !recorded {
			if !exists {
				return candidate, nil, nil
			}
			return nil, nil, &project.ConflictError{Path: path, Reason: "unmanaged file already exists"}
		}
		if !exists {
			return nil, missing(), nil
		}
		if bytes.Equal(onDisk, candidate) {
			return nil, nil, nil
		}
		// A shared file's regions are where every capability and the project write,
		// so an edit inside one is expected and must not block the skeleton update.
		// Outside them weld owns the file, and a recorded baseline still matching
		// the live file is what proves the project did not add anything there.
		if entry.Baseline && project.HashContent(onDisk) != entry.SHA256 {
			if project.HashContent(candidate) == entry.SHA256 {
				// The shipped version has not moved: the difference is the project's.
				return nil, nil, nil
			}
			return nil, modified(), nil
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
			return nil, missing(), nil
		}
		if project.HashContent(candidate) == entry.SHA256 || bytes.Equal(onDisk, candidate) {
			// The shipped version has not moved since weld wrote the file (or the
			// project already has it), so there is nothing to bring forward; a
			// project edit stays the project's and is reported as drift instead.
			return nil, nil, nil
		}
		if project.HashContent(onDisk) != entry.SHA256 {
			return nil, modified(), nil
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
