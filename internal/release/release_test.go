package release

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// fakeGit answers git commands from a script keyed on the joined arguments and
// records every call, so a test sees exactly what a release ran.
type fakeGit struct {
	answers map[string]string
	fail    map[string]bool
	calls   []string
}

func (g *fakeGit) run(dir string, args ...string) (string, error) {
	key := strings.Join(args, " ")
	g.calls = append(g.calls, key)
	for prefix := range g.fail {
		if strings.HasPrefix(key, prefix) {
			return "", errors.New("exit status 1: scripted failure")
		}
	}
	return g.answers[key], nil
}

func newFakeGit() *fakeGit {
	return &fakeGit{
		answers: map[string]string{
			"rev-parse --is-inside-work-tree":           "true\n",
			"status --porcelain --untracked-files=no":   "",
			"rev-parse HEAD":                            "abc123\n",
			"tag --list":                                "v0.1.0\nv0.2.0\nv0.10.0\nnot-a-version\n",
			"log --no-merges --format=%s v0.10.0..HEAD": "feat(cli): add routes\nfix: keep tags\nfeat!: drop api\ndocs: explain\nplain subject\n",
			"log --no-merges --format=%s":               "chore: init\n",
		},
		fail: map[string]bool{},
	}
}

func TestNormalizeVersion(t *testing.T) {
	for in, want := range map[string]string{"1.2.3": "v1.2.3", "v0.11.0": "v0.11.0", " v2.0.0 ": "v2.0.0"} {
		if got, err := NormalizeVersion(in); err != nil || got != want {
			t.Errorf("NormalizeVersion(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"", "1.2", "v1.2.3-rc.1", "01.2.3", "latest", "v1.2.3.4"} {
		if _, err := NormalizeVersion(in); err == nil {
			t.Errorf("NormalizeVersion(%q) = nil error, want one", in)
		}
	}
}

func TestPrepareComputesPreviousTagAndNotes(t *testing.T) {
	git := newFakeGit()
	plan, err := Prepare(Request{Dir: "/p", Version: "0.11.0", Git: git.run})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Tag != "v0.11.0" || plan.Previous != "v0.10.0" || plan.Commit != "abc123" {
		t.Fatalf("plan = %+v", plan)
	}
	want := `v0.11.0

Changes since v0.10.0.

## Breaking changes

- drop api

## Features

- **cli:** add routes

## Fixes

- keep tags

## Other changes

- docs: explain
- plain subject
`
	if plan.Notes != want {
		t.Fatalf("notes:\n%s\nwant:\n%s", plan.Notes, want)
	}
	for _, call := range git.calls {
		if strings.HasPrefix(call, "tag --annotate") || strings.HasPrefix(call, "push") {
			t.Fatalf("Prepare ran a writing git command: %s", call)
		}
	}
}

func TestPrepareFirstRelease(t *testing.T) {
	git := newFakeGit()
	git.answers["tag --list"] = ""
	plan, err := Prepare(Request{Dir: "/p", Version: "v0.1.0", Git: git.run})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Previous != "" || !strings.Contains(plan.Notes, "- chore: init") || strings.Contains(plan.Notes, "Changes since") {
		t.Fatalf("first release plan = %+v", plan)
	}
}

func TestPrepareRefusals(t *testing.T) {
	for name, tc := range map[string]struct {
		version string
		mutate  func(*fakeGit)
		want    string
	}{
		"not a repository": {"0.11.0", func(g *fakeGit) { g.fail["rev-parse --is-inside-work-tree"] = true }, "not inside a git repository"},
		"dirty tree":       {"0.11.0", func(g *fakeGit) { g.answers["status --porcelain --untracked-files=no"] = " M main.go\n" }, "uncommitted changes"},
		"existing tag":     {"0.10.0", func(*fakeGit) {}, "already exists"},
		"older version":    {"0.9.0", func(*fakeGit) {}, "not newer than the latest tag v0.10.0"},
		"nothing new":      {"0.11.0", func(g *fakeGit) { g.answers["log --no-merges --format=%s v0.10.0..HEAD"] = "" }, "no commits since v0.10.0"},
		"bad version":      {"next", func(*fakeGit) {}, "invalid release version"},
	} {
		t.Run(name, func(t *testing.T) {
			git := newFakeGit()
			tc.mutate(git)
			if _, err := Prepare(Request{Dir: "/p", Version: tc.version, Git: git.run}); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Prepare = %v, want an error containing %q", err, tc.want)
			}
		})
	}
}

func TestApplyTagsBuildsAndWritesChecksums(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	contents := map[string]string{"bin/demo-linux-amd64": "linux binary", "bin/demo-windows-amd64.exe": "windows binary"}
	git := newFakeGit()
	plan, err := Prepare(Request{Dir: dir, Version: "0.11.0", Git: git.run, ChecksumFile: "bin/SHA256SUMS", Build: func() ([]string, error) {
		var built []string
		for path, body := range contents {
			if err := os.WriteFile(filepath.Join(dir, path), []byte(body), 0o755); err != nil {
				return nil, err
			}
			built = append(built, path)
		}
		return built, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := plan.Apply()
	if err != nil {
		t.Fatal(err)
	}
	tagCall := git.calls[len(git.calls)-1]
	if !strings.HasPrefix(tagCall, "tag --annotate --cleanup=verbatim --message v0.11.0\n") || !strings.HasSuffix(tagCall, " v0.11.0 abc123") {
		t.Fatalf("tag call = %q", tagCall)
	}
	if result.Checksums != "bin/SHA256SUMS" || len(result.Binaries) != 2 {
		t.Fatalf("result = %+v", result)
	}
	sums, err := os.ReadFile(filepath.Join(dir, "bin", "SHA256SUMS"))
	if err != nil {
		t.Fatal(err)
	}
	for path, body := range contents {
		sum := sha256.Sum256([]byte(body))
		if want := hex.EncodeToString(sum[:]) + "  " + filepath.Base(path); !strings.Contains(string(sums), want) {
			t.Fatalf("SHA256SUMS lacks %q:\n%s", want, sums)
		}
	}
}

func TestApplyDeletesTheTagWhenTheBuildFails(t *testing.T) {
	git := newFakeGit()
	plan, err := Prepare(Request{Dir: t.TempDir(), Version: "0.11.0", Git: git.run, ChecksumFile: "bin/SHA256SUMS", Build: func() ([]string, error) {
		return nil, errors.New("go build linux/amd64: exit status 1")
	}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = plan.Apply()
	if err == nil || !strings.Contains(err.Error(), "go build") || !strings.Contains(err.Error(), "tag v0.11.0 deleted") {
		t.Fatalf("Apply = %v, want the build error and the tag deletion", err)
	}
	if last := git.calls[len(git.calls)-1]; last != "tag --delete v0.11.0" {
		t.Fatalf("last git call = %q, want the tag deletion", last)
	}
}

func TestApplyTagOnly(t *testing.T) {
	git := newFakeGit()
	plan, err := Prepare(Request{Dir: "/p", Version: "0.11.0", Git: git.run})
	if err != nil {
		t.Fatal(err)
	}
	result, err := plan.Apply()
	if err != nil || result.Tag != "v0.11.0" || result.Checksums != "" {
		t.Fatalf("Apply tag-only = %+v, %v", result, err)
	}
}

// TestReleaseAgainstARealRepository runs the command git against a throwaway
// repository, proving the tag message survives git's comment cleanup.
func TestReleaseAgainstARealRepository(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	dir := t.TempDir()
	git := CommandGit()
	for _, args := range [][]string{
		{"init", "--quiet"},
		{"-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "--quiet", "--allow-empty", "-m", "feat: first"},
	} {
		if _, err := git(dir, args...); err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
	}
	plan, err := Prepare(Request{Dir: dir, Version: "0.1.0", Git: func(dir string, args ...string) (string, error) {
		return git(dir, append([]string{"-c", "user.name=t", "-c", "user.email=t@example.com"}, args...)...)
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := plan.Apply(); err != nil {
		t.Fatal(err)
	}
	message, err := git(dir, "tag", "--list", "--format=%(contents)", "v0.1.0")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(message, "## Features") || !strings.Contains(message, "- first") {
		t.Fatalf("tag message lost the notes:\n%s", message)
	}
}
