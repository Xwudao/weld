package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// gitIn runs git in dir with a fixed identity, failing the test on error.
func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=weld", "-c", "user.email=weld@example.com"}, args...)...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func TestReleaseCommand(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	root := t.TempDir()
	if _, err := run(t, "new", "demo", "--module", "example.com/demo", "--dir", root); err != nil {
		t.Fatalf("new: %v", err)
	}
	dir := filepath.Join(root, "demo")

	if _, err := run(t, "release", "0.1.0", "--dir", dir); err == nil || !strings.Contains(err.Error(), "not inside a git repository") {
		t.Fatalf("release outside git error = %v", err)
	}
	// The repository's identity is set in its config, because release runs git
	// tag with the user's own configuration.
	gitIn(t, dir, "init", "--quiet")
	gitIn(t, dir, "config", "user.name", "weld")
	gitIn(t, dir, "config", "user.email", "weld@example.com")
	gitIn(t, dir, "add", ".")
	gitIn(t, dir, "commit", "--quiet", "-m", "feat: initial scaffold")

	if _, err := run(t, "release", "next", "--dir", dir); err == nil || !strings.Contains(err.Error(), "invalid release version") {
		t.Fatalf("release next error = %v", err)
	}

	out, err := run(t, "release", "0.1.0", "--dir", dir, "--dry-run")
	if err != nil {
		t.Fatalf("release --dry-run: %v", err)
	}
	if !strings.Contains(out, "tag v0.1.0") || !strings.Contains(out, "- initial scaffold") || !strings.Contains(out, "write bin/SHA256SUMS") {
		t.Fatalf("release --dry-run output = %q", out)
	}
	if tags := gitIn(t, dir, "tag", "--list"); strings.TrimSpace(tags) != "" {
		t.Fatalf("a dry run created a tag: %q", tags)
	}

	out, err = run(t, "release", "0.1.0", "--dir", dir)
	if err != nil {
		t.Fatalf("release: %v", err)
	}
	if !strings.Contains(out, "Tagged v0.1.0") || !strings.Contains(out, "git push origin v0.1.0") || !strings.Contains(out, "built bin/demo") {
		t.Fatalf("release output = %q", out)
	}
	sums, err := os.ReadFile(filepath.Join(dir, "bin", "SHA256SUMS"))
	if err != nil {
		t.Fatalf("read SHA256SUMS: %v", err)
	}
	if !strings.HasSuffix(strings.TrimSpace(string(sums)), "  demo") {
		t.Fatalf("SHA256SUMS = %q", sums)
	}
	if message := gitIn(t, dir, "tag", "--list", "--format=%(contents)", "v0.1.0"); !strings.Contains(message, "## Features") {
		t.Fatalf("tag message = %q", message)
	}

	// The same version again, and a release with nothing new, are refused.
	if _, err := run(t, "release", "0.1.0", "--dir", dir); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("repeat release error = %v", err)
	}
	if _, err := run(t, "release", "0.2.0", "--no-build", "--dir", dir); err == nil || !strings.Contains(err.Error(), "no commits since v0.1.0") {
		t.Fatalf("empty release error = %v", err)
	}

	// A tracked change blocks a release; an untracked file does not.
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("untracked\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, "release", "0.2.0", "--no-build", "--dir", dir); err == nil || !strings.Contains(err.Error(), "uncommitted changes") {
		t.Fatalf("dirty release error = %v", err)
	}
	gitIn(t, dir, "commit", "--quiet", "-am", "fix: simplify main")
	out, err = run(t, "release", "0.2.0", "--no-build", "--dir", dir)
	if err != nil {
		t.Fatalf("tag-only release: %v", err)
	}
	if strings.Contains(out, "SHA256SUMS") || !strings.Contains(out, "## Fixes") {
		t.Fatalf("tag-only release output = %q", out)
	}
}
