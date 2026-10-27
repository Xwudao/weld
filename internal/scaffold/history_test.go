package scaffold

import (
	"os/exec"
	"path/filepath"
	"testing"
)

// TestGitHistoryReadsCommittedVersionsNewestFirst covers a project nested in a
// larger repository, which is why the lookup is relative to the project
// directory rather than the repository root.
func TestGitHistoryReadsCommittedVersionsNewestFirst(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	repo := t.TempDir()
	dir := filepath.Join(repo, "services", "demo")
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-c", "user.name=weld", "-c", "user.email=weld@example.invalid", "-c", "commit.gpgsign=false"}, args...)...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q")
	for _, content := range []string{"first\n", "second\n"} {
		writeProjectFile(t, dir, "internal/x.go", content)
		git("add", "-A")
		git("commit", "-q", "-m", content)
	}

	versions, err := GitHistory(dir, "internal/x.go")
	if err != nil {
		t.Fatalf("GitHistory: %v", err)
	}
	if len(versions) != 2 || string(versions[0]) != "second\n" || string(versions[1]) != "first\n" {
		t.Fatalf("versions = %q", versions)
	}
	if none, err := GitHistory(t.TempDir(), "internal/x.go"); err != nil || len(none) != 0 {
		t.Fatalf("history outside a repository = %q, %v", none, err)
	}
}
