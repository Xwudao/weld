package projectcfg

import (
	"os/exec"
	"strings"
)

// gitHash returns the full HEAD commit hash, or "" when git is unavailable or
// the directory is not a repository. Build metadata is best effort: a missing
// git never fails a build, it only leaves the injected variable empty.
func gitHash() string {
	out, err := runGit("rev-parse", "HEAD")
	if err != nil {
		return ""
	}
	return out
}

// gitTag returns the most recent tag reachable from HEAD, or "" when there is
// none. It mirrors `git describe --tags --abbrev=0`, so a dirty tree still
// reports the last release tag.
func gitTag() string {
	out, err := runGit("describe", "--tags", "--abbrev=0")
	if err != nil {
		return ""
	}
	return out
}

// runGit runs one git query and returns its trimmed first line.
func runGit(args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
