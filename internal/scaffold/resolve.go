package scaffold

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/Xwudao/weld/internal/project"
	"github.com/Xwudao/weld/internal/semver"
)

// ResolveResult is a planned `weld upgrade resolve`.
type ResolveResult struct {
	*Result
	// Resolved lists the pending changes the plan accepts.
	Resolved []project.PendingChange
}

// Resolve plans accepting pending upgrade changes: the project has folded weld's
// change into a file (or decided not to), and the record should say so.
//
// Accepting a seam or a modified payload file moves its recorded baseline to the
// version this weld ships, so the next upgrade diffs from there instead of
// reporting the same change again. When the project file differs from that
// version — the usual case for a seam the project customised — the accepted
// version is kept under BaselineDir, because it is the base the next upgrade's
// three-way view needs and nothing else holds it. A retired file the project
// keeps becomes its own and leaves the manifest; a deleted file stays recorded,
// so it is still never resurrected.
//
// A file that still carries merge conflict markers is refused. A pending change
// whose capability is behind this weld is refused too: `weld upgrade` has to move
// the capability first, or the accepted version would not be what this weld
// ships. paths selects pending changes by project path; all selects every one.
func Resolve(req Request, paths []string, all bool) (*ResolveResult, error) {
	manifest, err := project.Load(req.Dir)
	if err != nil {
		return nil, err
	}
	selected, err := selectPending(manifest, paths, all)
	if err != nil {
		return nil, err
	}
	result := &Result{Dir: req.Dir}
	resolved := &ResolveResult{Result: result}
	if len(selected) == 0 {
		result.Notes = append(result.Notes, "No pending upgrade changes to resolve.")
		return resolved, nil
	}

	p := newPlanner(req, manifest, result)
	shared := patchedPaths(req.Catalog, p.present)
	shippers := pathShippers(req.Catalog, p.present)
	for _, change := range selected {
		capability, err := req.Catalog.Get(change.Capability)
		if err != nil {
			// The capability left the catalog; there is nothing left to accept.
			manifest.RemovePending(change.Path, change.Marker)
			resolved.Resolved = append(resolved.Resolved, change)
			continue
		}
		if newer, err := semver.IsNewer(capability.Version, manifest.CapabilityVersion(capability.Name)); err != nil {
			return nil, fmt.Errorf("capability %q: %w", capability.Name, err)
		} else if newer {
			return nil, fmt.Errorf("%s: capability %q is behind this weld; run weld upgrade before resolving its changes", change.Path, capability.Name)
		}

		switch change.Reason {
		case ReasonSeam, ReasonModified:
			advisory, err := p.pendingAdvisory(capability, change, shared, shippers)
			if err != nil {
				return nil, err
			}
			if advisory.Reason == ReasonOrphanedRegion {
				return nil, fmt.Errorf("%s: %s", change.Path, advisory.Detail)
			}
			if advisory.Target != "" {
				if err := p.acceptTarget(change.Path, []byte(advisory.Target), advisory.Role); err != nil {
					return nil, err
				}
			}
		case ReasonRetired:
			// The project keeps the file as its own (or already deleted it).
			if manifest.Owns(change.Path) {
				manifest.RemoveFile(change.Path)
			}
		}
		manifest.RemovePending(change.Path, change.Marker)
		resolved.Resolved = append(resolved.Resolved, change)
	}
	if err := p.writeManifest(); err != nil {
		return nil, err
	}
	return resolved, nil
}

// acceptTarget records target as the baseline of a project file and keeps a copy
// of it when the file differs. A file that still holds conflict markers has not
// been merged yet, so it is refused rather than recorded as done.
func (p *planner) acceptTarget(file string, target []byte, role project.Role) error {
	onDisk, err := os.ReadFile(filepath.Join(p.req.Dir, file))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if project.HasConflictMarkers(onDisk) {
		return &project.ConflictError{Path: file, Reason: "the file still contains merge conflict markers; finish the merge before resolving it"}
	}
	entry, ok := p.manifest.FileEntry(file)
	if !ok {
		return nil
	}
	if entry.Role != "" {
		role = entry.Role
	}
	p.manifest.SetPayloadFile(file, entry.Capability, "", role, target)
	operation, needed, err := baselineOperation(p.req.Dir, file, target, onDisk)
	if err != nil {
		return err
	}
	if needed {
		p.upsert(operation)
	}
	return nil
}

// selectPending returns the pending changes a resolve names. A path with no
// pending change is an error, so a typo is not silently "resolved".
func selectPending(manifest *project.Manifest, paths []string, all bool) ([]project.PendingChange, error) {
	if all {
		return append([]project.PendingChange(nil), manifest.Pending...), nil
	}
	if len(paths) == 0 {
		return nil, errors.New("name the files to resolve, or pass --all")
	}
	var selected []project.PendingChange
	seen := map[project.PendingChange]bool{}
	for _, requested := range paths {
		clean := path.Clean(filepath.ToSlash(requested))
		found := false
		for _, change := range manifest.Pending {
			if change.Path != clean {
				continue
			}
			found = true
			if !seen[change] {
				seen[change] = true
				selected = append(selected, change)
			}
		}
		if !found {
			var pending []string
			for _, change := range manifest.Pending {
				pending = append(pending, change.Path)
			}
			hint := "there are none"
			if len(pending) > 0 {
				hint = "pending: " + strings.Join(pending, ", ")
			}
			return nil, fmt.Errorf("%s has no pending upgrade change (%s)", requested, hint)
		}
	}
	return selected, nil
}
