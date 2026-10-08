package scaffold

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Xwudao/weld/internal/project"
	"github.com/Xwudao/weld/internal/template"
)

const (
	testName   = "demo"
	testModule = "example.com/demo"
)

func newCatalog() *template.Catalog { return template.Load() }

func create(t *testing.T, root string) string {
	t.Helper()
	result, err := Create(Request{Dir: root, Name: testName, Module: testModule, Version: "weld 0.1.0", Catalog: newCatalog()})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := result.Apply(); err != nil {
		t.Fatalf("Apply create: %v", err)
	}
	return filepath.Join(root, testName)
}

func addWeb(t *testing.T, dir string) *Result {
	t.Helper()
	result, err := Add(Request{Dir: dir, Catalog: newCatalog()}, "web")
	if err != nil {
		t.Fatalf("Add web: %v", err)
	}
	if err := result.Apply(); err != nil {
		t.Fatalf("Apply add web: %v", err)
	}
	return result
}

func goBuild(t *testing.T, dir string) {
	t.Helper()
	cmd := exec.Command("go", "build", "./...")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOPROXY=off", "GOWORK=off")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(raw)
}

func TestCreateWritesMinimalCLI(t *testing.T) {
	root := t.TempDir()
	dir := create(t, root)

	for _, path := range []string{
		"main.go",
		"internal/app/app.go",
		"internal/app/serve.go",
		"internal/server/server.go",
		"internal/server/assets.go",
		"Makefile",
		"README.md",
		".gitignore",
		project.ManifestName,
	} {
		if _, err := os.Stat(filepath.Join(dir, path)); err != nil {
			t.Errorf("expected %s: %v", path, err)
		}
	}

	// No web dependency or empty directory leaks into the base scaffold.
	if _, err := os.Stat(filepath.Join(dir, "web")); !os.IsNotExist(err) {
		t.Error("base scaffold created a web/ directory")
	}
	if strings.Contains(readFile(t, filepath.Join(dir, "Makefile")), "weld:web:installed") {
		t.Error("base Makefile already contains the web extension")
	}

	manifest, err := project.Load(dir)
	if err != nil {
		t.Fatalf("load manifest: %v", err)
	}
	if manifest.Base.Name != "base" || manifest.Base.Version == "" {
		t.Fatalf("base not recorded: %+v", manifest.Base)
	}
	if len(manifest.Capabilities) != 0 {
		t.Fatalf("fresh project has capabilities: %+v", manifest.Capabilities)
	}
	if len(manifest.Drift(dir)) != 0 {
		t.Fatalf("fresh project drifts: %+v", manifest.Drift(dir))
	}

	goBuild(t, dir)
}

func TestCreateRefusesExistingDirectory(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, testName), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Create(Request{Dir: root, Name: testName, Catalog: newCatalog()}); err == nil {
		t.Fatal("expected error when project directory exists")
	}
}

func TestAddWebAddsFrontendAndBuilds(t *testing.T) {
	root := t.TempDir()
	dir := create(t, root)

	before := readFile(t, filepath.Join(dir, "Makefile"))
	result := addWeb(t, dir)

	for _, path := range []string{
		"web/package.json",
		"web/vite.config.ts",
		"web/tsconfig.json",
		"web/index.html",
		"web/src/main.tsx",
		"web/src/App.tsx",
		"web/src/index.css",
		"internal/server/assets_web.go",
		"internal/server/dist/.gitkeep",
	} {
		if _, err := os.Stat(filepath.Join(dir, path)); err != nil {
			t.Errorf("expected %s: %v", path, err)
		}
	}

	after := readFile(t, filepath.Join(dir, "Makefile"))
	if after == before {
		t.Fatal("Makefile was not patched")
	}
	if strings.Count(after, "# weld:web:installed") != 1 {
		t.Fatal("web extension inserted more than once")
	}
	if !strings.Contains(after, "npm --prefix web run build") {
		t.Fatal("web build command missing from Makefile")
	}
	if !strings.Contains(readFile(t, filepath.Join(dir, ".gitignore")), "/web/node_modules/") {
		t.Fatal(".gitignore not patched")
	}

	manifest, err := project.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !manifest.HasCapability("web") || manifest.CapabilityVersion("web") != result.Capability.Version {
		t.Fatalf("web not recorded: %+v", manifest.Capabilities)
	}
	if len(manifest.Drift(dir)) != 0 {
		t.Fatalf("drift after add: %+v", manifest.Drift(dir))
	}

	goBuild(t, dir)
}

func TestAddWebIsIdempotent(t *testing.T) {
	root := t.TempDir()
	dir := create(t, root)
	addWeb(t, dir)

	before := readFile(t, filepath.Join(dir, "Makefile"))
	manifestBefore := readFile(t, filepath.Join(dir, project.ManifestName))

	result, err := Add(Request{Dir: dir, Catalog: newCatalog()}, "web")
	if err != nil {
		t.Fatalf("second Add: %v", err)
	}
	if len(result.Operations) != 0 {
		t.Fatalf("second Add planned %d operations, want 0", len(result.Operations))
	}
	if len(result.Notes) == 0 {
		t.Fatal("second Add produced no explanation")
	}
	if err := result.Apply(); err != nil {
		t.Fatalf("second Apply: %v", err)
	}

	if readFile(t, filepath.Join(dir, "Makefile")) != before {
		t.Fatal("Makefile changed on repeat add")
	}
	if readFile(t, filepath.Join(dir, project.ManifestName)) != manifestBefore {
		t.Fatal("manifest changed on repeat add")
	}
}

func TestAddWebRejectsUnmanagedFile(t *testing.T) {
	root := t.TempDir()
	dir := create(t, root)

	if err := os.MkdirAll(filepath.Join(dir, "web"), 0o755); err != nil {
		t.Fatal(err)
	}
	userFile := filepath.Join(dir, "web", "package.json")
	if err := os.WriteFile(userFile, []byte("{\"mine\":true}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	makefileBefore := readFile(t, filepath.Join(dir, "Makefile"))
	manifestBefore := readFile(t, filepath.Join(dir, project.ManifestName))

	if _, err := Add(Request{Dir: dir, Catalog: newCatalog()}, "web"); err == nil {
		t.Fatal("expected conflict for unmanaged file")
	}

	if readFile(t, userFile) != "{\"mine\":true}\n" {
		t.Fatal("user file was modified")
	}
	if readFile(t, filepath.Join(dir, "Makefile")) != makefileBefore {
		t.Fatal("Makefile changed on conflicting add")
	}
	if readFile(t, filepath.Join(dir, project.ManifestName)) != manifestBefore {
		t.Fatal("manifest changed on conflicting add")
	}
	if _, err := os.Stat(filepath.Join(dir, "internal/server/assets_web.go")); !os.IsNotExist(err) {
		t.Fatal("partial web files were written on failure")
	}
}

func TestAddRequiresWeldProject(t *testing.T) {
	if _, err := Add(Request{Dir: t.TempDir(), Catalog: newCatalog()}, "web"); err == nil {
		t.Fatal("expected error for non-project directory")
	}
}

func TestAddIsDryRunnable(t *testing.T) {
	root := t.TempDir()
	dir := create(t, root)

	makefileBefore := readFile(t, filepath.Join(dir, "Makefile"))
	manifestBefore := readFile(t, filepath.Join(dir, project.ManifestName))

	result, err := Add(Request{Dir: dir, Catalog: newCatalog()}, "web")
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if len(result.Operations) == 0 {
		t.Fatal("expected a non-empty plan")
	}
	// A dry run is a plan that is never applied.
	if readFile(t, filepath.Join(dir, "Makefile")) != makefileBefore {
		t.Fatal("planning wrote the Makefile")
	}
	if readFile(t, filepath.Join(dir, project.ManifestName)) != manifestBefore {
		t.Fatal("planning wrote the manifest")
	}
	if _, err := os.Stat(filepath.Join(dir, "web")); !os.IsNotExist(err) {
		t.Fatal("planning wrote the web frontend")
	}
}

func TestAddWebDoesNotTouchUnrelatedFiles(t *testing.T) {
	root := t.TempDir()
	dir := create(t, root)

	untouched := []string{
		"main.go",
		"internal/app/app.go",
		"internal/app/serve.go",
		"internal/server/server.go",
		"internal/server/assets.go",
		"README.md",
	}
	before := map[string]string{}
	for _, path := range untouched {
		before[path] = readFile(t, filepath.Join(dir, path))
	}

	addWeb(t, dir)

	for _, path := range untouched {
		if readFile(t, filepath.Join(dir, path)) != before[path] {
			t.Errorf("unrelated file %s changed after add web", path)
		}
	}
}
