package doctor

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Xwudao/weld/internal/project"
	"github.com/Xwudao/weld/internal/projectcfg"
	"github.com/Xwudao/weld/internal/scaffold"
	"github.com/Xwudao/weld/internal/template"
)

func newProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	result, err := scaffold.Create(scaffold.Request{
		Dir:     root,
		Name:    "demo",
		Module:  "example.com/demo",
		Version: "test",
		Catalog: template.Load(),
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := result.Apply(); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	return filepath.Join(root, "demo")
}

func alwaysLookPath(name string) (string, error) { return "/usr/bin/" + name, nil }

func missingLookPath(missing ...string) func(string) (string, error) {
	set := make(map[string]bool, len(missing))
	for _, name := range missing {
		set[name] = true
	}
	return func(name string) (string, error) {
		if set[name] {
			return "", errors.New("not found")
		}
		return "/usr/bin/" + name, nil
	}
}

func collector() (*Result, func(string, Level, string, ...any)) {
	result := &Result{}
	add := func(name string, level Level, format string, args ...any) {
		result.Checks = append(result.Checks, Check{Name: name, Level: level, Message: fmt.Sprintf(format, args...)})
	}
	return result, add
}

func findCheck(t *testing.T, result *Result, name string) Check {
	t.Helper()
	for _, check := range result.Checks {
		if check.Name == name {
			return check
		}
	}
	t.Fatalf("no %q check in %+v", name, result.Checks)
	return Check{}
}

// TestRunOnFreshProject proves a project weld just created is healthy.
func TestRunOnFreshProject(t *testing.T) {
	dir := newProject(t)
	result := Run(Request{Dir: dir, Catalog: template.Load(), LookPath: alwaysLookPath})
	if result.Failed() {
		t.Fatalf("fresh project failed: %+v", result.Checks)
	}
	if _, warnings, _ := result.Counts(); warnings != 0 {
		t.Fatalf("fresh project has warnings: %+v", result.Checks)
	}
	if check := findCheck(t, result, "project"); check.Level != LevelOK || !strings.Contains(check.Message, "demo") {
		t.Fatalf("project check = %+v", check)
	}
	if check := findCheck(t, result, "go.mod"); check.Level != LevelOK || check.Message != "example.com/demo" {
		t.Fatalf("go.mod check = %+v", check)
	}
}

// TestRunReportsChangedManagedFile proves a user edit is surfaced as a warning,
// not a failure or a write.
func TestRunReportsChangedManagedFile(t *testing.T) {
	dir := newProject(t)
	path := filepath.Join(dir, "README.md")
	if err := os.WriteFile(path, []byte("user notes\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	result := Run(Request{Dir: dir, Catalog: template.Load(), LookPath: alwaysLookPath})
	if result.Failed() {
		t.Fatalf("drift failed the run: %+v", result.Checks)
	}
	check := findCheck(t, result, "managed files")
	if check.Level != LevelWarn || !strings.Contains(check.Message, "README.md") {
		t.Fatalf("managed files check = %+v", check)
	}
}

// TestRunReportsMissingProject proves a directory that is not a weld project is
// a failed check, not a crash.
func TestRunReportsMissingProject(t *testing.T) {
	result := Run(Request{Dir: t.TempDir(), Catalog: template.Load(), LookPath: alwaysLookPath})
	if !result.Failed() {
		t.Fatal("missing project was not a failure")
	}
	if check := findCheck(t, result, "project"); check.Level != LevelFail {
		t.Fatalf("project check = %+v", check)
	}
}

// TestCheckCapabilitiesReportsUpgradeAndUnknown covers the catalog comparison:
// an older recorded version suggests weld upgrade and an unknown name is
// reported rather than invented.
func TestCheckCapabilitiesReportsUpgradeAndUnknown(t *testing.T) {
	manifest := &project.Manifest{
		Base: project.CapabilityRef{Name: "base", Version: "0.3.0"},
		Capabilities: []project.CapabilityRef{
			{Name: "http", Version: "0.0.1"},
			{Name: "not-shipped", Version: "1.0.0"},
		},
	}
	result, add := collector()
	checkCapabilities(manifest, template.Load(), add)
	if len(result.Checks) != 2 {
		t.Fatalf("checks = %+v", result.Checks)
	}
	messages := result.Checks[0].Message + "\n" + result.Checks[1].Message
	if !strings.Contains(messages, "upgrade available") || !strings.Contains(messages, "http") {
		t.Errorf("capability check does not report the upgrade: %s", messages)
	}
	if !strings.Contains(messages, "not-shipped") {
		t.Errorf("capability check does not report the unknown capability: %s", messages)
	}
}

// TestCheckToolchainWarnsForMissingWebTools proves web installs are checked for
// node and pnpm without failing the run.
func TestCheckToolchainWarnsForMissingWebTools(t *testing.T) {
	manifest := &project.Manifest{Capabilities: []project.CapabilityRef{{Name: "web", Version: "0.1.0"}}}
	result, add := collector()
	checkToolchain(t.TempDir(), manifest, nil, missingLookPath("pnpm"), add)
	check := findCheck(t, result, "web toolchain")
	if check.Level != LevelWarn || !strings.Contains(check.Message, "pnpm") {
		t.Fatalf("web toolchain check = %+v", check)
	}
}

// TestCheckToolchainValidatesDeploySettings proves an incomplete deploy section
// fails, while a complete one only warns about a missing ssh/scp.
func TestCheckToolchainValidatesDeploySettings(t *testing.T) {
	manifest := &project.Manifest{}
	cfg := &projectcfg.Config{Deploy: projectcfg.DeployConfig{Host: "prod", RemoteUploadDir: "/srv/app"}}
	result, add := collector()
	checkToolchain(t.TempDir(), manifest, cfg, alwaysLookPath, add)
	if check := findCheck(t, result, "deploy"); check.Level != LevelFail || !strings.Contains(check.Message, "remote_script") {
		t.Fatalf("incomplete deploy check = %+v", check)
	}

	cfg.Deploy.RemoteScript = "/srv/app/deploy.sh"
	result, add = collector()
	checkToolchain(t.TempDir(), manifest, cfg, missingLookPath("ssh"), add)
	if check := findCheck(t, result, "deploy"); check.Level != LevelWarn || !strings.Contains(check.Message, "ssh") {
		t.Fatalf("missing ssh check = %+v", check)
	}
}

// TestCheckGeneratedFilesReportsMissingLoom proves a missing generated graph is
// a failure with a reinstall hint.
func TestCheckGeneratedFilesReportsMissingLoom(t *testing.T) {
	manifest := &project.Manifest{Capabilities: []project.CapabilityRef{{Name: "loom", Version: "0.7.1"}}}
	result, add := collector()
	checkGeneratedFiles(t.TempDir(), manifest, add)
	check := findCheck(t, result, "generated")
	if check.Level != LevelFail || !strings.Contains(check.Message, "weld add loom") {
		t.Fatalf("generated check = %+v", check)
	}
}

// TestCheckDatabaseFilesReportsMissingTool proves db commands are checked for
// the pinned tool module and migrations directory.
func TestCheckDatabaseFilesReportsMissingTool(t *testing.T) {
	manifest := &project.Manifest{Capabilities: []project.CapabilityRef{{Name: "db", Version: "0.1.1"}}}
	result, add := collector()
	checkDatabaseFiles(t.TempDir(), manifest, add)
	check := findCheck(t, result, "database")
	if check.Level != LevelFail || !strings.Contains(check.Message, "weld add db") {
		t.Fatalf("database check = %+v", check)
	}
}

func noEnvLookup(string) (string, bool) { return "", false }

// TestCheckTestDatabaseReportsConfigured proves the test-database check resolves
// config.yml and reports the source, never the dsn.
func TestCheckTestDatabaseReportsConfigured(t *testing.T) {
	dir := t.TempDir()
	config := "environment: local\ndatabase:\n  host: db.internal\n  port: 6543\n  user: app\n  password: file-secret\n  name: appdb\ndatabase_test:\n  name: appdb_test\n"
	if err := os.WriteFile(filepath.Join(dir, "config.yml"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	manifest := &project.Manifest{Capabilities: []project.CapabilityRef{{Name: "db", Version: "0.2.0"}}}
	result, add := collector()
	checkTestDatabase(dir, manifest, noEnvLookup, add)
	check := findCheck(t, result, "database test")
	if check.Level != LevelOK || !strings.Contains(check.Message, "config.yml") {
		t.Fatalf("database test check = %+v", check)
	}
	if strings.Contains(check.Message, "file-secret") {
		t.Fatalf("database test check leaked the password: %+v", check)
	}
}

// TestCheckTestDatabaseWarnsWhenMissing proves an unconfigured test database is
// a warning, not a failure, so a project that intentionally runs no integration
// tests stays healthy.
func TestCheckTestDatabaseWarnsWhenMissing(t *testing.T) {
	dir := t.TempDir()
	config := "database:\n  host: db\n  user: app\n  name: appdb\n"
	if err := os.WriteFile(filepath.Join(dir, "config.yml"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	manifest := &project.Manifest{Capabilities: []project.CapabilityRef{{Name: "db", Version: "0.2.0"}}}
	result, add := collector()
	checkTestDatabase(dir, manifest, noEnvLookup, add)
	check := findCheck(t, result, "database test")
	if check.Level != LevelWarn || !strings.Contains(check.Message, "database_test.name") {
		t.Fatalf("database test check = %+v", check)
	}
}

// TestHelpers covers the small parsing helpers used by the checks.
func TestHelpers(t *testing.T) {
	if got := goModulePath("module example.com/demo\n\ngo 1.25.0\n"); got != "example.com/demo" {
		t.Fatalf("goModulePath = %q", got)
	}
	if got := goModulePath("// no module here\n"); got != "" {
		t.Fatalf("goModulePath missing = %q", got)
	}
	if !ignoreRulePresent("# local config\n/config.yml\n", "config.yml") {
		t.Fatal("config.yml ignoring was not detected")
	}
	if ignoreRulePresent("*.local\n", "config.yml") {
		t.Fatal("unrelated ignore rule matched")
	}
	if validYAML([]byte("database:\n  user: [unterminated\n")) {
		t.Fatal("invalid YAML was accepted")
	}
}
