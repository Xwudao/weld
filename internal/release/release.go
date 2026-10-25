// Package release cuts a local release of a generated project: it checks the
// repository, writes release notes from the commits since the previous version
// tag, creates an annotated tag, builds the binaries and writes their SHA-256
// checksums.
//
// It never pushes and never publishes. A tag is local until the caller runs the
// printed `git push`, which is what keeps a release reversible until the moment
// someone shares it. The tag is created before the build, so a build.ldflags
// value of ${git_tag} in weld.yml stamps the new version into the binaries, and
// it is deleted again when the build fails.
package release

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/Xwudao/weld/internal/semver"
)

// Git runs git with args inside dir and returns its standard output. It is the
// seam tests replace, so no test needs a real repository.
type Git func(dir string, args ...string) (string, error)

// Build builds the release binaries and returns their paths relative to the
// project directory.
type Build func() ([]string, error)

// Request describes a release.
type Request struct {
	Dir string
	// Version is the version to release, vMAJOR.MINOR.PATCH; a missing leading v
	// is added.
	Version string
	Git     Git
	// Build is nil for a tag-only release (--no-build).
	Build Build
	// ChecksumFile is where the checksums are written, relative to Dir. It is
	// required when Build is set.
	ChecksumFile string
}

// Plan is a release that has been checked and is ready to apply.
type Plan struct {
	req Request
	// Tag is the tag the release creates.
	Tag string
	// Previous is the newest earlier version tag, or "" for a first release.
	Previous string
	// Commit is the commit the tag points at.
	Commit string
	// Notes is the release notes, which are also the annotated tag's message.
	Notes string
}

// Result reports what a release produced.
type Result struct {
	Tag string
	// Binaries are the built binaries, relative to the project directory.
	Binaries []string
	// Checksums is the checksum file, relative to the project directory, or ""
	// for a tag-only release.
	Checksums string
}

// versionPattern is the only shape a release version may take: three numeric
// components. Pre-release suffixes are refused because the rest of weld cannot
// order them.
var versionPattern = regexp.MustCompile(`^v?(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

// NormalizeVersion returns version as a tag (with its leading v) or explains
// why it is not one.
func NormalizeVersion(version string) (string, error) {
	version = strings.TrimSpace(version)
	if !versionPattern.MatchString(version) {
		return "", fmt.Errorf("invalid release version %q: use MAJOR.MINOR.PATCH, for example v1.2.0", version)
	}
	return "v" + strings.TrimPrefix(version, "v"), nil
}

// Prepare checks that a release can be cut and computes its plan. It reads the
// repository but changes nothing, so a dry run is exactly this.
func Prepare(req Request) (*Plan, error) {
	tag, err := NormalizeVersion(req.Version)
	if err != nil {
		return nil, err
	}
	if req.Git == nil {
		return nil, errors.New("release: no git runner")
	}
	if req.Build != nil && req.ChecksumFile == "" {
		return nil, errors.New("release: a build needs a checksum file")
	}
	if _, err := req.Git(req.Dir, "rev-parse", "--is-inside-work-tree"); err != nil {
		return nil, fmt.Errorf("%s is not inside a git repository: %w", req.Dir, err)
	}
	// Untracked files are allowed (a local config.yml, editor files); a change to
	// a tracked file is not, because the tag would not describe the binaries.
	status, err := req.Git(req.Dir, "status", "--porcelain", "--untracked-files=no")
	if err != nil {
		return nil, fmt.Errorf("git status: %w", err)
	}
	if strings.TrimSpace(status) != "" {
		return nil, errors.New("the working tree has uncommitted changes; commit or stash them before releasing")
	}
	commit, err := req.Git(req.Dir, "rev-parse", "HEAD")
	if err != nil {
		return nil, fmt.Errorf("the repository has no commit to release: %w", err)
	}
	tags, err := req.Git(req.Dir, "tag", "--list")
	if err != nil {
		return nil, fmt.Errorf("git tag --list: %w", err)
	}
	previous, err := latestVersion(strings.Fields(tags), tag)
	if err != nil {
		return nil, err
	}

	logArgs := []string{"log", "--no-merges", "--format=%s"}
	if previous != "" {
		logArgs = append(logArgs, previous+"..HEAD")
	}
	subjects, err := req.Git(req.Dir, logArgs...)
	if err != nil {
		return nil, fmt.Errorf("git log: %w", err)
	}
	var lines []string
	for _, line := range strings.Split(subjects, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			lines = append(lines, line)
		}
	}
	if len(lines) == 0 {
		return nil, fmt.Errorf("there are no commits since %s; nothing to release", previous)
	}
	return &Plan{
		req:      req,
		Tag:      tag,
		Previous: previous,
		Commit:   strings.TrimSpace(commit),
		Notes:    Notes(tag, previous, lines),
	}, nil
}

// latestVersion returns the newest version tag in tags, and refuses a release
// whose tag already exists or is not newer than it.
func latestVersion(tags []string, next string) (string, error) {
	previous := ""
	for _, tag := range tags {
		if tag == next {
			return "", fmt.Errorf("tag %s already exists", next)
		}
		if !versionPattern.MatchString(tag) || !strings.HasPrefix(tag, "v") {
			continue
		}
		if previous == "" {
			previous = tag
			continue
		}
		if newer, err := semver.IsNewer(tag, previous); err == nil && newer {
			previous = tag
		}
	}
	if previous != "" {
		newer, err := semver.IsNewer(next, previous)
		if err != nil {
			return "", err
		}
		if !newer {
			return "", fmt.Errorf("release %s is not newer than the latest tag %s", next, previous)
		}
	}
	return previous, nil
}

// conventional matches a Conventional Commits subject: type, optional scope,
// optional breaking-change bang, and the description.
var conventional = regexp.MustCompile(`^([a-z]+)(\([^)]*\))?(!)?: (.+)$`)

// Notes renders the release notes for subjects, newest first as git log lists
// them. Conventional Commits are grouped (breaking changes, features, fixes,
// then everything else); any other subject is listed under Other changes.
func Notes(tag, previous string, subjects []string) string {
	groups := map[string][]string{}
	for _, subject := range subjects {
		match := conventional.FindStringSubmatch(subject)
		if match == nil {
			groups["Other changes"] = append(groups["Other changes"], subject)
			continue
		}
		kind, scope, breaking, description := match[1], strings.Trim(match[2], "()"), match[3] == "!", match[4]
		entry := description
		if scope != "" {
			entry = "**" + scope + ":** " + description
		}
		switch {
		case breaking:
			groups["Breaking changes"] = append(groups["Breaking changes"], entry)
		case kind == "feat":
			groups["Features"] = append(groups["Features"], entry)
		case kind == "fix":
			groups["Fixes"] = append(groups["Fixes"], entry)
		default:
			groups["Other changes"] = append(groups["Other changes"], subject)
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", tag)
	if previous != "" {
		fmt.Fprintf(&b, "\nChanges since %s.\n", previous)
	}
	for _, heading := range []string{"Breaking changes", "Features", "Fixes", "Other changes"} {
		if len(groups[heading]) == 0 {
			continue
		}
		fmt.Fprintf(&b, "\n## %s\n\n", heading)
		for _, entry := range groups[heading] {
			fmt.Fprintf(&b, "- %s\n", entry)
		}
	}
	return b.String()
}

// Apply creates the tag, builds and writes the checksums. A failed build or
// checksum deletes the tag again, so a failed release leaves no tag behind.
func (p *Plan) Apply() (result *Result, err error) {
	req := p.req
	if _, err := req.Git(req.Dir, "tag", "--annotate", "--cleanup=verbatim", "--message", p.Notes, p.Tag, p.Commit); err != nil {
		return nil, fmt.Errorf("git tag %s: %w", p.Tag, err)
	}
	defer func() {
		if err == nil {
			return
		}
		if _, deleteErr := req.Git(req.Dir, "tag", "--delete", p.Tag); deleteErr != nil {
			err = fmt.Errorf("%w (deleting tag %s also failed: %v)", err, p.Tag, deleteErr)
			return
		}
		err = fmt.Errorf("%w (tag %s deleted)", err, p.Tag)
	}()

	result = &Result{Tag: p.Tag}
	if req.Build == nil {
		return result, nil
	}
	binaries, err := req.Build()
	if err != nil {
		return nil, err
	}
	if len(binaries) == 0 {
		return nil, errors.New("the build produced no binary")
	}
	if err := writeChecksums(req.Dir, req.ChecksumFile, binaries); err != nil {
		return nil, err
	}
	result.Binaries = binaries
	result.Checksums = req.ChecksumFile
	return result, nil
}

// writeChecksums writes a sha256sum-compatible file: one "<hex>  <name>" line
// per binary, sorted, with names relative to the checksum file so
// `sha256sum -c` works from its directory.
func writeChecksums(dir, file string, binaries []string) error {
	target := filepath.Join(dir, filepath.FromSlash(file))
	base := filepath.Dir(target)
	lines := make([]string, 0, len(binaries))
	for _, binary := range binaries {
		path := filepath.Join(dir, filepath.FromSlash(binary))
		sum, err := sha256File(path)
		if err != nil {
			return err
		}
		name, err := filepath.Rel(base, path)
		if err != nil {
			return fmt.Errorf("checksum %s: %w", binary, err)
		}
		lines = append(lines, sum+"  "+filepath.ToSlash(name))
	}
	sort.Strings(lines)
	if err := os.MkdirAll(base, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(file), err)
	}
	tmp, err := os.CreateTemp(base, ".checksums-*")
	if err != nil {
		return fmt.Errorf("write %s: %w", file, err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(strings.Join(lines, "\n") + "\n"); err != nil {
		tmp.Close()
		return fmt.Errorf("write %s: %w", file, err)
	}
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		return fmt.Errorf("write %s: %w", file, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("write %s: %w", file, err)
	}
	if err := os.Rename(tmp.Name(), target); err != nil {
		return fmt.Errorf("write %s: %w", file, err)
	}
	return nil
}

func sha256File(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("checksum %s: %w", path, err)
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", fmt.Errorf("checksum %s: %w", path, err)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// CommandGit returns the Git that runs the git binary. A failure carries git's
// standard error, which names the problem (not a repository, no commits).
func CommandGit() Git {
	return func(dir string, args ...string) (string, error) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		var stderr strings.Builder
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if err != nil {
			if message := strings.TrimSpace(stderr.String()); message != "" {
				return "", fmt.Errorf("%w: %s", err, message)
			}
			return "", err
		}
		return string(out), nil
	}
}
