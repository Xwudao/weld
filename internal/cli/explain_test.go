package cli

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

// TestExplainOutsideAProject proves explain works before `weld new`: it prints
// the descriptor, the reverse requirements derived from the catalog and each
// file's role, and has no project section.
func TestExplainOutsideAProject(t *testing.T) {
	out, err := run(t, "explain", "db", "--dir", t.TempDir())
	if err != nil {
		t.Fatalf("explain db: %v", err)
	}
	for _, want := range []string{
		"db v",
		"Requires:    loom",
		"Required by: seed, sqlcx, user",
		"db/migrations/<version>_create_items.sql",
		"internal/di/migrate_provider.go",
		"when loom",
		"config.yml",
		"weld:config",
		"restored from config.example.yml",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("explain db is missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Project:") {
		t.Errorf("explain outside a project printed a project section:\n%s", out)
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.HasSuffix(line, " ") {
			t.Errorf("line has trailing whitespace: %q", line)
		}
	}
}

// TestExplainReportsProjectState proves explain names exactly what an add would
// install into an uninstalled project, and the recorded version once it is
// installed.
func TestExplainReportsProjectState(t *testing.T) {
	root := t.TempDir()
	if _, err := run(t, "new", "demo", "--dir", root); err != nil {
		t.Fatalf("new: %v", err)
	}
	dir := filepath.Join(root, "demo")

	out, err := run(t, "explain", "user", "--dir", dir)
	if err != nil {
		t.Fatalf("explain user: %v", err)
	}
	if !strings.Contains(out, "adding it installs config, loom, db, http, jwt, user") || !strings.Contains(out, "--with-dependencies") {
		t.Errorf("explain user does not name the install chain:\n%s", out)
	}

	out, err = run(t, "explain", "base", "--dir", dir)
	if err != nil {
		t.Fatalf("explain base: %v", err)
	}
	if !strings.Contains(out, "Project:     installed v") {
		t.Errorf("explain base does not report the installed base:\n%s", out)
	}
}

// TestExplainJSON pins the machine-readable shape: roles, the generated DI
// sources and the project's install chain.
func TestExplainJSON(t *testing.T) {
	root := t.TempDir()
	if _, err := run(t, "new", "demo", "--dir", root); err != nil {
		t.Fatalf("new: %v", err)
	}
	out, err := run(t, "explain", "loom", "--json", "--dir", filepath.Join(root, "demo"))
	if err != nil {
		t.Fatalf("explain --json: %v", err)
	}
	var explained explanation
	if err := json.Unmarshal([]byte(out), &explained); err != nil {
		t.Fatalf("decode: %v\n%s", err, out)
	}
	if explained.Name != "loom" || explained.Kind != "add" {
		t.Fatalf("explained = %+v", explained)
	}
	roles := map[string]string{}
	for _, file := range explained.Files {
		roles[file.Path] = file.Role
	}
	if roles["internal/di/di.go"] != "generated" {
		t.Errorf("di.go role = %q, want generated", roles["internal/di/di.go"])
	}
	if explained.Project == nil || explained.Project.Installed || strings.Join(explained.Project.WouldInstall, ",") != "config,loom" {
		t.Errorf("project = %+v, want an uninstalled loom that installs config,loom", explained.Project)
	}
}

// TestExplainRejectsUnknownCapability proves a typo is an error that lists the
// available names, and that the argument count is validated.
func TestExplainRejectsUnknownCapability(t *testing.T) {
	_, err := run(t, "explain", "nope")
	if err == nil || !strings.Contains(err.Error(), `"nope"`) || !strings.Contains(err.Error(), "available: base,") {
		t.Fatalf("explain nope = %v, want an error listing the catalog", err)
	}
	if _, err := run(t, "explain"); err == nil || !strings.Contains(err.Error(), "usage: weld explain") {
		t.Fatalf("explain without a name = %v, want the usage", err)
	}
}
