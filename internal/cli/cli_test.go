package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func run(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var out, errBuf bytes.Buffer
	err := Run(args, &out, &errBuf)
	return out.String(), err
}

func TestVersionAndHelp(t *testing.T) {
	out, err := run(t, "version")
	if err != nil {
		t.Fatalf("version: %v", err)
	}
	if !strings.Contains(out, progName) {
		t.Fatalf("version output = %q", out)
	}
	if _, err := run(t, "help"); err != nil {
		t.Fatalf("help: %v", err)
	}
	if _, err := run(t, "bogus"); err == nil {
		t.Fatal("expected error for unknown command")
	}
}

func TestNewAddListLifecycle(t *testing.T) {
	root := t.TempDir()

	if _, err := run(t, "new", "demo", "--module", "example.com/demo", "--dir", root); err != nil {
		t.Fatalf("new: %v", err)
	}
	project := filepath.Join(root, "demo")
	if _, err := os.Stat(filepath.Join(project, "main.go")); err != nil {
		t.Fatalf("new did not write main.go: %v", err)
	}

	listOut, err := run(t, "list", "--dir", project)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if !strings.Contains(listOut, "Installed in demo") {
		t.Fatalf("list output = %q", listOut)
	}

	if _, err := run(t, "add", "web", "--dir", project); err != nil {
		t.Fatalf("add web: %v", err)
	}
	if _, err := os.Stat(filepath.Join(project, "web", "package.json")); err != nil {
		t.Fatalf("add web did not write the frontend: %v", err)
	}
	// web requires http, which the same command installs without being asked.
	if _, err := os.Stat(filepath.Join(project, "internal", "httpserver", "http.go")); err != nil {
		t.Fatalf("add web did not resolve the http dependency: %v", err)
	}

	// Repeating add is a no-op that still succeeds.
	out, err := run(t, "add", "web", "--dir", project)
	if err != nil {
		t.Fatalf("repeat add: %v", err)
	}
	if !strings.Contains(out, "already installed") {
		t.Fatalf("repeat add output = %q", out)
	}
}

func TestAddHTTPThenWebInstallsEachOnce(t *testing.T) {
	root := t.TempDir()
	if _, err := run(t, "new", "demo", "--module", "example.com/demo", "--dir", root); err != nil {
		t.Fatalf("new: %v", err)
	}
	project := filepath.Join(root, "demo")

	if _, err := run(t, "add", "http", "--dir", project); err != nil {
		t.Fatalf("add http: %v", err)
	}
	out, err := run(t, "add", "web", "--dir", project)
	if err != nil {
		t.Fatalf("add web: %v", err)
	}
	if strings.Contains(out, "required capability") {
		t.Fatalf("web reinstalled an already-present dependency: %q", out)
	}

	listOut, err := run(t, "list", "--dir", project)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, capability := range []string{"http", "web"} {
		if !strings.Contains(listOut, "* "+capability) {
			t.Errorf("%s not marked installed in list output: %q", capability, listOut)
		}
	}
}

func TestAddDBInstallsWithoutHTTP(t *testing.T) {
	root := t.TempDir()
	if _, err := run(t, "new", "demo", "--module", "example.com/demo", "--dir", root); err != nil {
		t.Fatalf("new: %v", err)
	}
	dir := filepath.Join(root, "demo")

	out, err := run(t, "add", "db", "--dir", dir)
	if err != nil {
		t.Fatalf("add db: %v", err)
	}
	if !strings.Contains(out, "internal/data/data.go") {
		t.Fatalf("add db output = %q", out)
	}
	if _, err := os.Stat(filepath.Join(dir, "internal", "data", "data.go")); err != nil {
		t.Fatalf("add db did not write the repository: %v", err)
	}
	// db is independent of the HTTP capabilities.
	for _, path := range []string{"internal/httpserver", "internal/api", "internal/web"} {
		if _, err := os.Stat(filepath.Join(dir, path)); !os.IsNotExist(err) {
			t.Errorf("add db created %s", path)
		}
	}

	// Repeating add is a no-op that still succeeds.
	out, err = run(t, "add", "db", "--dir", dir)
	if err != nil {
		t.Fatalf("repeat add: %v", err)
	}
	if !strings.Contains(out, "already installed") {
		t.Fatalf("repeat add output = %q", out)
	}

	listOut, err := run(t, "list", "--dir", dir)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if !strings.Contains(listOut, "* db") {
		t.Errorf("db not marked installed in list output: %q", listOut)
	}
}

func TestNewDryRunWritesNothing(t *testing.T) {
	root := t.TempDir()
	out, err := run(t, "new", "demo", "--dir", root, "--dry-run")
	if err != nil {
		t.Fatalf("new --dry-run: %v", err)
	}
	if !strings.Contains(out, "dry run") {
		t.Fatalf("output = %q", out)
	}
	if _, err := os.Stat(filepath.Join(root, "demo")); !os.IsNotExist(err) {
		t.Fatal("dry run created the project")
	}
}
