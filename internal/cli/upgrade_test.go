package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Xwudao/weld/internal/project"
)

// olderMiddlewareSeam stands in for a project an older weld scaffolded: the
// middleware seam it wrote lacked the Recover entry this weld ships.
func olderMiddlewareSeam(t *testing.T, dir string) {
	t.Helper()
	const seam = "internal/httpserver/middleware.go"
	shipped, err := os.ReadFile(filepath.Join(dir, seam))
	if err != nil {
		t.Fatal(err)
	}
	older := strings.Replace(string(shipped), "\t\thttpx.Recover(logger),\n", "", 1)
	if err := os.WriteFile(filepath.Join(dir, seam), []byte(older), 0o644); err != nil {
		t.Fatal(err)
	}
	manifest, err := project.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	manifest.SetFile(seam, "http", project.RoleSeam, []byte(older))
	encoded, err := manifest.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, project.ManifestName), encoded, 0o644); err != nil {
		t.Fatal(err)
	}
	rewriteCapabilityVersion(t, dir, "http", "0.0.1")
}

// TestUpgradeJSONPendingAndResolve walks the agent workflow: read the JSON plan,
// apply it, see the seam stay pending (and --check fail), then resolve it.
func TestUpgradeJSONPendingAndResolve(t *testing.T) {
	root := t.TempDir()
	if _, err := run(t, "new", "demo", "--module", "example.com/demo", "--dir", root); err != nil {
		t.Fatalf("new: %v", err)
	}
	dir := filepath.Join(root, "demo")
	if _, err := run(t, "add", "http", "--with-dependencies", "--dir", dir); err != nil {
		t.Fatalf("add http: %v", err)
	}
	olderMiddlewareSeam(t, dir)

	out, err := run(t, "upgrade", "--dir", dir, "--dry-run", "--json")
	if err != nil {
		t.Fatalf("dry run --json: %v (%s)", err, out)
	}
	var report struct {
		Mode       string `json:"mode"`
		UpToDate   bool   `json:"upToDate"`
		Upgraded   []struct{ Name, From, To string }
		Operations []struct{ Action, Path string }
		Advisories []struct {
			Path, Reason, Role, From, To, Target, Diff string
		}
		NextSteps []string `json:"nextSteps"`
	}
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out)
	}
	if report.Mode != "dry-run" || report.UpToDate || len(report.Upgraded) != 1 || report.Upgraded[0].Name != "http" {
		t.Fatalf("report = %+v", report)
	}
	var seam bool
	for _, advisory := range report.Advisories {
		if advisory.Path == "internal/httpserver/middleware.go" {
			seam = advisory.Reason == "seam" && advisory.Role == "seam" && advisory.From == "0.0.1" &&
				strings.Contains(advisory.Target, "httpx.Recover(logger)") && advisory.Diff != ""
		}
	}
	if !seam {
		t.Fatalf("no complete seam advisory in %+v", report.Advisories)
	}
	if !strings.Contains(strings.Join(report.NextSteps, "\n"), "upgrade resolve internal/httpserver/middleware.go") {
		t.Fatalf("next steps do not name resolve: %q", report.NextSteps)
	}

	if out, err := run(t, "upgrade", "--dir", dir, "--verify=none"); err != nil {
		t.Fatalf("upgrade: %v (%s)", err, out)
	}
	out, err = run(t, "upgrade", "--dir", dir, "--check")
	if err == nil || !strings.Contains(err.Error(), "pending upgrade changes") {
		t.Fatalf("--check with a pending change: %v (%s)", err, out)
	}
	if !strings.Contains(out, "pending since 0.0.1") {
		t.Fatalf("check output does not show the pending change:\n%s", out)
	}

	if _, err := run(t, "upgrade", "resolve", "--dir", dir, "--all", "internal/httpserver/middleware.go"); err == nil {
		t.Fatal("resolve accepted both paths and --all")
	}
	before, err := os.ReadFile(filepath.Join(dir, project.ManifestName))
	if err != nil {
		t.Fatal(err)
	}
	out, err = run(t, "upgrade", "resolve", "--dir", dir, "--all", "--dry-run")
	if err != nil || !strings.Contains(out, "Would resolve 1 pending change") {
		t.Fatalf("resolve --dry-run: %v (%s)", err, out)
	}
	after, err := os.ReadFile(filepath.Join(dir, project.ManifestName))
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("resolve --dry-run rewrote weld.json")
	}

	if out, err := run(t, "upgrade", "resolve", "--dir", dir, "internal/httpserver/middleware.go"); err != nil {
		t.Fatalf("resolve: %v (%s)", err, out)
	}
	if out, err := run(t, "upgrade", "--dir", dir, "--check"); err != nil {
		t.Fatalf("--check after resolve: %v (%s)", err, out)
	}
	out, err = run(t, "upgrade", "resolve", "--dir", dir, "--all")
	if err != nil || !strings.Contains(out, "No pending upgrade changes") {
		t.Fatalf("resolve with nothing pending: %v (%s)", err, out)
	}
}
