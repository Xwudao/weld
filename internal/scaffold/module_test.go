package scaffold

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Xwudao/weld/internal/project"
)

// addModule plans and applies `weld add module <name>`.
func addModule(t *testing.T, dir, name string) *Result {
	t.Helper()
	result, err := AddModule(Request{Dir: dir, Catalog: newCatalog()}, name)
	if err != nil {
		t.Fatalf("AddModule %s: %v", name, err)
	}
	if err := result.Apply(); err != nil {
		t.Fatalf("Apply add module %s: %v", name, err)
	}
	return result
}

func TestValidateModuleName(t *testing.T) {
	for _, name := range []string{"widget", "orders", "a1", "catalog2"} {
		if err := ValidateModuleName(name); err != nil {
			t.Errorf("ValidateModuleName(%q) = %v, want nil", name, err)
		}
	}
	// Not a canonical Go package / URL segment, a Go keyword, or a reserved
	// built-in API path.
	for _, name := range []string{"", "Widget", "widget-1", "1widget", "_widget", "widget_1", "range", "func", "items", "openapi", "api"} {
		if err := ValidateModuleName(name); err == nil {
			t.Errorf("ValidateModuleName(%q) = nil, want an error", name)
		}
	}
}

func TestAddModuleInstallsHTTPAndConfig(t *testing.T) {
	root := t.TempDir()
	dir := create(t, root)

	result := addModule(t, dir, "widget")
	if got, want := strings.Join(result.Installed, ","), "config,http"; got != want {
		t.Fatalf("installed = %q, want %q", got, want)
	}
	for _, path := range []string{
		"internal/config/config.go",
		"internal/httpserver/http.go",
		"internal/httpserver/widget_route.go",
		"internal/modules/widget/dto.go",
		"internal/modules/widget/service.go",
		"internal/modules/widget/module.go",
		"internal/modules/widget/module_test.go",
		"internal/modules/widget/README.md",
	} {
		if _, err := os.Stat(filepath.Join(dir, path)); err != nil {
			t.Errorf("expected %s: %v", path, err)
		}
	}

	// The non-Loom route is appended to the httpserver extension point.
	httpGo := readFile(t, filepath.Join(dir, "internal/httpserver/http.go"))
	if !strings.Contains(httpGo, "routes = append(routes, installWidgetRoute)") {
		t.Errorf("routes extension point not patched:\n%s", httpGo)
	}
	if !strings.Contains(httpGo, "weld:module:widget:installed") {
		t.Errorf("module sentinel missing:\n%s", httpGo)
	}
	if strings.Count(httpGo, "weld:routes:begin") != 1 {
		t.Errorf("routes region duplicated:\n%s", httpGo)
	}

	manifest := mustLoad(t, dir)
	if !manifest.HasModule("widget") || manifest.ModuleVersion("widget") == "" {
		t.Fatalf("module not recorded: %+v", manifest.Modules)
	}
	if !manifest.HasCapability("http") || !manifest.HasCapability("config") {
		t.Fatalf("dependencies not recorded: %+v", manifest.Capabilities)
	}
	if len(manifest.Drift(dir)) != 0 {
		t.Fatalf("drift after add module: %+v", manifest.Drift(dir))
	}
	gofmtCheck(t, dir)
}

// TestAddModuleGeneratedProjectBuildsAndTests compiles and runs a real
// generated module project. Its internal/modules/widget/module_test.go exercises
// the HTTP handler in process with httptest, so no socket and no database is
// needed. It skips when dependencies cannot be resolved.
func TestAddModuleGeneratedProjectBuildsAndTests(t *testing.T) {
	root := t.TempDir()
	dir := create(t, root)
	addModule(t, dir, "widget")
	if !buildAndTestGeneratedProject(t, dir) {
		t.Skip("cannot resolve the generated project's dependencies (network/module cache unavailable)")
	}
}

func TestAddModuleIsIdempotent(t *testing.T) {
	root := t.TempDir()
	dir := create(t, root)
	addModule(t, dir, "widget")

	before := map[string]string{}
	for _, path := range []string{"internal/modules/widget/module.go", "internal/httpserver/http.go", project.ManifestName} {
		before[path] = readFile(t, filepath.Join(dir, path))
	}
	result, err := AddModule(Request{Dir: dir, Catalog: newCatalog()}, "widget")
	if err != nil {
		t.Fatalf("second AddModule: %v", err)
	}
	if len(result.Operations) != 0 {
		t.Fatalf("second AddModule planned %d operations, want 0", len(result.Operations))
	}
	if len(result.Notes) == 0 {
		t.Fatal("second AddModule produced no explanation")
	}
	if err := result.Apply(); err != nil {
		t.Fatalf("second Apply: %v", err)
	}
	for path, want := range before {
		if readFile(t, filepath.Join(dir, path)) != want {
			t.Errorf("%s changed on repeat add", path)
		}
	}
}

func TestAddModuleIsDryRunnable(t *testing.T) {
	root := t.TempDir()
	dir := create(t, root)

	manifestBefore := readFile(t, filepath.Join(dir, project.ManifestName))
	result, err := AddModule(Request{Dir: dir, Catalog: newCatalog()}, "widget")
	if err != nil {
		t.Fatalf("AddModule: %v", err)
	}
	if len(result.Operations) == 0 {
		t.Fatal("expected a non-empty plan")
	}
	// A dry run is a plan that is never applied.
	if readFile(t, filepath.Join(dir, project.ManifestName)) != manifestBefore {
		t.Fatal("planning wrote the manifest")
	}
	for _, path := range []string{"internal/modules", "internal/httpserver"} {
		if _, err := os.Stat(filepath.Join(dir, path)); !os.IsNotExist(err) {
			t.Fatalf("planning wrote %s", path)
		}
	}
}

func TestAddModuleRejectsInvalidNameBeforeWriting(t *testing.T) {
	root := t.TempDir()
	dir := create(t, root)

	manifestBefore := readFile(t, filepath.Join(dir, project.ManifestName))
	if _, err := AddModule(Request{Dir: dir, Catalog: newCatalog()}, "items"); err == nil {
		t.Fatal("expected an error for a reserved module name")
	}
	if readFile(t, filepath.Join(dir, project.ManifestName)) != manifestBefore {
		t.Fatal("a rejected module wrote the manifest")
	}
	if _, err := os.Stat(filepath.Join(dir, "internal", "modules")); !os.IsNotExist(err) {
		t.Fatalf("a rejected module wrote files: %v", err)
	}
}

func TestAddModuleRefusesUnmanagedFile(t *testing.T) {
	root := t.TempDir()
	dir := create(t, root)

	if err := os.MkdirAll(filepath.Join(dir, "internal", "modules", "widget"), 0o755); err != nil {
		t.Fatal(err)
	}
	userFile := filepath.Join(dir, "internal", "modules", "widget", "module.go")
	if err := os.WriteFile(userFile, []byte("package widget\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := AddModule(Request{Dir: dir, Catalog: newCatalog()}, "widget"); err == nil {
		t.Fatal("expected a conflict for an unmanaged module file")
	}
	if got := readFile(t, userFile); got != "package widget\n" {
		t.Fatalf("user file was modified: %q", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "internal", "httpserver")); !os.IsNotExist(err) {
		t.Fatal("partial files were written on failure")
	}
}

// TestAddModuleFilesSurviveLaterAdd proves a module package is a stable seam: a
// later capability add leaves a user-edited module file untouched and keeps the
// module route alongside the new one.
func TestAddModuleFilesSurviveLaterAdd(t *testing.T) {
	root := t.TempDir()
	dir := create(t, root)
	addModule(t, dir, "widget")

	path := filepath.Join(dir, "internal", "modules", "widget", "service.go")
	edited := readFile(t, path) + "\n// user wiring, must survive a later add\n"
	if err := os.WriteFile(path, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}

	add(t, dir, "web")

	if got := readFile(t, path); got != edited {
		t.Fatalf("a later add rewrote the user's module file:\n%s", got)
	}
	httpGo := readFile(t, filepath.Join(dir, "internal/httpserver/http.go"))
	for _, want := range []string{"installWidgetRoute", "installWebRoute"} {
		if !strings.Contains(httpGo, want) {
			t.Errorf("routes region lost %q after a later add:\n%s", want, httpGo)
		}
	}
}

// TestAddModuleComposesWithDBWithoutLoom proves a module and db compose without
// Loom, in the module-first order, without touching module files and without
// wiring a database into the HTTP surface.
func TestAddModuleComposesWithDBWithoutLoom(t *testing.T) {
	root := t.TempDir()
	dir := create(t, root)
	addModule(t, dir, "widget")
	moduleBefore := readFile(t, filepath.Join(dir, "internal/modules/widget/module.go"))

	add(t, dir, "db")

	manifest := mustLoad(t, dir)
	if !manifest.HasModule("widget") || !manifest.HasCapability("db") {
		t.Fatalf("composition not recorded: modules=%v caps=%v", manifest.Modules, manifest.Capabilities)
	}
	if readFile(t, filepath.Join(dir, "internal/modules/widget/module.go")) != moduleBefore {
		t.Fatal("installing db rewrote a module file")
	}
	if httpGo := readFile(t, filepath.Join(dir, "internal/httpserver/http.go")); !strings.Contains(httpGo, "installWidgetRoute") {
		t.Errorf("installing db dropped the module route:\n%s", httpGo)
	}
	if len(manifest.Drift(dir)) != 0 {
		t.Fatalf("drift: %+v", manifest.Drift(dir))
	}
	gofmtCheck(t, dir)
}

// TestAddModuleWithLoomRendersGraphAndPreservesDBPruning installs the module
// after Loom (the harder order): the regenerated graph must provide and route
// the module, and installing db must still declare the pool and repository as
// available bindings only, never constructing them in loom_gen.go.
func TestAddModuleWithLoomRendersGraphAndPreservesDBPruning(t *testing.T) {
	loomToolAvailable(t)
	root := t.TempDir()
	dir := create(t, root)
	add(t, dir, "loom")
	add(t, dir, "db")
	addModule(t, dir, "widget")

	// A Loom project wires through the graph, not the plain route seam.
	if _, err := os.Stat(filepath.Join(dir, "internal", "httpserver", "widget_route.go")); !os.IsNotExist(err) {
		t.Error("a Loom project also wrote the non-Loom route seam")
	}
	di := readFile(t, filepath.Join(dir, "internal/di/di.go"))
	for _, want := range []string{"loom.Provide(widget.NewService)", "widget.Register(mux, widgetService)", "loom.Provide(NewPool)"} {
		if !strings.Contains(di, want) {
			t.Errorf("di.go is missing %q:\n%s", want, di)
		}
	}
	gen := readFile(t, filepath.Join(dir, "internal/di/loom_gen.go"))
	if !strings.Contains(gen, "widget.NewService") {
		t.Errorf("loom_gen.go does not construct the module service:\n%s", gen)
	}
	for _, forbidden := range []string{"NewPool", "NewRepository"} {
		if strings.Contains(gen, forbidden) {
			t.Errorf("loom_gen.go constructs the unused %s; installing the module must not wire db:\n%s", forbidden, gen)
		}
	}
	if drift := mustLoad(t, dir).Drift(dir); len(drift) != 0 {
		t.Fatalf("drift: %+v", drift)
	}
	gofmtCheck(t, dir)
	if !buildAndTestGeneratedProject(t, dir) {
		t.Skip("cannot resolve the generated project's dependencies (network/module cache unavailable)")
	}
}

// TestAddLoomAfterModuleRendersGraph installs Loom after the module: the
// generated graph must include the pre-existing module, and the module package
// must not be rewritten. The generated project is only gofmt-checked here, since
// the module-after-Loom order already builds the same graph.
func TestAddLoomAfterModuleRendersGraph(t *testing.T) {
	loomToolAvailable(t)
	root := t.TempDir()
	dir := create(t, root)
	addModule(t, dir, "widget")
	moduleBefore := readFile(t, filepath.Join(dir, "internal/modules/widget/module.go"))

	add(t, dir, "loom")

	if readFile(t, filepath.Join(dir, "internal/modules/widget/module.go")) != moduleBefore {
		t.Fatal("installing Loom rewrote the module file")
	}
	di := readFile(t, filepath.Join(dir, "internal/di/di.go"))
	if !strings.Contains(di, "loom.Provide(widget.NewService)") {
		t.Errorf("di.go does not include the pre-existing module:\n%s", di)
	}
	gen := readFile(t, filepath.Join(dir, "internal/di/loom_gen.go"))
	if !strings.Contains(gen, "widget.NewService") {
		t.Errorf("loom_gen.go does not construct the pre-existing module service:\n%s", gen)
	}
	gofmtCheck(t, dir)
}
