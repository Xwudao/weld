package scaffold

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Xwudao/weld/internal/project"
)

// BaselineDir holds the content weld last accepted for a file whose project
// version differs from it, keyed by the file's project path.
//
// `weld upgrade resolve` moves a file's recorded baseline to the version this
// weld ships, but a project-owned seam rarely equals that version byte for byte:
// the project folded the change into its own edits. The next upgrade needs the
// accepted version to compute what weld changed since, and nothing else holds
// it — the project's history only has the project's files. Weld writes the
// copy only for such a file, so a project that never diverged has no directory.
const BaselineDir = ".weld/baseline"

// historyDepth bounds how many commits a base lookup reads per file, so an
// advisory on a long-lived file stays cheap.
const historyDepth = 64

// History returns the earlier committed contents of a project file, newest
// first. It is how an upgrade recovers the content weld once wrote when the
// manifest only kept its hash. A project that is not under version control has
// no history, which is not an error.
type History func(dir, file string) ([][]byte, error)

// GitHistory reads a file's committed versions from the git repository the
// project lives in. A missing git binary, a directory outside a repository and a
// file with no commits all yield no history.
func GitHistory(dir, file string) ([][]byte, error) {
	if _, err := exec.LookPath("git"); err != nil {
		return nil, nil
	}
	log := exec.Command("git", "log", "--format=%H", "-n", strconv.Itoa(historyDepth), "--", file)
	log.Dir = dir
	out, err := log.Output()
	if err != nil {
		return nil, nil
	}
	var versions [][]byte
	for _, commit := range strings.Fields(string(out)) {
		// "./" makes the path relative to dir rather than to the repository root,
		// so a project nested inside a larger repository resolves correctly.
		show := exec.Command("git", "show", commit+":./"+file)
		show.Dir = dir
		content, err := show.Output()
		if err != nil {
			// The commit deleted the file; an older one may still hold it.
			continue
		}
		versions = append(versions, content)
	}
	return versions, nil
}

// Base sources an advisory reports, so a reader knows how far to trust the
// three-way view.
const (
	// BaseFromBaseline is the copy `weld upgrade resolve` kept in BaselineDir.
	BaseFromBaseline = "baseline"
	// BaseFromGit is a committed version whose hash matches the manifest.
	BaseFromGit = "git"
	// BaseFromDisk is the project file itself, which still matches the
	// manifest, so it is exactly what weld wrote.
	BaseFromDisk = "disk"
)

// recordedContent finds the content the manifest's hash for entry describes:
// the file itself when it still matches, the copy resolve kept, or the newest
// committed version with the same hash. It returns nil when none is available,
// which leaves an advisory with the two-way diff alone.
func recordedContent(root string, entry project.ManagedFile, onDisk []byte, exists bool, history History) ([]byte, string) {
	if entry.SHA256 == "" {
		return nil, ""
	}
	if exists && project.HashContent(onDisk) == entry.SHA256 {
		return onDisk, BaseFromDisk
	}
	if kept, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path.Join(BaselineDir, entry.Path)))); err == nil &&
		project.HashContent(kept) == entry.SHA256 {
		return kept, BaseFromBaseline
	}
	if history == nil {
		return nil, ""
	}
	versions, err := history(root, entry.Path)
	if err != nil {
		return nil, ""
	}
	for _, version := range versions {
		if project.HashContent(version) == entry.SHA256 {
			return version, BaseFromGit
		}
	}
	return nil, ""
}

// baselineOperation plans the BaselineDir copy for a file whose recorded
// baseline becomes accepted: the copy is written when the project file differs
// from it and removed when the file equals it (the file itself then proves the
// baseline, and a stale copy would only mislead).
func baselineOperation(root, file string, accepted, onDisk []byte) (project.Operation, bool, error) {
	kept := path.Join(BaselineDir, file)
	existing, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(kept)))
	exists := err == nil
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return project.Operation{}, false, err
	}
	if bytes.Equal(onDisk, accepted) {
		if !exists {
			return project.Operation{}, false, nil
		}
		return project.Operation{Path: kept, Delete: true, Overwrite: true}, true, nil
	}
	if exists && bytes.Equal(existing, accepted) {
		return project.Operation{}, false, nil
	}
	return project.Operation{Path: kept, Content: accepted, Overwrite: exists}, true, nil
}
