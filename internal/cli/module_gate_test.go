package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Xwudao/weld/internal/project"
)

func TestModuleRegistrationGate(t *testing.T) {
	dir := t.TempDir()
	write := func(path, content string) {
		t.Helper()
		path = filepath.Join(dir, path)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module example.com/demo\n\ngo 1.23\n")
	write("main.go", "package main\nfunc main() {}\n")
	write("Dockerfile", "FROM scratch\n")
	write("internal/modules/orders/custom.go", "package orders\n")
	write("internal/modules/notes/README.md", "Not a Go package\n")

	commands := [][]string{
		{"build"},
		{"build", "--docker", "--dry-run"},
		{"test", "--db=off"},
		{"test", "--db=off", "--dry-run", "."},
	}
	invoke := func(args []string) error {
		t.Helper()
		_, err := run(t, append([]string{"--dir", dir}, args...)...)
		return err
	}
	for _, args := range commands {
		if err := invoke(args); err != nil {
			t.Fatalf("%v in plain Go project: %v", args, err)
		}
	}

	manifest := project.NewManifest("demo", "example.com/demo", "test")
	writeManifest := func() {
		t.Helper()
		raw, err := manifest.Encode()
		if err != nil {
			t.Fatal(err)
		}
		write(project.ManifestName, string(raw))
	}
	writeManifest()
	for _, args := range commands {
		err := invoke(args)
		if err == nil || !strings.Contains(err.Error(), "internal/modules/orders") ||
			!strings.Contains(err.Error(), "weld add module orders --adopt") {
			t.Errorf("%v with unregistered module: %v", args, err)
		}
	}

	// An adopted package has a module entry but no managed files.
	manifest.AddModule(project.ModuleRef{Name: "orders", Version: "1.0.0"})
	writeManifest()
	for _, args := range commands {
		if err := invoke(args); err != nil {
			t.Errorf("%v with registered module: %v", args, err)
		}
	}

	write(project.ManifestName, "{")
	for _, args := range commands {
		if err := invoke(args); err == nil || !strings.Contains(err.Error(), "invalid manifest") {
			t.Errorf("%v with malformed weld.json: %v", args, err)
		}
	}
}
