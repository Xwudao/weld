package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Xwudao/weld/internal/project"
)

func TestRemoveModuleCommand(t *testing.T) {
	root := t.TempDir()
	mustRun := func(args ...string) string {
		t.Helper()
		out, err := run(t, args...)
		if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		return out
	}
	mustRun("new", "demo", "--dir", root)
	dir := filepath.Join(root, "demo")
	mustRun("add", "module", "widget", "--command", "--with-dependencies", "--dir", dir)
	mustRun("add", "module", "other", "--dir", dir)
	if _, err := run(t, "remove", "module", "../widget", "--dir", dir); err == nil {
		t.Fatal("invalid module name accepted")
	}
	modified := "internal/modules/widget/service.go"
	added := "internal/modules/widget/assets/content.txt"
	get := func(rel string) string {
		t.Helper()
		content, err := os.ReadFile(filepath.Join(dir, rel))
		if err != nil {
			t.Fatal(err)
		}
		return string(content)
	}
	if err := os.MkdirAll(filepath.Join(dir, filepath.Dir(added)), 0o755); err != nil {
		t.Fatal(err)
	}
	for path, content := range map[string]string{modified: get(modified) + "\n// user edit\n", added: "custom asset"} {
		if err := os.WriteFile(filepath.Join(dir, path), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	before := get(project.ManifestName)
	if _, err := run(t, "remove", "module", "widget", "--dir", dir); err == nil || !strings.Contains(err.Error(), "--delete-source") {
		t.Fatalf("removing developed module without opt-in: %v", err)
	}
	out := mustRun("remove", "module", "widget", "--delete-source", "--dry-run", "--dir", dir)
	removed := []string{modified, added, "internal/app/widget_command.go", "internal/di/widget_graph.go", "internal/commands/widget/command.go"}
	for _, file := range removed {
		if !strings.Contains(out, "remove "+file) {
			t.Errorf("dry run did not list %s: %s", file, out)
		}
		get(file)
	}
	if get(project.ManifestName) != before {
		t.Fatal("dry run changed the manifest")
	}

	mustRun("remove", "module", "widget", "--delete-source", "--dir", dir)
	for _, file := range append(removed, "internal/modules/widget") {
		if _, err := os.Lstat(filepath.Join(dir, file)); !os.IsNotExist(err) {
			t.Errorf("%s remains after removal: %v", file, err)
		}
	}
	m, err := project.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if m.HasModule("widget") || m.HasCommand("widget") || !m.HasModule("other") || !m.HasCapability("http") {
		t.Fatalf("unexpected manifest after removal: modules=%v commands=%v capabilities=%v", m.Modules, m.Commands, m.Capabilities)
	}
	for _, file := range removed {
		if m.Owns(file) {
			t.Errorf("manifest still records %s", file)
		}
	}
	for _, file := range []string{"internal/di/di.go", "internal/di/loom_gen.go"} {
		if got := get(file); strings.Contains(got, "widget") || !strings.Contains(got, "other") {
			t.Errorf("%s has incorrect module wiring", file)
		}
	}
	mustRun("remove", "module", "other", "--dir", dir)
}
