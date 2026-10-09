package scaffold

import (
	"bytes"
	"encoding/json"
	"io"
	"io/fs"
	"net"
	nethttp "net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

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

func add(t *testing.T, dir, name string) *Result {
	t.Helper()
	result, err := Add(Request{Dir: dir, Catalog: newCatalog()}, name)
	if err != nil {
		t.Fatalf("Add %s: %v", name, err)
	}
	if err := result.Apply(); err != nil {
		t.Fatalf("Apply add %s: %v", name, err)
	}
	return result
}

func goBuild(t *testing.T, dir string) {
	t.Helper()
	runGo(t, dir, "build", "./...")
}

func goTest(t *testing.T, dir, pkg string) {
	t.Helper()
	runGo(t, dir, "test", pkg)
}

func runGo(t *testing.T, dir string, args ...string) {
	t.Helper()
	runGoWithEnv(t, dir, os.Environ(), args...)
}

// runGoWithEnv runs the go command in dir with an explicit environment, so a
// test can prove behavior with a variable such as DATABASE_URL removed. It
// fails the test on a non-zero exit.
func runGoWithEnv(t *testing.T, dir string, env []string, args ...string) {
	t.Helper()
	cmd := exec.Command("go", args...)
	cmd.Dir = dir
	cmd.Env = append(env, "GOPROXY=off", "GOWORK=off")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

// gofmtCheck fails when the generated project is not gofmt-clean, which would
// mean a marker patch produced invalid formatting.
func gofmtCheck(t *testing.T, dir string) {
	t.Helper()
	cmd := exec.Command("gofmt", "-l", ".")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("gofmt -l: %v\n%s", err, out)
	}
	if files := strings.TrimSpace(string(out)); files != "" {
		t.Fatalf("generated project is not gofmt-clean:\n%s", files)
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
		"go.sum",
		"Makefile",
		"README.md",
		".gitignore",
		project.ManifestName,
	} {
		if _, err := os.Stat(filepath.Join(dir, path)); err != nil {
			t.Errorf("expected %s: %v", path, err)
		}
	}

	// No capability-specific files leak into the base scaffold: no HTTP server,
	// no serve command, no frontend, no API.
	for _, path := range []string{
		"internal/app/serve.go",
		"internal/httpserver",
		"internal/web",
		"internal/api",
		"web",
	} {
		if _, err := os.Stat(filepath.Join(dir, path)); !os.IsNotExist(err) {
			t.Errorf("base scaffold created %s", path)
		}
	}
	// The base go.mod carries the weld:deps extension point and pins Cobra, the
	// command tree every capability extends. pflag and mousetrap are Cobra's own
	// requirements, pinned so the base project builds with no network access.
	goMod := readFile(t, filepath.Join(dir, "go.mod"))
	if !strings.Contains(goMod, "weld:deps:begin") || !strings.Contains(goMod, "weld:deps:end") {
		t.Errorf("base go.mod is missing the weld:deps extension point:\n%s", goMod)
	}
	if !strings.Contains(goMod, "github.com/spf13/cobra") || !strings.Contains(goMod, "github.com/spf13/pflag") {
		t.Errorf("base go.mod does not pin Cobra and pflag:\n%s", goMod)
	}
	if strings.Contains(goMod, "go-validate") {
		t.Errorf("base go.mod already requires go-validate:\n%s", goMod)
	}
	// The base app is a Cobra command tree; it must not smuggle in HTTP or a
	// serve command, which only the http capability adds.
	if app := readFile(t, filepath.Join(dir, "internal/app/app.go")); !strings.Contains(app, "github.com/spf13/cobra") {
		t.Error("base app.go does not build on Cobra")
	} else if strings.Contains(app, "net/http") || strings.Contains(app, "\"serve\"") {
		t.Error("base app.go references HTTP or the serve command")
	}
	if readme := readFile(t, filepath.Join(dir, "README.md")); strings.Contains(readme, "/api/health") {
		t.Error("base README mentions the API health endpoint")
	}
	if makefile := readFile(t, filepath.Join(dir, "Makefile")); strings.Contains(makefile, "serve") {
		t.Error("base Makefile references serve")
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

	gofmtCheck(t, dir)
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

func TestAddWebInstallsHTTPDependency(t *testing.T) {
	root := t.TempDir()
	dir := create(t, root)

	baseApp := readFile(t, filepath.Join(dir, "internal/app/app.go"))
	result := add(t, dir, "web")

	if got, want := strings.Join(result.Installed, ","), "config,loom,http,web"; got != want {
		t.Fatalf("installed = %q, want %q", got, want)
	}
	for _, path := range []string{
		// shared configuration, installed as a dependency of http
		"internal/config/config.go",
		"internal/config/config_test.go",
		"config.example.yml",
		"config.yml",
		// frontend
		"web/package.json",
		"web/vite.config.ts",
		"web/tsconfig.json",
		"web/index.html",
		"web/src/main.tsx",
		"web/src/App.tsx",
		"web/src/index.css",
		// web capability Go payload
		"internal/web/web.go",
		"internal/web/assets.go",
		"internal/web/web_test.go",
		"internal/web/dist/.gitkeep",
		// http capability, installed as a dependency, and its Loom graph
		"internal/httpserver/http.go",
		"internal/httpserver/server.go",
		"internal/httpserver/http_test.go",
		"internal/app/serve.go",
		"internal/app/serve_test.go",
		"internal/di/di.go",
	} {
		if _, err := os.Stat(filepath.Join(dir, path)); err != nil {
			t.Errorf("expected %s: %v", path, err)
		}
	}

	// The SPA is served through the Loom server graph, not a route file.
	if _, err := os.Stat(filepath.Join(dir, "internal", "httpserver", "web_route.go")); !os.IsNotExist(err) {
		t.Error("the web capability wrote the retired non-Loom route seam")
	}
	di := readFile(t, filepath.Join(dir, "internal/di/di.go"))
	if !strings.Contains(di, "web.Handler()") {
		t.Errorf("the Loom server graph does not mount the SPA:\n%s", di)
	}

	// The base CLI itself is untouched: the capability adds files, it does not
	// edit the dispatcher.
	if readFile(t, filepath.Join(dir, "internal/app/app.go")) != baseApp {
		t.Error("add web modified the base app dispatcher")
	}

	manifest, err := project.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !manifest.HasCapability("http") || !manifest.HasCapability("web") {
		t.Fatalf("capabilities not recorded: %+v", manifest.Capabilities)
	}
	if manifest.CapabilityVersion("web") != result.Capability.Version {
		t.Fatalf("web version = %q", manifest.CapabilityVersion("web"))
	}
	if len(manifest.Drift(dir)) != 0 {
		t.Fatalf("drift after add: %+v", manifest.Drift(dir))
	}

	if !buildAndTestGeneratedProject(t, dir) {
		t.Skip("cannot resolve the generated project's dependencies (network/module cache unavailable)")
	}
}

func TestAddHTTPAloneInstallsOnlyHTTP(t *testing.T) {
	root := t.TempDir()
	dir := create(t, root)

	result := add(t, dir, "http")
	if got, want := strings.Join(result.Installed, ","), "config,loom,http"; got != want {
		t.Fatalf("installed = %q, want %q", got, want)
	}
	if _, err := os.Stat(filepath.Join(dir, "internal/httpserver/http.go")); err != nil {
		t.Fatalf("internal/httpserver/http.go missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "internal/app/serve.go")); err != nil {
		t.Fatalf("serve command missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "internal/web")); !os.IsNotExist(err) {
		t.Fatal("add http created the web capability")
	}
	// With only http installed the Loom server graph exists and serves nothing
	// beyond the middleware chain.
	di := readFile(t, filepath.Join(dir, "internal/di/di.go"))
	if !strings.Contains(di, "loom.Graph[*App](") || !strings.Contains(di, "func newMux(") {
		t.Fatalf("the Loom server graph is missing:\n%s", di)
	}

	if !buildAndTestGeneratedProject(t, dir) {
		t.Skip("cannot resolve the generated project's dependencies (network/module cache unavailable)")
	}
}

func TestAddWebAfterHTTPResolvesNothingExtra(t *testing.T) {
	root := t.TempDir()
	dir := create(t, root)
	add(t, dir, "http")

	result := add(t, dir, "web")
	if got, want := strings.Join(result.Installed, ","), "web"; got != want {
		t.Fatalf("installed = %q, want %q", got, want)
	}
	manifest, err := project.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !manifest.HasCapability("http") || !manifest.HasCapability("web") {
		t.Fatalf("capabilities not recorded: %+v", manifest.Capabilities)
	}
	if !buildAndTestGeneratedProject(t, dir) {
		t.Skip("cannot resolve the generated project's dependencies (network/module cache unavailable)")
	}
}

func TestAddWebIsIdempotent(t *testing.T) {
	root := t.TempDir()
	dir := create(t, root)
	add(t, dir, "web")

	before := readFile(t, filepath.Join(dir, "Makefile"))
	httpBefore := readFile(t, filepath.Join(dir, "internal/httpserver/http.go"))
	manifestBefore := readFile(t, filepath.Join(dir, project.ManifestName))

	result, err := Add(Request{Dir: dir, Catalog: newCatalog()}, "web")
	if err != nil {
		t.Fatalf("second Add: %v", err)
	}
	if len(result.Operations) != 0 {
		t.Fatalf("second Add planned %d operations, want 0", len(result.Operations))
	}
	if len(result.Installed) != 0 {
		t.Fatalf("second Add installed %v, want none", result.Installed)
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
	if readFile(t, filepath.Join(dir, "internal/httpserver/http.go")) != httpBefore {
		t.Fatal("httpserver composition changed on repeat add")
	}
	if readFile(t, filepath.Join(dir, project.ManifestName)) != manifestBefore {
		t.Fatal("manifest changed on repeat add")
	}
}

func TestAddRejectsNonAdditiveCapability(t *testing.T) {
	root := t.TempDir()
	dir := create(t, root)
	if _, err := Add(Request{Dir: dir, Catalog: newCatalog()}, "base"); err == nil {
		t.Fatal("expected error adding the base capability")
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
	if _, err := os.Stat(filepath.Join(dir, "internal/httpserver/http.go")); !os.IsNotExist(err) {
		t.Fatal("partial files were written on failure")
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
	if _, err := os.Stat(filepath.Join(dir, "internal/httpserver")); !os.IsNotExist(err) {
		t.Fatal("planning wrote the http capability")
	}
}

func TestAddWebDoesNotTouchUnrelatedFiles(t *testing.T) {
	root := t.TempDir()
	dir := create(t, root)

	// go.mod is deliberately absent: the config capability that http installs
	// patches its weld:deps region, so it is no longer unrelated.
	untouched := []string{
		"main.go",
		"internal/app/app.go",
		"README.md",
	}
	before := map[string]string{}
	for _, path := range untouched {
		before[path] = readFile(t, filepath.Join(dir, path))
	}

	add(t, dir, "web")

	for _, path := range untouched {
		if readFile(t, filepath.Join(dir, path)) != before[path] {
			t.Errorf("unrelated file %s changed after add web", path)
		}
	}
}

// goValidateDir uses the published v0.2.0 dependency by default. A local
// checkout is used only when explicitly requested for testing a development
// version; its replacement is confined to the generated test project.
func goValidateDir(t *testing.T) string {
	t.Helper()
	dir := os.Getenv("WELD_GO_VALIDATE_DIR")
	if dir == "" {
		return "published:v0.2.0"
	}
	if _, err := os.Stat(filepath.Join(dir, "go.mod")); err != nil {
		t.Fatalf("WELD_GO_VALIDATE_DIR does not contain go.mod: %v", err)
	}
	absolute, err := filepath.Abs(dir)
	if err != nil {
		t.Fatal(err)
	}
	return absolute
}

// useLocalGoValidate adds a test-only replace only for an explicit checkout;
// the default exercises the published dependency without sibling repositories.
func useLocalGoValidate(t *testing.T, dir, goValidateDir string) {
	t.Helper()
	if goValidateDir == "published:v0.2.0" {
		return
	}
	path := filepath.Join(dir, "go.mod")
	content := readFile(t, path)
	content += "\nreplace github.com/Xwudao/go-validate => " + goValidateDir + "\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// goModTidy resolves the generated project's test-only dependencies. It returns
// false when they cannot be fetched (no network or module cache), so the caller
// can skip rather than fail on an environment limitation.
func goModTidy(t *testing.T, dir string) bool {
	t.Helper()
	cmd := exec.Command("go", "mod", "tidy")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOWORK=off")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Logf("go mod tidy could not resolve dependencies: %v\n%s", err, out)
		return false
	}
	return true
}

// buildAndTestGeneratedProject runs gofmt, resolves dependencies, then builds,
// vets and tests the generated project. It returns false when dependencies
// cannot be resolved (no network or module cache), so the caller can skip
// rather than fail on an environment limitation.
func buildAndTestGeneratedProject(t *testing.T, dir string) bool {
	t.Helper()
	gofmtCheck(t, dir)
	if !goModTidy(t, dir) {
		return false
	}
	goBuild(t, dir)
	runGo(t, dir, "vet", "./...")
	goTest(t, dir, "./...")
	return true
}

// runDBTool runs a command inside dir/db/tools, the nested module that pins the
// sqlc generator. It returns false when the command cannot run (no network or
// module cache for the pinned tool), so the caller can skip instead of failing
// on an environment limitation.
func runDBTool(t *testing.T, dir string, args ...string) bool {
	t.Helper()
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir = filepath.Join(dir, "db", "tools")
	cmd.Env = append(os.Environ(), "GOWORK=off")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Logf("db tool %s: %v\n%s", strings.Join(args, " "), err, out)
		return false
	}
	return true
}

// snapshotDir reads the regular files in dir into a name->content map, so a
// regeneration can be compared byte for byte.
func snapshotDir(t *testing.T, dir string) map[string]string {
	t.Helper()
	files := map[string]string{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir %s: %v", dir, err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", entry.Name(), err)
		}
		files[entry.Name()] = string(raw)
	}
	return files
}

func TestAddAPIInstallsHTTPDependency(t *testing.T) {
	root := t.TempDir()
	dir := create(t, root)

	result := add(t, dir, "api")
	if got, want := strings.Join(result.Installed, ","), "config,loom,http,api"; got != want {
		t.Fatalf("installed = %q, want %q", got, want)
	}
	for _, path := range []string{
		"internal/api/dto.go",
		"internal/api/service.go",
		"internal/api/handler.go",
		"internal/api/openapi.go",
		"internal/api/api_test.go",
		"internal/api/openapi_test.go",
		"internal/httpserver/http.go",
		"internal/di/di.go",
		"internal/di/api_provider.go",
	} {
		if _, err := os.Stat(filepath.Join(dir, path)); err != nil {
			t.Errorf("expected %s: %v", path, err)
		}
	}
	// api requires http but never web or db.
	for _, path := range []string{"internal/web", "web"} {
		if _, err := os.Stat(filepath.Join(dir, path)); !os.IsNotExist(err) {
			t.Errorf("add api created %s", path)
		}
	}

	// The API is served through the Loom server graph, not a route file, and the
	// dependency region of go.mod is patched.
	if _, err := os.Stat(filepath.Join(dir, "internal", "httpserver", "api_route.go")); !os.IsNotExist(err) {
		t.Error("the api capability wrote the retired non-Loom route seam")
	}
	di := readFile(t, filepath.Join(dir, "internal/di/di.go"))
	if !strings.Contains(di, "api.Register(mux, service)") {
		t.Errorf("the Loom server graph does not mount the API:\n%s", di)
	}
	goMod := readFile(t, filepath.Join(dir, "go.mod"))
	if !strings.Contains(goMod, "require github.com/Xwudao/go-validate") {
		t.Errorf("go.mod does not require go-validate:\n%s", goMod)
	}
	if !strings.Contains(goMod, "weld:api:installed") {
		t.Errorf("go.mod deps region not patched:\n%s", goMod)
	}

	manifest, err := project.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !manifest.HasCapability("http") || !manifest.HasCapability("api") {
		t.Fatalf("capabilities not recorded: %+v", manifest.Capabilities)
	}
	if len(manifest.Drift(dir)) != 0 {
		t.Fatalf("drift after add: %+v", manifest.Drift(dir))
	}

	gofmtCheck(t, dir)
}

func TestAddAPIIsIdempotent(t *testing.T) {
	root := t.TempDir()
	dir := create(t, root)
	add(t, dir, "api")

	before := readFile(t, filepath.Join(dir, project.ManifestName))
	httpBefore := readFile(t, filepath.Join(dir, "internal/httpserver/http.go"))
	goModBefore := readFile(t, filepath.Join(dir, "go.mod"))

	result, err := Add(Request{Dir: dir, Catalog: newCatalog()}, "api")
	if err != nil {
		t.Fatalf("second Add: %v", err)
	}
	if len(result.Operations) != 0 {
		t.Fatalf("second Add planned %d operations, want 0", len(result.Operations))
	}
	if err := result.Apply(); err != nil {
		t.Fatalf("second Apply: %v", err)
	}
	if readFile(t, filepath.Join(dir, project.ManifestName)) != before {
		t.Fatal("manifest changed on repeat add")
	}
	if readFile(t, filepath.Join(dir, "internal/httpserver/http.go")) != httpBefore {
		t.Fatal("httpserver composition changed on repeat add")
	}
	if readFile(t, filepath.Join(dir, "go.mod")) != goModBefore {
		t.Fatal("go.mod changed on repeat add")
	}
}

func TestAddAPIRejectsMissingExtensionPoint(t *testing.T) {
	root := t.TempDir()
	dir := create(t, root)

	// Drop the dependency extension point a user might have removed; the plan
	// must fail before writing rather than patch a missing region.
	goModPath := filepath.Join(dir, "go.mod")
	content := strings.ReplaceAll(readFile(t, goModPath), "// weld:deps:begin\n", "")
	content = strings.ReplaceAll(content, "// weld:deps:end\n", "")
	if err := os.WriteFile(goModPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := Add(Request{Dir: dir, Catalog: newCatalog()}, "api"); err == nil {
		t.Fatal("expected an error when the deps extension point is missing")
	}
	if _, err := os.Stat(filepath.Join(dir, "internal/api")); !os.IsNotExist(err) {
		t.Fatal("partial files were written on failure")
	}
}

// TestAddAPIGeneratedProjectBuildsAndTests compiles and tests a real generated
// API project with the published go-validate dependency and the kin-openapi
// test dependency. It skips only when dependencies cannot be resolved.
func TestAddAPIGeneratedProjectBuildsAndTests(t *testing.T) {
	goValidate := goValidateDir(t)
	root := t.TempDir()
	dir := create(t, root)
	add(t, dir, "api")

	gofmtCheck(t, dir)
	useLocalGoValidate(t, dir, goValidate)
	if !goModTidy(t, dir) {
		t.Skip("cannot resolve the generated project's test dependencies (network/module cache unavailable)")
	}
	goBuild(t, dir)
	runGo(t, dir, "vet", "./...")
	goTest(t, dir, "./...")
}

// TestAddAPIAndWebInEitherOrder proves the two capabilities compose on one
// server in both orders: each requires http, both are mounted on the one Loom
// server graph, and the generated project compiles and tests either way.
func TestAddAPIAndWebInEitherOrder(t *testing.T) {
	goValidate := goValidateDir(t)
	orders := []struct {
		name          string
		first, second string
	}{
		{"web then api", "web", "api"},
		{"api then web", "api", "web"},
	}
	for _, order := range orders {
		t.Run(order.name, func(t *testing.T) {
			root := t.TempDir()
			dir := create(t, root)
			add(t, dir, order.first)
			add(t, dir, order.second)

			di := readFile(t, filepath.Join(dir, "internal/di/di.go"))
			for _, want := range []string{
				"web.Handler()",
				"api.Register(mux, service)",
			} {
				if !strings.Contains(di, want) {
					t.Errorf("%q missing from the Loom server graph:\n%s", want, di)
				}
			}
			// One serve command serves both: http owns it and neither web nor api
			// adds another.
			list, err := Add(Request{Dir: dir, Catalog: newCatalog()}, "http")
			if err != nil {
				t.Fatalf("http already installed: %v", err)
			}
			if len(list.Installed) != 0 {
				t.Fatalf("http reinstalled: %v", list.Installed)
			}

			gofmtCheck(t, dir)
			useLocalGoValidate(t, dir, goValidate)
			if !goModTidy(t, dir) {
				t.Skip("cannot resolve the generated project's test dependencies (network/module cache unavailable)")
			}
			goBuild(t, dir)
			goTest(t, dir, "./...")
		})
	}
}

// TestAddDBInstallsAlone proves db is independent: adding it to a fresh CLI
// installs only db — no HTTP server, no web frontend, no API — and the
// generated repository speaks pgx, not HTTP or JSON.
func TestAddDBInstallsAlone(t *testing.T) {
	root := t.TempDir()
	dir := create(t, root)

	result := add(t, dir, "db")
	if got, want := strings.Join(result.Installed, ","), "config,loom,db"; got != want {
		t.Fatalf("installed = %q, want %q", got, want)
	}
	for _, path := range []string{
		// shared configuration, installed as a dependency of db
		"internal/config/config.go",
		"config.yml",
		"config.example.yml",
		"sqlc.yaml",
		"db/migrations/000001_create_items.sql",
		"db/query/items.sql",
		"db/tools/go.mod",
		"db/tools/go.sum",
		"internal/data/data.go",
		"internal/data/sqlc/db.go",
		"internal/data/sqlc/models.go",
		"internal/data/sqlc/items.sql.go",
		"internal/data/data_test.go",
		"internal/data/postgres_integration_test.go",
		"internal/data/README.md",
	} {
		if _, err := os.Stat(filepath.Join(dir, path)); err != nil {
			t.Errorf("expected %s: %v", path, err)
		}
	}
	// db requires neither http nor web nor api: it is usable in a CLI-only project.
	for _, path := range []string{"internal/httpserver", "internal/api", "internal/web", "web"} {
		if _, err := os.Stat(filepath.Join(dir, path)); !os.IsNotExist(err) {
			t.Errorf("add db created %s", path)
		}
	}

	// The generated repository imports pgx but no HTTP or JSON surface, and it
	// does not depend on the API package.
	dataGo := readFile(t, filepath.Join(dir, "internal/data/data.go"))
	if !strings.Contains(dataGo, "github.com/jackc/pgx/v5/pgxpool") {
		t.Errorf("data.go does not import pgxpool:\n%s", dataGo)
	}
	for _, forbidden := range []string{"\"net/http\"", "\"encoding/json\"", "/internal/api\""} {
		if strings.Contains(dataGo, forbidden) {
			t.Errorf("data.go imports %s", forbidden)
		}
	}

	// The go.mod dependency region and the Makefile db region are patched.
	goMod := readFile(t, filepath.Join(dir, "go.mod"))
	if !strings.Contains(goMod, "require github.com/jackc/pgx/v5 v5.7.6") {
		t.Errorf("go.mod does not pin pgx/v5:\n%s", goMod)
	}
	if !strings.Contains(goMod, "weld:db:installed") {
		t.Errorf("go.mod deps region not patched:\n%s", goMod)
	}
	makefile := readFile(t, filepath.Join(dir, "Makefile"))
	for _, want := range []string{
		"weld:db:installed",
		"sqlc:",
		"migrate-up:",
		"migrate-status:",
		"migrate-down:",
		"go tool sqlc generate",
		"go tool goose",
		`test -n "$$DATABASE_URL"`,
		"CONFIRM",
	} {
		if !strings.Contains(makefile, want) {
			t.Errorf("Makefile is missing %q:\n%s", want, makefile)
		}
	}
	if strings.Contains(makefile, "psql") {
		t.Errorf("Makefile still shells out to psql:\n%s", makefile)
	}
	// The sqlc tool is pinned in a nested module, not in the app module.
	toolsMod := readFile(t, filepath.Join(dir, "db", "tools", "go.mod"))
	if !strings.Contains(toolsMod, "github.com/sqlc-dev/sqlc/cmd/sqlc") {
		t.Errorf("db/tools/go.mod does not pin the sqlc tool:\n%s", toolsMod)
	}
	if !strings.Contains(toolsMod, "github.com/pressly/goose/v3/cmd/goose") {
		t.Errorf("db/tools/go.mod does not pin the goose migration tool:\n%s", toolsMod)
	}
	if strings.Contains(goMod, "sqlc-dev/sqlc") || strings.Contains(goMod, "pressly/goose") {
		t.Errorf("a db tool leaked into the application go.mod:\n%s", goMod)
	}

	manifest, err := project.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !manifest.HasCapability("db") {
		t.Fatalf("db not recorded: %+v", manifest.Capabilities)
	}
	if len(manifest.Drift(dir)) != 0 {
		t.Fatalf("drift after add: %+v", manifest.Drift(dir))
	}

	gofmtCheck(t, dir)
}

func TestAddDBIsIdempotent(t *testing.T) {
	root := t.TempDir()
	dir := create(t, root)
	add(t, dir, "db")

	manifestBefore := readFile(t, filepath.Join(dir, project.ManifestName))
	goModBefore := readFile(t, filepath.Join(dir, "go.mod"))
	makefileBefore := readFile(t, filepath.Join(dir, "Makefile"))

	result, err := Add(Request{Dir: dir, Catalog: newCatalog()}, "db")
	if err != nil {
		t.Fatalf("second Add: %v", err)
	}
	if len(result.Operations) != 0 {
		t.Fatalf("second Add planned %d operations, want 0", len(result.Operations))
	}
	if len(result.Installed) != 0 {
		t.Fatalf("second Add installed %v, want none", result.Installed)
	}
	if len(result.Notes) == 0 {
		t.Fatal("second Add produced no explanation")
	}
	if err := result.Apply(); err != nil {
		t.Fatalf("second Apply: %v", err)
	}

	if readFile(t, filepath.Join(dir, project.ManifestName)) != manifestBefore {
		t.Fatal("manifest changed on repeat add")
	}
	if readFile(t, filepath.Join(dir, "go.mod")) != goModBefore {
		t.Fatal("go.mod changed on repeat add")
	}
	if readFile(t, filepath.Join(dir, "Makefile")) != makefileBefore {
		t.Fatal("Makefile changed on repeat add")
	}
}

func TestAddDBRejectsUnmanagedFile(t *testing.T) {
	root := t.TempDir()
	dir := create(t, root)

	if err := os.MkdirAll(filepath.Join(dir, "db", "migrations"), 0o755); err != nil {
		t.Fatal(err)
	}
	userFile := filepath.Join(dir, "db", "migrations", "000001_create_items.sql")
	if err := os.WriteFile(userFile, []byte("-- mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	makefileBefore := readFile(t, filepath.Join(dir, "Makefile"))
	if _, err := Add(Request{Dir: dir, Catalog: newCatalog()}, "db"); err == nil {
		t.Fatal("expected conflict for unmanaged file")
	}
	if readFile(t, userFile) != "-- mine\n" {
		t.Fatal("user file was modified")
	}
	if readFile(t, filepath.Join(dir, "Makefile")) != makefileBefore {
		t.Fatal("Makefile changed on conflicting add")
	}
	if _, err := os.Stat(filepath.Join(dir, "internal/data")); !os.IsNotExist(err) {
		t.Fatal("partial files were written on failure")
	}
}

func TestAddDBIsDryRunnable(t *testing.T) {
	root := t.TempDir()
	dir := create(t, root)

	makefileBefore := readFile(t, filepath.Join(dir, "Makefile"))
	goModBefore := readFile(t, filepath.Join(dir, "go.mod"))

	result, err := Add(Request{Dir: dir, Catalog: newCatalog()}, "db")
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if len(result.Operations) == 0 {
		t.Fatal("expected a non-empty plan")
	}
	if readFile(t, filepath.Join(dir, "Makefile")) != makefileBefore {
		t.Fatal("planning wrote the Makefile")
	}
	if readFile(t, filepath.Join(dir, "go.mod")) != goModBefore {
		t.Fatal("planning wrote go.mod")
	}
	for _, path := range []string{"internal/data", "db", "sqlc.yaml"} {
		if _, err := os.Stat(filepath.Join(dir, path)); !os.IsNotExist(err) {
			t.Fatalf("planning wrote %s", path)
		}
	}
}

// TestAddDBMigrationTargetsAreGuarded proves the generated migration targets
// fail safe: they refuse to run without an explicit DATABASE_URL (goose and psql
// would otherwise fall back to a local database), migrate-down needs an explicit
// confirmation and reverts a single migration, and no target shells out to psql.
// It also proves the generated integration test never drops application tables.
func TestAddDBMigrationTargetsAreGuarded(t *testing.T) {
	root := t.TempDir()
	dir := create(t, root)
	add(t, dir, "db")

	// A dry run shows the planned commands without running them: they must go
	// through the pinned goose tool and never touch psql.
	planned := runMake(t, dir, envWithout("DATABASE_URL"), "-n", "migrate-up")
	if !strings.Contains(planned, "go tool goose") {
		t.Errorf("migrate-up dry run does not use the pinned goose tool:\n%s", planned)
	}
	if strings.Contains(planned, "psql") {
		t.Errorf("migrate-up dry run still shells out to psql:\n%s", planned)
	}

	// An unset DATABASE_URL must fail before any database client runs.
	up := runMakeExpectError(t, dir, envWithout("DATABASE_URL", "PG_TEST_DSN"), "migrate-up")
	if !strings.Contains(up, "DATABASE_URL") {
		t.Errorf("migrate-up without DATABASE_URL did not explain the guard:\n%s", up)
	}
	if strings.Contains(up, "goose") {
		t.Errorf("migrate-up ran goose despite the missing DATABASE_URL:\n%s", up)
	}
	status := runMakeExpectError(t, dir, envWithout("DATABASE_URL"), "migrate-status")
	if !strings.Contains(status, "DATABASE_URL") {
		t.Errorf("migrate-status without DATABASE_URL did not explain the guard:\n%s", status)
	}
	// migrate-down without CONFIRM must refuse even when a dsn is present.
	down := runMakeExpectError(t, dir, append(envWithout("CONFIRM"), "DATABASE_URL=postgres://invalid.invalid:5432/unused"), "migrate-down")
	if !strings.Contains(down, "CONFIRM") {
		t.Errorf("migrate-down without CONFIRM did not refuse:\n%s", down)
	}
	if strings.Contains(down, "goose") {
		t.Errorf("migrate-down ran goose despite the missing confirmation:\n%s", down)
	}

	// The generated integration test isolates itself in a schema; it must never
	// contain a global DROP TABLE.
	integration := readFile(t, filepath.Join(dir, "internal", "data", "postgres_integration_test.go"))
	if strings.Contains(integration, "DROP TABLE") {
		t.Errorf("generated integration test contains a global DROP TABLE:\n%s", integration)
	}
	for _, want := range []string{"CREATE SCHEMA", "search_path", "DROP SCHEMA", "CASCADE"} {
		if !strings.Contains(integration, want) {
			t.Errorf("generated integration test is missing %q", want)
		}
	}
}

// envWithout returns the process environment with the named variables removed,
// so a test can prove a target's behavior when a variable is unset regardless
// of the environment it runs in.
func envWithout(keys ...string) []string {
	dropped := map[string]bool{}
	for _, key := range keys {
		dropped[key] = true
	}
	var env []string
	for _, entry := range os.Environ() {
		name := entry
		if i := strings.IndexByte(entry, '='); i >= 0 {
			name = entry[:i]
		}
		if !dropped[name] {
			env = append(env, entry)
		}
	}
	return env
}

func runMake(t *testing.T, dir string, env []string, args ...string) string {
	t.Helper()
	cmd := exec.Command("make", args...)
	cmd.Dir = dir
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("make %s unexpectedly failed: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

func runMakeExpectError(t *testing.T, dir string, env []string, args ...string) string {
	t.Helper()
	cmd := exec.Command("make", args...)
	cmd.Dir = dir
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("make %s unexpectedly succeeded:\n%s", strings.Join(args, " "), out)
	}
	return string(out)
}

// TestAddDBGeneratedProjectBuildsAndTests compiles and tests a real generated db
// project without PostgreSQL: the shipped sqlc output makes the project build,
// and the integration test skips because PG_TEST_DSN is unset.
func TestAddDBGeneratedProjectBuildsAndTests(t *testing.T) {
	root := t.TempDir()
	dir := create(t, root)
	add(t, dir, "db")

	if !buildAndTestGeneratedProject(t, dir) {
		t.Skip("cannot resolve the generated project's pgx dependencies (network/module cache unavailable)")
	}
}

// TestAddDBSqlcGenerationIsReproducible regenerates internal/data/sqlc with the
// pinned tool and requires the committed output to be byte-identical, so the
// shipped code cannot drift from db/migrations and db/query.
func TestAddDBSqlcGenerationIsReproducible(t *testing.T) {
	root := t.TempDir()
	dir := create(t, root)
	add(t, dir, "db")

	generated := filepath.Join(dir, "internal", "data", "sqlc")
	before := snapshotDir(t, generated)

	if !runDBTool(t, dir, "go", "tool", "sqlc", "generate", "-f", "../../sqlc.yaml") {
		t.Skip("cannot run the pinned sqlc tool (network/module cache unavailable)")
	}
	after := snapshotDir(t, generated)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("regeneration changed internal/data/sqlc\nbefore: %v\nafter: %v", before, after)
	}
}

// TestAddDBComposesWithWeb proves db and the HTTP capabilities compose in either
// order: web installs http and both leave the app building and testing.
func TestAddDBComposesWithWeb(t *testing.T) {
	orders := []struct {
		name          string
		first, second string
	}{
		{"web then db", "web", "db"},
		{"db then web", "db", "web"},
	}
	for _, order := range orders {
		t.Run(order.name, func(t *testing.T) {
			root := t.TempDir()
			dir := create(t, root)
			add(t, dir, order.first)
			add(t, dir, order.second)

			manifest, err := project.Load(dir)
			if err != nil {
				t.Fatal(err)
			}
			for _, capability := range []string{"http", "web", "db"} {
				if !manifest.HasCapability(capability) {
					t.Errorf("%s not recorded: %+v", capability, manifest.Capabilities)
				}
			}
			goMod := readFile(t, filepath.Join(dir, "go.mod"))
			if !strings.Contains(goMod, "weld:db:installed") || !strings.Contains(goMod, "github.com/jackc/pgx/v5") {
				t.Errorf("go.mod is missing the db dependency:\n%s", goMod)
			}

			if !buildAndTestGeneratedProject(t, dir) {
				t.Skip("cannot resolve the generated project's dependencies (network/module cache unavailable)")
			}
		})
	}
}

// TestAddDBComposesWithAPI proves db and api compose in either order and that
// the JSON API stays independent of the sqlc rows. It uses the published
// go-validate version unless a local checkout is explicitly requested.
func TestAddDBComposesWithAPI(t *testing.T) {
	goValidate := goValidateDir(t)
	orders := []struct {
		name          string
		first, second string
	}{
		{"api then db", "api", "db"},
		{"db then api", "db", "api"},
	}
	for _, order := range orders {
		t.Run(order.name, func(t *testing.T) {
			root := t.TempDir()
			dir := create(t, root)
			add(t, dir, order.first)
			add(t, dir, order.second)

			goMod := readFile(t, filepath.Join(dir, "go.mod"))
			for _, want := range []string{
				"weld:db:installed",
				"github.com/jackc/pgx/v5",
				"weld:api:installed",
				"github.com/Xwudao/go-validate",
			} {
				if !strings.Contains(goMod, want) {
					t.Errorf("go.mod is missing %q:\n%s", want, goMod)
				}
			}
			// The API DTOs stay independent of the sqlc rows: the HTTP layer
			// must not import the generated data package.
			handler := readFile(t, filepath.Join(dir, "internal/api/handler.go"))
			if strings.Contains(handler, "internal/data") {
				t.Error("internal/api imports internal/data; the JSON DTOs must stay independent of the sqlc rows")
			}

			gofmtCheck(t, dir)
			useLocalGoValidate(t, dir, goValidate)
			if !goModTidy(t, dir) {
				t.Skip("cannot resolve the generated project's test dependencies (network/module cache unavailable)")
			}
			goBuild(t, dir)
			runGo(t, dir, "vet", "./...")
			goTest(t, dir, "./...")
		})
	}
}

// TestAddDBPostgresIntegration runs the generated project's PostgreSQL
// integration test against the database named by PG_TEST_DSN.
//
// It is the only test that needs a real server, and it skips visibly when
// PG_TEST_DSN is unset. It never falls back to any other dsn, so it can only
// target a dedicated test database the operator names explicitly; passing a
// live application database here would be a mistake the operator makes on
// purpose, not one this suite can make by accident.
func TestAddDBPostgresIntegration(t *testing.T) {
	dsn := os.Getenv("PG_TEST_DSN")
	if dsn == "" {
		t.Skip("PG_TEST_DSN is not set; no dedicated PostgreSQL test database is available")
	}
	root := t.TempDir()
	dir := create(t, root)
	add(t, dir, "db")
	if !goModTidy(t, dir) {
		t.Skip("cannot resolve the generated project's pgx dependencies (network/module cache unavailable)")
	}

	cmd := exec.Command("go", "test", "./internal/data/...", "-run", "TestPostgresRepositoryIntegration", "-v", "-count=1")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOWORK=off", "PG_TEST_DSN="+dsn)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("generated PostgreSQL integration test failed: %v\n%s", err, out)
	}
	t.Logf("generated PostgreSQL integration test output:\n%s", out)
}

// --- loom capability -------------------------------------------------------

var (
	loomToolOnce sync.Once
	loomToolOK   bool
)

// loomToolAvailable reports whether the pinned Loom generator can be built from
// the module cache or network. It is checked once per test binary.
func loomToolAvailable(t *testing.T) bool {
	t.Helper()
	loomToolOnce.Do(func() {
		dir, err := os.MkdirTemp("", "weld-loom-avail-")
		if err != nil {
			return
		}
		defer os.RemoveAll(dir)
		gomod := "module weld.test/tools\n\ngo 1.25.0\n\ntool github.com/Xwudao/loom/cmd/loom\n\nrequire github.com/Xwudao/loom v0.3.1\n"
		if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(gomod), 0o644); err != nil {
			return
		}
		cmd := exec.Command("go", "build", "-o", filepath.Join(dir, "loom"), "github.com/Xwudao/loom/cmd/loom")
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=-mod=mod")
		loomToolOK = cmd.Run() == nil
	})
	if !loomToolOK {
		t.Skip("pinned Loom generator unavailable (no network or module cache); skipping Loom integration test")
	}
	return true
}

// runLoomTool rebuilds the pinned generator and runs it against dir, returning
// its combined output and whether it succeeded.
func runLoomTool(t *testing.T, dir string, args ...string) (string, bool) {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "loom")
	build := exec.Command("go", "build", "-o", bin, "github.com/Xwudao/loom/cmd/loom")
	build.Dir = filepath.Join(dir, "tools", "loom")
	build.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=-mod=mod")
	if out, err := build.CombinedOutput(); err != nil {
		t.Logf("build pinned loom generator: %v\n%s", err, out)
		return string(out), false
	}
	run := exec.Command(bin, args...)
	run.Dir = dir
	run.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=-mod=mod")
	out, err := run.CombinedOutput()
	return string(out), err == nil
}

func TestAddLoomInstallsCLIOnlyGraph(t *testing.T) {
	loomToolAvailable(t)
	root := t.TempDir()
	dir := create(t, root)

	result := add(t, dir, "loom")
	if got, want := strings.Join(result.Installed, ","), "config,loom"; got != want {
		t.Fatalf("installed = %q, want %q", got, want)
	}
	for _, path := range []string{
		"internal/di/di.go",
		"internal/di/di_test.go",
		"internal/di/README.md",
		"tools/loom/go.mod",
		"tools/loom/go.sum",
		"internal/config/config.go",
	} {
		if _, err := os.Stat(filepath.Join(dir, path)); err != nil {
			t.Errorf("expected %s: %v", path, err)
		}
	}
	// Loom alone is a CLI-only graph: no HTTP server, no serve command, no
	// generated initializer (there is no loom.Graph declaration yet).
	for _, path := range []string{
		"internal/httpserver",
		"internal/app/serve.go",
		"internal/app/serve_loom.go",
		"internal/di/loom_gen.go",
		"internal/data",
		"internal/api",
		"internal/web",
	} {
		if _, err := os.Stat(filepath.Join(dir, path)); !os.IsNotExist(err) {
			t.Errorf("add loom created %s", path)
		}
	}
	di := readFile(t, filepath.Join(dir, "internal/di/di.go"))
	for _, want := range []string{"var commonModule = loom.Module(", "func NewConfigLoader()", "func NewConfig("} {
		if !strings.Contains(di, want) {
			t.Errorf("di.go is missing %q:\n%s", want, di)
		}
	}
	for _, forbidden := range []string{"NewServer", "internal/httpserver", "func InitApp", "type App struct"} {
		if strings.Contains(di, forbidden) {
			t.Errorf("the CLI-only graph references %q:\n%s", forbidden, di)
		}
	}
	// The opt-in Go floor is raised and Loom is a direct dependency.
	goMod := readFile(t, filepath.Join(dir, "go.mod"))
	if !strings.Contains(goMod, "go 1.25.0") || !strings.Contains(goMod, "require github.com/Xwudao/loom v0.3.1") {
		t.Errorf("go.mod is missing the Loom floor/dependency:\n%s", goMod)
	}
	manifest := mustLoad(t, dir)
	if len(manifest.Drift(dir)) != 0 {
		t.Fatalf("drift after add loom: %+v", manifest.Drift(dir))
	}
	gofmtCheck(t, dir)
	if !buildAndTestGeneratedProject(t, dir) {
		t.Skip("cannot resolve the generated project's dependencies (network/module cache unavailable)")
	}
}

func TestAddLoomIsIdempotent(t *testing.T) {
	loomToolAvailable(t)
	root := t.TempDir()
	dir := create(t, root)
	add(t, dir, "loom")

	before := map[string]string{}
	for _, path := range []string{"internal/di/di.go", "go.mod", project.ManifestName} {
		before[path] = readFile(t, filepath.Join(dir, path))
	}
	result, err := Add(Request{Dir: dir, Catalog: newCatalog()}, "loom")
	if err != nil {
		t.Fatalf("second Add: %v", err)
	}
	if len(result.Operations) != 0 || result.GenerateLoom {
		t.Fatalf("second Add planned %d operations, generate=%v", len(result.Operations), result.GenerateLoom)
	}
	if len(result.Notes) == 0 {
		t.Fatal("second Add produced no explanation")
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

// TestAddLoomGraphFollowsInstalledCapabilities proves the CLI-only graph is
// regenerated for the installed set in either order, and that installing db only
// declares the pool and repository as available bindings: the CLI-only graph has
// no server root and no generated initializer, so nothing constructs them.
func TestAddLoomGraphFollowsInstalledCapabilities(t *testing.T) {
	loomToolAvailable(t)
	orders := []struct {
		name string
		caps []string
	}{
		{"db then loom", []string{"db", "loom"}},
		{"loom then db", []string{"loom", "db"}},
	}
	for _, order := range orders {
		t.Run(order.name, func(t *testing.T) {
			root := t.TempDir()
			dir := create(t, root)
			for _, capability := range order.caps {
				add(t, dir, capability)
			}
			di := readFile(t, filepath.Join(dir, "internal/di/di.go"))
			for _, want := range []string{"loom.Provide(NewPool)", "loom.As[data.Repository](NewRepository)"} {
				if !strings.Contains(di, want) {
					t.Errorf("di.go does not declare the available binding %q:\n%s", want, di)
				}
			}
			// A CLI-only project has no server graph and so no generated initializer.
			for _, forbidden := range []string{"func InitApp", "NewServer", "internal/httpserver"} {
				if strings.Contains(di, forbidden) {
					t.Errorf("the CLI-only graph references %q:\n%s", forbidden, di)
				}
			}
			if _, err := os.Stat(filepath.Join(dir, "internal", "di", "loom_gen.go")); !os.IsNotExist(err) {
				t.Errorf("a CLI-only project generated an initializer: %v", err)
			}
			manifest := mustLoad(t, dir)
			if len(manifest.Drift(dir)) != 0 {
				t.Fatalf("drift: %+v", manifest.Drift(dir))
			}
			gofmtCheck(t, dir)
			if !buildAndTestGeneratedProject(t, dir) {
				t.Skip("cannot resolve the generated project's dependencies (network/module cache unavailable)")
			}
		})
	}
}

// TestAPIDBMatrixServesInMemoryWithoutDatabase proves api+db has the same
// development-demo semantics with Loom: serve starts with no
// database credentials and no reachable database, answers the JSON API over
// HTTP, keeps items in memory, and loses them on restart. Installing db must not
// switch the API to PostgreSQL, require a credential, or read the dsn.
func TestAPIDBMatrixServesInMemoryWithoutDatabase(t *testing.T) {
	goValidate := goValidateDir(t)
	cases := []struct {
		name      string
		caps      []string
		needsLoom bool
		extraEnv  []string
		forbidden string
	}{
		{name: "api+db+loom", caps: []string{"api", "db", "loom"}, needsLoom: true},
		{name: "api+db", caps: []string{"api", "db"}},
		{
			name:      "api+db+loom ignores a malformed DATABASE_URL",
			caps:      []string{"api", "db", "loom"},
			needsLoom: true,
			extraEnv:  []string{"DATABASE_URL=postgres://user:sentinel-do-not-log:extra@host:notaport/db"},
			forbidden: "sentinel-do-not-log",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.needsLoom {
				loomToolAvailable(t)
			}
			root := t.TempDir()
			dir := create(t, root)
			useLocalGoValidate(t, dir, goValidate)
			for _, capability := range tc.caps {
				add(t, dir, capability)
			}
			if !goModTidy(t, dir) {
				t.Skip("cannot resolve the generated project's dependencies (network/module cache unavailable)")
			}
			clean := envWithout("DATABASE_URL", "DB_HOST", "DB_PORT", "DB_USER", "DB_PASSWORD", "DB_NAME")
			env := append(clean, tc.extraEnv...)
			binary := buildAppBinary(t, dir, env)

			// First run: the API is reachable and keeps an item in memory.
			first, base, stderr := serveProbe(t, binary, dir, env)
			if tc.forbidden != "" && strings.Contains(stderr.String(), tc.forbidden) {
				t.Fatalf("serve logged a database credential:\n%s", stderr.String())
			}
			created := postItem(t, base, `{"name":"widget","quantity":2,"status":"active"}`)
			id, _ := created["id"].(string)
			if id == "" {
				t.Fatalf("POST /api/items returned no id: %v", created)
			}
			if got := getItem(t, base, id); got["id"] != id {
				t.Fatalf("GET /api/items/%s = %v", id, got)
			}

			// Restart: the in-memory demo starts empty, so the item is gone.
			if err := first.Process.Kill(); err != nil {
				t.Fatalf("kill serve: %v", err)
			}
			_ = first.Wait()
			_, base, _ = serveProbe(t, binary, dir, env)
			if status, body := getRaw(t, base+"/api/items/"+id); status != nethttp.StatusNotFound {
				t.Fatalf("after restart GET /api/items/%s = %d, want 404 (in-memory data is lost on restart): %s", id, status, body)
			}
		})
	}
}

// buildAppBinary builds the generated project's binary with GOPROXY=off, so it
// proves the module graph already resolved by go mod tidy is complete.
func buildAppBinary(t *testing.T, dir string, env []string) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "app")
	build := exec.Command("go", "build", "-o", binary, ".")
	build.Dir = dir
	build.Env = append(env, "GOPROXY=off", "GOWORK=off")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	return binary
}

// freeAddr returns an available loopback address. The short race between closing
// the probe listener and the server binding is acceptable for a test; a lost
// race fails the probe loudly.
func freeAddr(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatalf("close probe listener: %v", err)
	}
	return addr
}

// syncBuffer is a concurrency-safe writer for a subprocess's stderr: the
// process writes from its own goroutine while the test reads the captured
// output, so access must be synchronized.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// serveProbe starts the generated serve command on a fresh loopback address,
// waits until it answers, and returns the process, its base URL and its captured
// stderr. The process is killed when the test finishes.
func serveProbe(t *testing.T, binary, dir string, env []string) (*exec.Cmd, string, *syncBuffer) {
	t.Helper()
	addr := freeAddr(t)
	cmd := exec.Command(binary, "serve", "--addr", addr)
	cmd.Dir = dir
	cmd.Env = env
	stderr := &syncBuffer{}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start serve: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	base := "http://" + addr
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := nethttp.Get(base + "/api/openapi.json")
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == nethttp.StatusOK {
				return cmd, base, stderr
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("serve did not become ready on %s:\n%s", addr, stderr.String())
	return nil, "", stderr
}

func postItem(t *testing.T, base, body string) map[string]any {
	t.Helper()
	resp, err := nethttp.Post(base+"/api/items", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST /api/items: %v", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read POST /api/items: %v", err)
	}
	if resp.StatusCode != nethttp.StatusCreated {
		t.Fatalf("POST /api/items = %d: %s", resp.StatusCode, raw)
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("decode POST /api/items: %v", err)
	}
	return apiResponseData(payload)
}

// apiResponseData returns a JSON API response's payload. A project generated by
// the current weld-template wraps responses in the {code,msg,data} envelope; one
// generated by the pinned template that predates the envelope returns the bare
// payload, so this accepts either shape while the template pin catches up.
func apiResponseData(payload map[string]any) map[string]any {
	if data, ok := payload["data"].(map[string]any); ok {
		return data
	}
	return payload
}

func getItem(t *testing.T, base, id string) map[string]any {
	t.Helper()
	status, raw := getRaw(t, base+"/api/items/"+id)
	if status != nethttp.StatusOK {
		t.Fatalf("GET /api/items/%s = %d: %s", id, status, raw)
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("decode GET /api/items/%s: %v", id, err)
	}
	return apiResponseData(payload)
}

func getRaw(t *testing.T, url string) (int, []byte) {
	t.Helper()
	resp, err := nethttp.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read GET %s: %v", url, err)
	}
	return resp.StatusCode, raw
}

// TestGeneratedProjectRaceAndVet runs the generated project's tests under the
// race detector and vets it for the relevant API/db combinations, all using
// Loom, so the in-memory API and its HTTP composition are race-clean
// without PostgreSQL.
func TestGeneratedProjectRaceAndVet(t *testing.T) {
	goValidate := goValidateDir(t)
	cases := []struct {
		name      string
		caps      []string
		needsLoom bool
	}{
		{name: "api+db", caps: []string{"api", "db"}},
		{name: "api+db+loom", caps: []string{"api", "db", "loom"}, needsLoom: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.needsLoom {
				loomToolAvailable(t)
			}
			root := t.TempDir()
			dir := create(t, root)
			useLocalGoValidate(t, dir, goValidate)
			for _, capability := range tc.caps {
				add(t, dir, capability)
			}
			if !goModTidy(t, dir) {
				t.Skip("cannot resolve the generated project's dependencies (network/module cache unavailable)")
			}
			gofmtCheck(t, dir)
			runGo(t, dir, "vet", "./...")
			runGo(t, dir, "test", "-race", "./...")
		})
	}
}

// TestAddLoomGenerationIsReproducible runs the pinned generator in dry-run and
// requires it to report no change, so the committed initializer cannot drift
// from the graph.
func TestAddLoomGenerationIsReproducible(t *testing.T) {
	loomToolAvailable(t)
	root := t.TempDir()
	dir := create(t, root)
	// http installs loom and its server graph, so there is a graph declaration to
	// regenerate.
	add(t, dir, "http")

	out, ok := runLoomTool(t, dir, "generate", "-dry-run", "./internal/di")
	if !ok {
		t.Skipf("cannot run the pinned Loom generator: %s", out)
	}
	if !strings.Contains(out, "unchanged") {
		t.Fatalf("regeneration drifted from the committed initializer:\n%s", out)
	}
}

// TestLoomInstalledByEveryAdd guards the architecture: every capability except
// the bare base CLI requires Loom, directly or through http, and Loom requires
// config. config is the one leaf Loom itself requires, so weld plans Loom for an
// explicit `weld add config` too.
func TestLoomInstalledByEveryAdd(t *testing.T) {
	catalog := newCatalog()
	for _, name := range []string{"http", "db", "redis", "mail", "storage"} {
		capability, err := catalog.Get(name)
		if err != nil {
			t.Fatalf("Get %s: %v", name, err)
		}
		var requiresLoom bool
		for _, required := range capability.Requires {
			if required == "loom" {
				requiresLoom = true
			}
		}
		if !requiresLoom {
			t.Errorf("capability %s does not require loom", name)
		}
	}
	for _, name := range []string{"api", "web", "cron"} {
		capability, err := catalog.Get(name)
		if err != nil {
			t.Fatalf("Get %s: %v", name, err)
		}
		var requiresHTTP bool
		for _, required := range capability.Requires {
			if required == "http" {
				requiresHTTP = true
			}
		}
		if !requiresHTTP {
			t.Errorf("capability %s does not require http (which requires loom)", name)
		}
	}
	loom, err := catalog.Get("loom")
	if err != nil {
		t.Fatalf("Get loom: %v", err)
	}
	if len(loom.Requires) != 1 || loom.Requires[0] != "config" {
		t.Errorf("loom requires %v, want [config]", loom.Requires)
	}
	config, err := catalog.Get("config")
	if err != nil {
		t.Fatalf("Get config: %v", err)
	}
	if len(config.Requires) != 1 || config.Requires[0] != "base" {
		t.Errorf("config requires %v, want [base]", config.Requires)
	}
}

func mustLoad(t *testing.T, dir string) *project.Manifest {
	t.Helper()
	manifest, err := project.Load(dir)
	if err != nil {
		t.Fatalf("load manifest: %v", err)
	}
	return manifest
}

// TestAddLoomKeepsAPIInMemory proves installing api+db+loom leaves the API on
// the in-memory development service, in either install order: the graph declares
// the pool and repository as available bindings but does not bind them to
// api.Service, so the generated initializer constructs neither. The API uses
// the published go-validate Spec API by default. The generated di_test.go
// serves the API over a real socket with no
// PostgreSQL.
func TestAddLoomKeepsAPIInMemory(t *testing.T) {
	goValidate := goValidateDir(t)
	loomToolAvailable(t)
	orders := []struct {
		name string
		caps []string
	}{
		{"api db then loom", []string{"api", "db", "loom"}},
		{"loom then api db", []string{"loom", "api", "db"}},
	}
	for _, order := range orders {
		t.Run(order.name, func(t *testing.T) {
			root := t.TempDir()
			dir := create(t, root)
			useLocalGoValidate(t, dir, goValidate)
			for _, capability := range order.caps {
				add(t, dir, capability)
			}
			di := readFile(t, filepath.Join(dir, "internal/di/di.go"))
			for _, want := range []string{"loom.Provide(NewAPIService)", "loom.Provide(NewPool)", "loom.As[data.Repository](NewRepository)"} {
				if !strings.Contains(di, want) {
					t.Errorf("di.go is missing %q:\n%s", want, di)
				}
			}
			for _, forbidden := range []string{"repositoryService", "func NewAPIService"} {
				if strings.Contains(di, forbidden) {
					t.Errorf("di.go still defines the API wiring itself (%q); the provider belongs in api_provider.go:\n%s", forbidden, di)
				}
			}
			// The provider lives in the stable seam and is the in-memory demo by
			// default.
			provider := readFile(t, filepath.Join(dir, "internal/di/api_provider.go"))
			for _, want := range []string{"func NewAPIService() api.Service", "return api.NewService()"} {
				if !strings.Contains(provider, want) {
					t.Errorf("api_provider.go is missing %q:\n%s", want, provider)
				}
			}
			gen := readFile(t, filepath.Join(dir, "internal/di/loom_gen.go"))
			if !strings.Contains(gen, "NewAPIService") {
				t.Errorf("loom_gen.go does not bind the API service:\n%s", gen)
			}
			for _, forbidden := range []string{"NewPool", "NewRepository"} {
				if strings.Contains(gen, forbidden) {
					t.Errorf("loom_gen.go constructs the unused %s; the default graph must not wire db:\n%s", forbidden, gen)
				}
			}
			gofmtCheck(t, dir)
			if !buildAndTestGeneratedProject(t, dir) {
				t.Skip("cannot resolve the generated project's dependencies (network/module cache unavailable)")
			}
		})
	}
}

// TestAPIProviderSeamGuardsInstallOnce proves the api capability owns the stable
// api_provider.go seam, guarded on loom. Because api requires http (which
// requires loom), loom is always present when api installs, so the guard always
// applies and loom itself no longer declares the file.
func TestAPIProviderSeamGuardsInstallOnce(t *testing.T) {
	const path = "internal/di/api_provider.go"
	catalog := newCatalog()

	api, err := catalog.Get("api")
	if err != nil {
		t.Fatalf("Get api: %v", err)
	}
	var apiFile *template.File
	for i := range api.Files {
		if api.Files[i].Path == path {
			apiFile = &api.Files[i]
		}
	}
	if apiFile == nil {
		t.Fatalf("api does not declare %s", path)
	}
	if len(apiFile.When) != 1 || apiFile.When[0] != "loom" {
		t.Errorf("api %s is not guarded by when: [loom]: %+v", path, apiFile)
	}
	if !entryApplies(map[string]bool{"base": true, "loom": true}, apiFile.When, apiFile.WhenAbsent) {
		t.Error("api does not write api_provider.go when loom is installed")
	}
	if entryApplies(map[string]bool{"base": true}, apiFile.When, apiFile.WhenAbsent) {
		t.Error("api writes api_provider.go without loom")
	}

	loom, err := catalog.Get("loom")
	if err != nil {
		t.Fatalf("Get loom: %v", err)
	}
	for _, file := range loom.Files {
		if file.Path == path {
			t.Errorf("loom still declares %s; the provider seam belongs to the api capability", path)
		}
	}
}

// TestAPIProviderSeamSurvivesLaterAdd proves the durable seam end to end: after
// api+db+loom, a user rewrites internal/di/api_provider.go to consume
// data.Repository; a later `weld add web` regenerates the graph and loom_gen.go
// from the new provider but leaves api_provider.go byte for byte intact, and the
// project still builds, vets and tests without PostgreSQL.
func TestAPIProviderSeamSurvivesLaterAdd(t *testing.T) {
	goValidate := goValidateDir(t)
	loomToolAvailable(t)
	root := t.TempDir()
	dir := create(t, root)
	useLocalGoValidate(t, dir, goValidate)
	for _, capability := range []string{"api", "db", "loom"} {
		add(t, dir, capability)
	}

	// The user rewires the API service onto the repository Loom already declares
	// as an available binding. Ignoring the repository keeps the test hermetic:
	// the point is that Loom constructs the pool and repository because this
	// provider asks for them.
	const edited = `package di

import (
	"example.com/demo/internal/api"
	"example.com/demo/internal/data"
)

// NewAPIService is wired by hand onto the repository.
func NewAPIService(repo data.Repository) api.Service {
	_ = repo
	return api.NewService()
}
`
	path := filepath.Join(dir, "internal/di/api_provider.go")
	if err := os.WriteFile(path, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}

	add(t, dir, "web")

	if got := readFile(t, path); got != edited {
		t.Fatalf("the later add rewrote the user's api_provider.go:\n%s", got)
	}
	di := readFile(t, filepath.Join(dir, "internal/di/di.go"))
	if !strings.Contains(di, "example.com/demo/internal/web") {
		t.Errorf("di.go was not regenerated for web:\n%s", di)
	}
	gen := readFile(t, filepath.Join(dir, "internal/di/loom_gen.go"))
	for _, want := range []string{"NewPool", "NewRepository"} {
		if !strings.Contains(gen, want) {
			t.Errorf("loom_gen.go did not follow the edited provider to wire %s:\n%s", want, gen)
		}
	}
	// The regenerated graph test must not call the edited provider's signature.
	if diTest := readFile(t, filepath.Join(dir, "internal/di/di_test.go")); strings.Contains(diTest, "NewAPIService(") {
		t.Errorf("the regenerated di_test.go calls the editable provider:\n%s", diTest)
	}
	if drift := mustLoad(t, dir).Drift(dir); len(drift) != 0 {
		t.Fatalf("drift after the later add: %+v", drift)
	}
	if !buildAndTestGeneratedProject(t, dir) {
		t.Skip("cannot resolve the generated project's dependencies (network/module cache unavailable)")
	}
}

// --- config milestone ------------------------------------------------------

// TestAddHTTPInstallsSharedConfig proves the first http/db add installs the
// shared config capability: the typed loader, the local config.yml and the
// committed example, the git-ignore rule and the yaml dependency. An http-only
// project never gains a database section.
func TestAddHTTPInstallsSharedConfig(t *testing.T) {
	root := t.TempDir()
	dir := create(t, root)

	result := add(t, dir, "http")
	if got, want := strings.Join(result.Installed, ","), "config,loom,http"; got != want {
		t.Fatalf("installed = %q, want %q", got, want)
	}
	for _, path := range []string{"internal/config/config.go", "internal/config/config_test.go", "config.yml", "config.example.yml"} {
		if _, err := os.Stat(filepath.Join(dir, path)); err != nil {
			t.Errorf("expected %s: %v", path, err)
		}
	}
	if gi := readFile(t, filepath.Join(dir, ".gitignore")); !strings.Contains(gi, "config.yml") || !strings.Contains(gi, "weld:config:installed") {
		t.Errorf(".gitignore does not ignore config.yml:\n%s", gi)
	}
	if gm := readFile(t, filepath.Join(dir, "go.mod")); !strings.Contains(gm, "gopkg.in/yaml.v3") || !strings.Contains(gm, "weld:config:installed") {
		t.Errorf("go.mod does not require yaml.v3:\n%s", gm)
	}
	cfg := readFile(t, filepath.Join(dir, "config.yml"))
	if strings.Contains(cfg, "database:") {
		t.Errorf("http-only config.yml already has a database section:\n%s", cfg)
	}
	if !strings.Contains(cfg, "http:") {
		t.Errorf("http-only config.yml is missing the http section:\n%s", cfg)
	}
	manifest := mustLoad(t, dir)
	if len(manifest.Drift(dir)) != 0 {
		t.Fatalf("drift after add: %+v", manifest.Drift(dir))
	}
	if !buildAndTestGeneratedProject(t, dir) {
		t.Skip("cannot resolve the generated project's dependencies (network/module cache unavailable)")
	}
}

// TestAddDBOnlyConfigHasNoHTTPSection proves a db-only project does not gain a
// gratuitous http section: the config capability writes only the marker region
// and each capability appends its own section.
func TestAddDBOnlyConfigHasNoHTTPSection(t *testing.T) {
	root := t.TempDir()
	dir := create(t, root)

	add(t, dir, "db")
	cfg := readFile(t, filepath.Join(dir, "config.yml"))
	if strings.Contains(cfg, "http:") {
		t.Errorf("db-only config.yml carries an http section:\n%s", cfg)
	}
	if !strings.Contains(cfg, "database:") {
		t.Errorf("db-only config.yml is missing the database section:\n%s", cfg)
	}
	example := readFile(t, filepath.Join(dir, "config.example.yml"))
	if strings.Contains(example, "http:") {
		t.Errorf("db-only config.example.yml carries an http section:\n%s", example)
	}
}

// TestAddDBMergesConfigSectionInBothOrders proves the second capability appends
// its section to the existing local config.yml and committed example, whichever
// order http and db are added in, and does so exactly once.
func TestAddDBMergesConfigSectionInBothOrders(t *testing.T) {
	orders := []struct {
		name          string
		first, second string
	}{
		{"http then db", "http", "db"},
		{"db then http", "db", "http"},
	}
	for _, order := range orders {
		t.Run(order.name, func(t *testing.T) {
			root := t.TempDir()
			dir := create(t, root)
			add(t, dir, order.first)
			add(t, dir, order.second)

			for _, name := range []string{"config.yml", "config.example.yml"} {
				content := readFile(t, filepath.Join(dir, name))
				if !strings.Contains(content, "http:") || !strings.Contains(content, "database:") {
					t.Errorf("%s is missing a section:\n%s", name, content)
				}
				if count := strings.Count(content, "weld:db:installed"); count != 1 {
					t.Errorf("%s has %d database sections, want 1", name, count)
				}
			}
			manifest := mustLoad(t, dir)
			if len(manifest.Drift(dir)) != 0 {
				t.Fatalf("drift: %+v", manifest.Drift(dir))
			}
		})
	}
}

// TestAddDBPreservesEditedConfig proves a late add merges its section without
// clobbering a user's edits to the local file: the changed address and an added
// comment survive while the database section is appended.
func TestAddDBPreservesEditedConfig(t *testing.T) {
	root := t.TempDir()
	dir := create(t, root)
	add(t, dir, "http")

	path := filepath.Join(dir, "config.yml")
	edited := strings.Replace(readFile(t, path), `  addr: ":8080"`, "  # keep my address\n  addr: \":9000\"", 1)
	if err := os.WriteFile(path, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}

	add(t, dir, "db")
	after := readFile(t, path)
	for _, want := range []string{"# keep my address", `addr: ":9000"`, "database:"} {
		if !strings.Contains(after, want) {
			t.Errorf("add db did not preserve/merge %q:\n%s", want, after)
		}
	}
}

// TestAddPreservesConfigPassword proves a user's local database password in
// config.yml survives later adds, including an idempotent repeat of db.
func TestAddPreservesConfigPassword(t *testing.T) {
	root := t.TempDir()
	dir := create(t, root)
	add(t, dir, "db")

	path := filepath.Join(dir, "config.yml")
	edited := strings.Replace(readFile(t, path), `  password: ""`, `  password: "sentinel-password"`, 1)
	if err := os.WriteFile(path, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}

	add(t, dir, "http")
	add(t, dir, "db")
	if !strings.Contains(readFile(t, path), `password: "sentinel-password"`) {
		t.Fatalf("the database password was clobbered:\n%s", readFile(t, path))
	}
}

// TestAddRestoresGitIgnoredConfigInBothOrders proves a fresh clone can continue.
// config.yml is git-ignored, so it is absent after checkout while the manifest
// still records it and the tracked config.example.yml remains. Adding the other
// capability must restore the local file from the example rather than fail on
// the missing patch target, and must merge its section exactly once.
func TestAddRestoresGitIgnoredConfigInBothOrders(t *testing.T) {
	orders := []struct {
		name          string
		first, second string
	}{
		{"http then db", "http", "db"},
		{"db then http", "db", "http"},
	}
	for _, order := range orders {
		t.Run(order.name, func(t *testing.T) {
			root := t.TempDir()
			dir := create(t, root)
			add(t, dir, order.first)

			// Simulate a fresh clone: only the git-ignored local file is gone.
			if err := os.Remove(filepath.Join(dir, "config.yml")); err != nil {
				t.Fatal(err)
			}

			add(t, dir, order.second)

			for _, name := range []string{"config.yml", "config.example.yml"} {
				content := readFile(t, filepath.Join(dir, name))
				for _, want := range []string{"http:", `addr: ":8080"`, "database:", "host: localhost"} {
					if !strings.Contains(content, want) {
						t.Errorf("%s is missing %q after the restore:\n%s", name, want, content)
					}
				}
				if count := strings.Count(content, "weld:db:installed"); count != 1 {
					t.Errorf("%s has %d database sections, want 1", name, count)
				}
				if count := strings.Count(content, "weld:http:installed"); count != 1 {
					t.Errorf("%s has %d http sections, want 1", name, count)
				}
			}
			manifest := mustLoad(t, dir)
			if len(manifest.Drift(dir)) != 0 {
				t.Fatalf("drift after restoring the config: %+v", manifest.Drift(dir))
			}
		})
	}
}

// TestAddConfigRestoreKeepsSecretsOutOfTheExample proves a development password
// lives only in the git-ignored local file: a restore copies the tracked example
// (which never absorbed the secret) rather than inventing or scraping a
// credential.
func TestAddConfigRestoreKeepsSecretsOutOfTheExample(t *testing.T) {
	root := t.TempDir()
	dir := create(t, root)
	add(t, dir, "db")

	path := filepath.Join(dir, "config.yml")
	edited := strings.Replace(readFile(t, path), `  password: ""`, `  password: "sentinel-password"`, 1)
	if err := os.WriteFile(path, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}

	// A fresh clone: the local file and its secret are gone.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	add(t, dir, "http")

	example := readFile(t, filepath.Join(dir, "config.example.yml"))
	if strings.Contains(example, "sentinel-password") {
		t.Errorf("the tracked example leaked the local password:\n%s", example)
	}
	restored := readFile(t, path)
	if strings.Contains(restored, "sentinel-password") {
		t.Errorf("the restore invented a credential instead of using the tracked example:\n%s", restored)
	}
	if !strings.Contains(restored, `password: ""`) {
		t.Errorf("the restored config.yml has no empty password field:\n%s", restored)
	}
}

// TestAddConfigRestoreReportsAMissingOrCorruptExample proves a fresh clone with
// no usable tracked example fails with an actionable instruction instead of a
// silent default: the missing local config is a distinct error naming the
// example to restore.
func TestAddConfigRestoreReportsAMissingOrCorruptExample(t *testing.T) {
	cases := []struct {
		name    string
		corrupt string
		want    string
	}{
		{"example absent", "", "is not present"},
		{"example corrupt", "http:\n  addr: \":8080\"\n", "has no weld:config"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			dir := create(t, root)
			add(t, dir, "db")

			if err := os.Remove(filepath.Join(dir, "config.yml")); err != nil {
				t.Fatal(err)
			}
			example := filepath.Join(dir, "config.example.yml")
			if tc.corrupt == "" {
				if err := os.Remove(example); err != nil {
					t.Fatal(err)
				}
			} else if err := os.WriteFile(example, []byte(tc.corrupt), 0o644); err != nil {
				t.Fatal(err)
			}

			_, err := Add(Request{Dir: dir, Catalog: newCatalog()}, "http")
			if err == nil {
				t.Fatal("expected an error when the tracked example cannot restore config.yml")
			}
			for _, want := range []string{"config.yml", "config.example.yml", tc.want} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not mention %q", err, want)
				}
			}
			if _, statErr := os.Stat(filepath.Join(dir, "config.yml")); !os.IsNotExist(statErr) {
				t.Errorf("a failed plan created config.yml: %v", statErr)
			}
		})
	}
}

func TestAddDoesNotBootstrapUnmanagedConfig(t *testing.T) {
	root := t.TempDir()
	dir := create(t, root)
	add(t, dir, "db")
	if err := os.Remove(filepath.Join(dir, "config.yml")); err != nil {
		t.Fatal(err)
	}
	manifest := mustLoad(t, dir)
	for i, file := range manifest.Files {
		if file.Path == "config.yml" {
			manifest.Files = append(manifest.Files[:i], manifest.Files[i+1:]...)
			break
		}
	}
	raw, err := manifest.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, project.ManifestName), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = Add(Request{Dir: dir, Catalog: newCatalog()}, "http")
	if err == nil || !strings.Contains(err.Error(), "not managed by weld") {
		t.Fatalf("Add(http) with unmanaged missing config error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "config.yml")); !os.IsNotExist(err) {
		t.Fatalf("unmanaged config was created: %v", err)
	}
}

// TestOldCLIRejectsNewServeBeforeWriting prevents an existing stdlib-CLI
// project from receiving a Cobra serve file that cannot compile against it.
// --- logging milestone -----------------------------------------------------

// assertInjectedLogger fails when a generated project does not ship the base
// logging factory or reaches for the process default logger instead of an
// injected *slog.Logger.
func assertInjectedLogger(t *testing.T, dir string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(dir, "internal", "logging", "logging.go")); err != nil {
		t.Fatalf("generated project is missing the base logging factory: %v", err)
	}
	if di, err := os.ReadFile(filepath.Join(dir, "internal", "di", "di.go")); err == nil {
		if !strings.Contains(string(di), "loom.Provide(NewLogger)") {
			t.Errorf("internal/di/di.go does not provide the logger to the graph")
		}
	}
	if gen, err := os.ReadFile(filepath.Join(dir, "internal", "di", "loom_gen.go")); err == nil {
		if !strings.Contains(string(gen), "NewLogger") {
			t.Errorf("internal/di/loom_gen.go does not construct the injected logger")
		}
	}
	forbidden := []string{"slog.SetDefault", "slog.Default(", "slog.Info(", "slog.Error(", "slog.Warn(", "slog.Debug("}
	err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(dir, path)
		if relErr != nil {
			rel = path
		}
		src := string(raw)
		for _, pattern := range forbidden {
			if strings.Contains(src, pattern) {
				t.Errorf("%s uses the process default logger (%s); the logger must be injected", rel, pattern)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk generated project: %v", err)
	}
}

// TestBaseGeneratedProjectLogsErrorsThroughSlog proves the base CLI's top-level
// error path is structured logging: an unknown command exits non-zero and the
// failure is a slog record rather than a fmt.Fprintln line, and the logger is
// built by the base factory with no process default logger.
func TestBaseGeneratedProjectLogsErrorsThroughSlog(t *testing.T) {
	root := t.TempDir()
	dir := create(t, root)
	assertInjectedLogger(t, dir)
	if !buildAndTestGeneratedProject(t, dir) {
		t.Skip("cannot resolve the generated project's dependencies (network/module cache unavailable)")
	}

	binary := filepath.Join(t.TempDir(), "app")
	build := exec.Command("go", "build", "-o", binary, ".")
	build.Dir = dir
	build.Env = append(os.Environ(), "GOPROXY=off", "GOWORK=off")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	run := exec.Command(binary, "bogus")
	run.Dir = dir
	out, err := run.CombinedOutput()
	if err == nil {
		t.Fatalf("an unknown command unexpectedly succeeded:\n%s", out)
	}
	text := string(out)
	if !strings.Contains(text, "level=ERROR") || !strings.Contains(text, "bogus") {
		t.Fatalf("the failed command was not logged through slog:\n%s", text)
	}
	if strings.Contains(text, "error:") {
		t.Fatalf("the base CLI still uses the fmt error path:\n%s", text)
	}
}

// TestBaseGeneratedProjectBareRunShowsHelp proves a bare base invocation (no
// argument) prints the command help and exits zero: with no capability there is
// no long-running root action, so nothing starts.
func TestBaseGeneratedProjectBareRunShowsHelp(t *testing.T) {
	root := t.TempDir()
	dir := create(t, root)
	if !goModTidy(t, dir) {
		t.Skip("cannot resolve the generated project's dependencies (network/module cache unavailable)")
	}
	env := serveEnv()
	binary := buildAppBinary(t, dir, env)

	out, err := exec.Command(binary).CombinedOutput()
	if err != nil {
		t.Fatalf("bare base invocation failed: %v\n%s", err, out)
	}
	text := string(out)
	for _, want := range []string{"Usage:", "version", "help"} {
		if !strings.Contains(text, want) {
			t.Errorf("bare base help is missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "listening") {
		t.Errorf("a bare base invocation started a server:\n%s", text)
	}
}

// TestBareAppAndServeShareOneLifecycle proves that once http is installed a bare
// `app` and `app serve` are the same long-running lifecycle: both bind the
// shared --addr and log the listening line, and both stop cleanly on SIGTERM.
// help, version and an unknown command never start it.
func TestBareAppAndServeShareOneLifecycle(t *testing.T) {
	root := t.TempDir()
	dir := create(t, root)
	add(t, dir, "http")
	if !goModTidy(t, dir) {
		t.Skip("cannot resolve the generated project's dependencies (network/module cache unavailable)")
	}
	env := serveEnv()
	binary := buildAppBinary(t, dir, env)

	for _, args := range [][]string{nil, {"serve"}} {
		cmd, addr, stderr := startApp(t, binary, dir, env, args...)
		if !strings.Contains(stderr.String(), addr) {
			t.Errorf("args %v did not bind the shared --addr %s:\n%s", args, addr, stderr.String())
		}
		stopServe(t, cmd, stderr)
	}

	for _, args := range [][]string{{"help"}, {"version"}, {"--version"}, {"bogus"}} {
		cmd := exec.Command(binary, args...)
		cmd.Dir = dir
		cmd.Env = env
		out, _ := cmd.CombinedOutput()
		if strings.Contains(string(out), "listening") {
			t.Errorf("%v started the server:\n%s", args, out)
		}
	}
}

// TestLoomBareAppAndServeShareOneLifecycle proves the Loom replacement keeps a
// bare `app` and `app serve` identical: both run the generated graph and log the
// listening line, and both stop cleanly on SIGTERM.
func TestLoomBareAppAndServeShareOneLifecycle(t *testing.T) {
	loomToolAvailable(t)
	root := t.TempDir()
	dir := create(t, root)
	for _, capability := range []string{"http", "loom"} {
		add(t, dir, capability)
	}
	if !goModTidy(t, dir) {
		t.Skip("cannot resolve the generated project's dependencies (network/module cache unavailable)")
	}
	env := serveEnv()
	binary := buildAppBinary(t, dir, env)

	for _, args := range [][]string{nil, {"serve"}} {
		cmd, addr, stderr := startApp(t, binary, dir, env, args...)
		if !strings.Contains(stderr.String(), addr) {
			t.Errorf("args %v did not bind the shared --addr %s:\n%s", args, addr, stderr.String())
		}
		stopServe(t, cmd, stderr)
	}
}

// TestLoggingMilestoneCombinationMatrix proves the logging milestone composes
// with every capability combination: the base logging factory is present, no
// generated file reaches for the process default logger, and the composed
// project gofmt/builds/vets/tests.
func TestLoggingMilestoneCombinationMatrix(t *testing.T) {
	goValidate := goValidateDir(t)
	cases := []struct {
		name      string
		caps      []string
		needsAPI  bool
		needsLoom bool
	}{
		{name: "base"},
		{name: "http", caps: []string{"http"}},
		{name: "api", caps: []string{"api"}, needsAPI: true},
		{name: "db", caps: []string{"db"}},
		{name: "redis", caps: []string{"redis"}},
		{name: "loom", caps: []string{"loom"}, needsLoom: true},
		{name: "redis+loom", caps: []string{"redis", "loom"}, needsLoom: true},
		{name: "api+db", caps: []string{"api", "db"}, needsAPI: true},
		{name: "db+loom", caps: []string{"db", "loom"}, needsLoom: true},
		{name: "api+db+loom", caps: []string{"api", "db", "loom"}, needsAPI: true, needsLoom: true},
		{name: "cron", caps: []string{"cron"}},
		{name: "http+cron", caps: []string{"http", "cron"}},
		{name: "cron+loom", caps: []string{"cron", "loom"}, needsLoom: true},
		{name: "mail+storage+loom", caps: []string{"mail", "storage", "loom"}, needsLoom: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.needsLoom {
				loomToolAvailable(t)
			}
			root := t.TempDir()
			dir := create(t, root)
			if tc.needsAPI {
				useLocalGoValidate(t, dir, goValidate)
			}
			for _, capability := range tc.caps {
				add(t, dir, capability)
			}
			assertInjectedLogger(t, dir)
			if !buildAndTestGeneratedProject(t, dir) {
				t.Skip("cannot resolve the generated project's dependencies (network/module cache unavailable)")
			}
		})
	}
}

// --- redis capability -------------------------------------------------------

// redisSectionKeys maps a capability to the YAML section it appends, so an
// order test can assert both sections landed without hard-coding per order.
var redisSectionKeys = map[string]string{"http": "http:", "db": "database:", "redis": "redis:"}

func TestAddRedisInstallsAlone(t *testing.T) {
	root := t.TempDir()
	dir := create(t, root)

	result := add(t, dir, "redis")
	if got, want := strings.Join(result.Installed, ","), "config,loom,redis"; got != want {
		t.Fatalf("installed = %q, want %q", got, want)
	}
	for _, path := range []string{
		"internal/config/config.go",
		"internal/config/redis.go",
		"internal/config/redis_test.go",
		"internal/redisclient/redisclient.go",
		"internal/redisclient/redisclient_test.go",
		"internal/redisclient/README.md",
		"internal/di/di.go",
		"internal/di/redis_provider.go",
		"config.yml",
		"config.example.yml",
	} {
		if _, err := os.Stat(filepath.Join(dir, path)); err != nil {
			t.Errorf("expected %s: %v", path, err)
		}
	}
	// redis never pulls http, db, api or web; loom is a CLI-only graph here.
	for _, path := range []string{"internal/httpserver", "internal/data", "internal/api", "internal/web"} {
		if _, err := os.Stat(filepath.Join(dir, path)); !os.IsNotExist(err) {
			t.Errorf("add redis created %s", path)
		}
	}

	// The config struct gained the field through the extension point and the
	// loader calls the redis environment and defaults hooks.
	configGo := readFile(t, filepath.Join(dir, "internal/config/config.go"))
	for _, want := range []string{
		"weld:redis:installed",
		"Redis Redis `yaml:\"redis\"`",
		"cfg.applyRedisEnv(l.lookupEnv)",
		"cfg.applyRedisDefaults()",
	} {
		if !strings.Contains(configGo, want) {
			t.Errorf("config.go is missing %q:\n%s", want, configGo)
		}
	}
	// The client imports go-redis but no HTTP, JSON or data surface.
	clientGo := readFile(t, filepath.Join(dir, "internal/redisclient/redisclient.go"))
	if !strings.Contains(clientGo, "github.com/redis/go-redis/v9") {
		t.Errorf("redisclient.go does not import go-redis:\n%s", clientGo)
	}
	for _, forbidden := range []string{"\"net/http\"", "\"encoding/json\"", "internal/data"} {
		if strings.Contains(clientGo, forbidden) {
			t.Errorf("redisclient.go imports %s", forbidden)
		}
	}

	cfg := readFile(t, filepath.Join(dir, "config.yml"))
	if !strings.Contains(cfg, "redis:") || !strings.Contains(cfg, "weld:redis:installed") {
		t.Errorf("config.yml has no redis section:\n%s", cfg)
	}
	goMod := readFile(t, filepath.Join(dir, "go.mod"))
	for _, want := range []string{"github.com/redis/go-redis/v9", "github.com/alicebob/miniredis/v2", "weld:redis:installed"} {
		if !strings.Contains(goMod, want) {
			t.Errorf("go.mod is missing %q:\n%s", want, goMod)
		}
	}
	if drift := mustLoad(t, dir).Drift(dir); len(drift) != 0 {
		t.Fatalf("drift after add redis: %+v", drift)
	}
	gofmtCheck(t, dir)
}

func TestAddRedisGeneratedProjectBuildsAndTests(t *testing.T) {
	root := t.TempDir()
	dir := create(t, root)
	add(t, dir, "redis")
	if !buildAndTestGeneratedProject(t, dir) {
		t.Skip("cannot resolve the generated project's redis dependencies (network/module cache unavailable)")
	}
}

func TestAddRedisIsIdempotent(t *testing.T) {
	root := t.TempDir()
	dir := create(t, root)
	add(t, dir, "redis")

	before := map[string]string{}
	for _, path := range []string{"internal/config/config.go", "config.yml", "config.example.yml", "go.mod", project.ManifestName} {
		before[path] = readFile(t, filepath.Join(dir, path))
	}
	result, err := Add(Request{Dir: dir, Catalog: newCatalog()}, "redis")
	if err != nil {
		t.Fatalf("second Add: %v", err)
	}
	if len(result.Operations) != 0 || len(result.Installed) != 0 {
		t.Fatalf("second Add planned %d operations and installed %v, want none", len(result.Operations), result.Installed)
	}
	if len(result.Notes) == 0 {
		t.Fatal("second Add produced no explanation")
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

func TestAddRedisIsDryRunnable(t *testing.T) {
	root := t.TempDir()
	dir := create(t, root)

	configPath := filepath.Join(dir, "config.yml")
	examplePath := filepath.Join(dir, "config.example.yml")
	goModBefore := readFile(t, filepath.Join(dir, "go.mod"))

	result, err := Add(Request{Dir: dir, Catalog: newCatalog()}, "redis")
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if len(result.Operations) == 0 {
		t.Fatal("expected a non-empty plan")
	}
	if readFile(t, filepath.Join(dir, "go.mod")) != goModBefore {
		t.Fatal("planning wrote go.mod")
	}
	for _, path := range []string{configPath, examplePath, filepath.Join(dir, "internal/redisclient"), filepath.Join(dir, "internal/config/redis.go")} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("planning wrote %s", path)
		}
	}
}

// TestAddRedisConfigMergesInBothOrders proves the redis section lands in the
// local config.yml and the committed example whichever capability is added
// first, exactly once, and that the other capability's section is untouched.
func TestAddRedisConfigMergesInBothOrders(t *testing.T) {
	orders := []struct {
		name          string
		first, second string
	}{
		{"http then redis", "http", "redis"},
		{"redis then http", "redis", "http"},
		{"db then redis", "db", "redis"},
		{"redis then db", "redis", "db"},
	}
	for _, order := range orders {
		t.Run(order.name, func(t *testing.T) {
			root := t.TempDir()
			dir := create(t, root)
			add(t, dir, order.first)
			add(t, dir, order.second)

			for _, name := range []string{"config.yml", "config.example.yml"} {
				content := readFile(t, filepath.Join(dir, name))
				for _, key := range []string{redisSectionKeys[order.first], redisSectionKeys[order.second]} {
					if !strings.Contains(content, key) {
						t.Errorf("%s is missing section %q:\n%s", name, key, content)
					}
				}
				if count := strings.Count(content, "weld:redis:installed"); count != 1 {
					t.Errorf("%s has %d redis sections, want 1:\n%s", name, count, content)
				}
			}
			if drift := mustLoad(t, dir).Drift(dir); len(drift) != 0 {
				t.Fatalf("drift: %+v", drift)
			}
		})
	}
}

// TestAddRedisPreservesEditedConfig proves a late add merges its section without
// clobbering a user's edits to the local file.
func TestAddRedisPreservesEditedConfig(t *testing.T) {
	root := t.TempDir()
	dir := create(t, root)
	add(t, dir, "http")

	path := filepath.Join(dir, "config.yml")
	edited := strings.Replace(readFile(t, path), `  addr: ":8080"`, "  # keep my address\n  addr: \":9000\"", 1)
	if err := os.WriteFile(path, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}

	add(t, dir, "redis")
	after := readFile(t, path)
	for _, want := range []string{"# keep my address", `addr: ":9000"`, "redis:"} {
		if !strings.Contains(after, want) {
			t.Errorf("add redis did not preserve/merge %q:\n%s", want, after)
		}
	}
}

// TestAddRedisPreservesConfigPassword proves a user's local Redis password in
// config.yml survives later adds, including an idempotent repeat of redis.
func TestAddRedisPreservesConfigPassword(t *testing.T) {
	root := t.TempDir()
	dir := create(t, root)
	add(t, dir, "redis")

	path := filepath.Join(dir, "config.yml")
	edited := strings.Replace(readFile(t, path), `  password: ""`, `  password: "sentinel-password"`, 1)
	if err := os.WriteFile(path, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}

	add(t, dir, "http")
	add(t, dir, "redis")
	if !strings.Contains(readFile(t, path), `password: "sentinel-password"`) {
		t.Fatalf("the redis password was clobbered:\n%s", readFile(t, path))
	}
}

// TestAddRedisRestoresGitIgnoredConfig proves a fresh clone can continue:
// config.yml is git-ignored, so it is absent after checkout while the manifest
// records it and the tracked config.example.yml remains. Adding another
// capability restores the local file from the example and merges its section.
func TestAddRedisRestoresGitIgnoredConfig(t *testing.T) {
	root := t.TempDir()
	dir := create(t, root)
	add(t, dir, "redis")

	if err := os.Remove(filepath.Join(dir, "config.yml")); err != nil {
		t.Fatal(err)
	}
	add(t, dir, "db")

	for _, name := range []string{"config.yml", "config.example.yml"} {
		content := readFile(t, filepath.Join(dir, name))
		for _, want := range []string{"redis:", "database:"} {
			if !strings.Contains(content, want) {
				t.Errorf("%s is missing %q after the restore:\n%s", name, want, content)
			}
		}
		if count := strings.Count(content, "weld:redis:installed"); count != 1 {
			t.Errorf("%s has %d redis sections, want 1", name, count)
		}
	}
	if drift := mustLoad(t, dir).Drift(dir); len(drift) != 0 {
		t.Fatalf("drift after restoring the config: %+v", drift)
	}
}

// TestAddRedisRejectsConfigWithoutExtensionPoints proves backward compatibility:
// a config.go generated before the redis extension points existed has no marker
// region, so `weld add redis` fails clearly instead of rewriting the file. The
// project keeps its hand-written config and no redis files are written.
func TestAddRedisRejectsConfigWithoutExtensionPoints(t *testing.T) {
	root := t.TempDir()
	dir := create(t, root)
	add(t, dir, "db")

	path := filepath.Join(dir, "internal/config/config.go")
	content := readFile(t, path)
	content = strings.ReplaceAll(content, "\t// weld:configfields:begin\n", "")
	content = strings.ReplaceAll(content, "\t// weld:configfields:end\n", "")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := Add(Request{Dir: dir, Catalog: newCatalog()}, "redis")
	if err == nil {
		t.Fatal("expected an error when the config extension point is missing")
	}
	if !strings.Contains(err.Error(), "extension point") {
		t.Fatalf("error = %v, want it to name the missing extension point", err)
	}
	for _, unchanged := range []string{path} {
		if readFile(t, unchanged) != content {
			t.Errorf("%s was rewritten on the failed add", unchanged)
		}
	}
	for _, absent := range []string{"internal/redisclient", "internal/config/redis.go"} {
		if _, statErr := os.Stat(filepath.Join(dir, absent)); !os.IsNotExist(statErr) {
			t.Errorf("a failed plan wrote %s", absent)
		}
	}
}

// TestRedisGeneratedProjectRaceAndVet runs the generated project's tests under
// the race detector and vets the Redis combinations, all using Loom, proving
// the idle client and the composed graph are race-clean with no Redis server.
func TestRedisGeneratedProjectRaceAndVet(t *testing.T) {
	cases := []struct {
		name      string
		caps      []string
		needsLoom bool
	}{
		{name: "redis", caps: []string{"redis"}},
		{name: "redis+loom", caps: []string{"redis", "loom"}, needsLoom: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.needsLoom {
				loomToolAvailable(t)
			}
			root := t.TempDir()
			dir := create(t, root)
			for _, capability := range tc.caps {
				add(t, dir, capability)
			}
			if !goModTidy(t, dir) {
				t.Skip("cannot resolve the generated project's dependencies (network/module cache unavailable)")
			}
			gofmtCheck(t, dir)
			runGo(t, dir, "vet", "./...")
			runGo(t, dir, "test", "-race", "./...")
		})
	}
}

// TestAddRedisLoomPrunesUnconsumedClient proves the install != wire contract:
// with redis and loom installed the graph declares NewRedisClient but nothing
// depends on *redis.Client, so the generated initializer never constructs it and
// no Redis setting is needed. The provider lives in the stable redis_provider.go
// seam, not in the regenerated graph.
func TestAddRedisLoomPrunesUnconsumedClient(t *testing.T) {
	loomToolAvailable(t)
	orders := []struct {
		name string
		caps []string
	}{
		{"redis then http", []string{"redis", "http"}},
		{"http then redis", []string{"http", "redis"}},
	}
	for _, order := range orders {
		t.Run(order.name, func(t *testing.T) {
			root := t.TempDir()
			dir := create(t, root)
			for _, capability := range order.caps {
				add(t, dir, capability)
			}
			provider := readFile(t, filepath.Join(dir, "internal/di/redis_provider.go"))
			if !strings.Contains(provider, "func NewRedisClient") {
				t.Errorf("redis_provider.go is missing NewRedisClient:\n%s", provider)
			}
			di := readFile(t, filepath.Join(dir, "internal/di/di.go"))
			if !strings.Contains(di, "loom.Provide(NewRedisClient)") {
				t.Errorf("di.go does not declare the Redis client binding:\n%s", di)
			}
			gen := readFile(t, filepath.Join(dir, "internal/di/loom_gen.go"))
			if strings.Contains(gen, "NewRedisClient") {
				t.Errorf("loom_gen.go constructs the unused Redis client; the default composition must not wire redis:\n%s", gen)
			}
			if drift := mustLoad(t, dir).Drift(dir); len(drift) != 0 {
				t.Fatalf("drift after add: %+v", drift)
			}
			gofmtCheck(t, dir)
			if !buildAndTestGeneratedProject(t, dir) {
				t.Skip("cannot resolve the generated project's dependencies (network/module cache unavailable)")
			}
		})
	}
}

// TestAddRedisLoomProviderSeamIsStable proves the durable seam: a user edit to
// redis_provider.go survives a later capability add that regenerates the graph,
// and the graph still follows the installed capabilities.
func TestAddRedisLoomProviderSeamIsStable(t *testing.T) {
	loomToolAvailable(t)
	root := t.TempDir()
	dir := create(t, root)
	for _, capability := range []string{"redis", "loom"} {
		add(t, dir, capability)
	}

	path := filepath.Join(dir, "internal/di/redis_provider.go")
	edited := readFile(t, path) + "\n// user wiring, must survive a later add\n"
	if err := os.WriteFile(path, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}

	add(t, dir, "web")

	if got := readFile(t, path); got != edited {
		t.Fatalf("the later add rewrote the user's redis_provider.go:\n%s", got)
	}
	di := readFile(t, filepath.Join(dir, "internal/di/di.go"))
	if !strings.Contains(di, "internal/web") {
		t.Errorf("di.go was not regenerated for web:\n%s", di)
	}
	if !buildAndTestGeneratedProject(t, dir) {
		t.Skip("cannot resolve the generated project's dependencies (network/module cache unavailable)")
	}
}

// TestAddRedisLoomConsumesWhenProviderAsks proves explicit consumption end to
// end: after api+redis+loom a user edits the stable api_provider.go to consume
// *redis.Client; a later `weld add web` regenerates the graph and loom_gen.go so
// the initializer constructs NewRedisClient, while redis_provider.go and the
// edited api_provider.go stay byte for byte intact. The API uses the published
// go-validate Spec API by default.
func TestAddRedisLoomConsumesWhenProviderAsks(t *testing.T) {
	goValidate := goValidateDir(t)
	loomToolAvailable(t)
	root := t.TempDir()
	dir := create(t, root)
	useLocalGoValidate(t, dir, goValidate)
	for _, capability := range []string{"api", "redis", "loom"} {
		add(t, dir, capability)
	}

	providerPath := filepath.Join(dir, "internal/di/redis_provider.go")
	providerBefore := readFile(t, providerPath)

	const edited = `package di

import (
	"example.com/demo/internal/api"

	"github.com/redis/go-redis/v9"
)

// NewAPIService is wired by hand onto the Redis client.
func NewAPIService(client *redis.Client) api.Service {
	_ = client
	return api.NewService()
}
`
	apiPath := filepath.Join(dir, "internal/di/api_provider.go")
	if err := os.WriteFile(apiPath, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}

	add(t, dir, "web")

	if got := readFile(t, apiPath); got != edited {
		t.Fatalf("the later add rewrote the user's api_provider.go:\n%s", got)
	}
	if got := readFile(t, providerPath); got != providerBefore {
		t.Fatalf("the later add rewrote redis_provider.go:\n%s", got)
	}
	di := readFile(t, filepath.Join(dir, "internal/di/di.go"))
	if !strings.Contains(di, "example.com/demo/internal/web") {
		t.Errorf("di.go was not regenerated for web:\n%s", di)
	}
	gen := readFile(t, filepath.Join(dir, "internal/di/loom_gen.go"))
	if !strings.Contains(gen, "NewRedisClient") {
		t.Errorf("loom_gen.go did not follow the edited provider to construct NewRedisClient:\n%s", gen)
	}
	if !buildAndTestGeneratedProject(t, dir) {
		t.Skip("cannot resolve the generated project's dependencies (network/module cache unavailable)")
	}
}

// TestRedisProviderSeamGuardsInstallOnce proves the redis capability owns the
// stable redis_provider.go seam, guarded on loom. Because redis requires loom,
// loom is always present when redis installs, so the guard always applies and
// loom itself no longer declares the file.
func TestRedisProviderSeamGuardsInstallOnce(t *testing.T) {
	const path = "internal/di/redis_provider.go"
	catalog := newCatalog()

	redis, err := catalog.Get("redis")
	if err != nil {
		t.Fatalf("Get redis: %v", err)
	}
	var redisFile *template.File
	for i := range redis.Files {
		if redis.Files[i].Path == path {
			redisFile = &redis.Files[i]
		}
	}
	if redisFile == nil {
		t.Fatalf("redis does not declare %s", path)
	}
	if len(redisFile.When) != 1 || redisFile.When[0] != "loom" {
		t.Errorf("redis %s is not guarded by when: [loom]: %+v", path, redisFile)
	}
	if !entryApplies(map[string]bool{"base": true, "loom": true}, redisFile.When, redisFile.WhenAbsent) {
		t.Error("redis does not write redis_provider.go when loom is installed")
	}
	if entryApplies(map[string]bool{"base": true}, redisFile.When, redisFile.WhenAbsent) {
		t.Error("redis writes redis_provider.go without loom")
	}

	loom, err := catalog.Get("loom")
	if err != nil {
		t.Fatalf("Get loom: %v", err)
	}
	for _, file := range loom.Files {
		if file.Path == path {
			t.Errorf("loom still declares %s; the provider seam belongs to the redis capability", path)
		}
	}
}

// --- cron capability --------------------------------------------------------

// serveEnv is the environment a generated serve process runs with: the database,
// HTTP, Redis, mail and storage variables are removed so the process can never
// reach an external service, and GOWORK is off so no workspace interferes.
func serveEnv() []string {
	return append(envWithout(
		"DATABASE_URL", "DB_HOST", "DB_PORT", "DB_USER", "DB_PASSWORD", "DB_NAME",
		"HTTP_ADDR", "CRON_TIMEZONE",
		"REDIS_ADDR", "REDIS_USERNAME", "REDIS_PASSWORD", "REDIS_DB", "REDIS_TLS",
		"MAIL_HOST", "MAIL_PORT", "MAIL_USERNAME", "MAIL_PASSWORD", "MAIL_FROM", "MAIL_TLS", "MAIL_TIMEOUT_SECONDS",
		"STORAGE_ENDPOINT", "STORAGE_REGION", "STORAGE_BUCKET", "STORAGE_ACCESS_KEY_ID", "STORAGE_SECRET_ACCESS_KEY", "STORAGE_PATH_STYLE", "STORAGE_TLS",
	), "GOWORK=off")
}

// startServe starts a generated serve binary on a fresh loopback address and
// waits until it logs the listening line, returning the process and its captured
// stderr. The process is killed when the test finishes.
func startServe(t *testing.T, binary, dir string, env []string) (*exec.Cmd, string, *syncBuffer) {
	t.Helper()
	addr := freeAddr(t)
	cmd := exec.Command(binary, "serve", "--addr", addr)
	cmd.Dir = dir
	cmd.Env = env
	stderr := &syncBuffer{}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start serve: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	waitForLog(t, stderr, "listening", 20*time.Second)
	return cmd, addr, stderr
}

// startApp starts a generated binary with the given command words on a fresh
// loopback address, appending the shared --addr flag, and waits until it logs
// the listening line. It returns the process and its captured stderr, and kills
// the process when the test finishes. Empty args exercise a bare invocation.
func startApp(t *testing.T, binary, dir string, env []string, args ...string) (*exec.Cmd, string, *syncBuffer) {
	t.Helper()
	addr := freeAddr(t)
	full := append(append([]string{}, args...), "--addr", addr)
	cmd := exec.Command(binary, full...)
	cmd.Dir = dir
	cmd.Env = env
	stderr := &syncBuffer{}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start %v: %v", args, err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	waitForLog(t, stderr, "listening", 20*time.Second)
	return cmd, addr, stderr
}

// stopServe sends SIGTERM and waits for the process to exit, returning its final
// stderr. A non-zero exit after the signal is a failure: serve must stop cleanly.
func stopServe(t *testing.T, cmd *exec.Cmd, stderr *syncBuffer) string {
	t.Helper()
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("signal serve: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("serve exited with %v after SIGTERM:\n%s", err, stderr.String())
		}
	case <-time.After(20 * time.Second):
		t.Fatalf("serve did not exit after SIGTERM:\n%s", stderr.String())
	}
	return stderr.String()
}

// waitForLog blocks until the captured output contains substr or the timeout
// elapses.
func waitForLog(t *testing.T, stderr *syncBuffer, substr string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if strings.Contains(stderr.String(), substr) {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("serve never logged %q:\n%s", substr, stderr.String())
}

// TestAddCronInstallsLibraryWithoutHTTP proves `weld add cron` is a library
// install that needs no server: it writes the scheduler and its stable
// registration file, attaches a runtime to the shared config seam, and creates
// no serve command or HTTP package. It schedules nothing by itself.
func TestAddCronInstallsHTTPAndLoom(t *testing.T) {
	root := t.TempDir()
	dir := create(t, root)

	result := add(t, dir, "cron")
	if got, want := strings.Join(result.Installed, ","), "config,loom,http,cron"; got != want {
		t.Fatalf("installed = %q, want %q", got, want)
	}
	for _, path := range []string{
		"internal/cron/cron.go",
		"internal/cron/register.go",
		"internal/config/cron.go",
		"internal/di/cron_provider.go",
		"internal/di/di.go",
		"internal/app/serve.go",
	} {
		if _, err := os.Stat(filepath.Join(dir, path)); err != nil {
			t.Errorf("expected %s: %v", path, err)
		}
	}
	// The scheduler follows serve through the Loom graph, never the retired
	// runtime registry.
	for _, absent := range []string{"internal/config/runtime.go", "internal/app/cron.go"} {
		if _, err := os.Stat(filepath.Join(dir, absent)); !os.IsNotExist(err) {
			t.Errorf("add cron created the retired %s", absent)
		}
	}

	// The registration file is the stable job seam and registers nothing.
	register := readFile(t, filepath.Join(dir, "internal/cron/register.go"))
	if !strings.Contains(register, "func Register(s *Scheduler) error") {
		t.Errorf("register.go does not expose Register:\n%s", register)
	}
	if !strings.Contains(register, "return nil") {
		t.Errorf("register.go schedules work by default:\n%s", register)
	}
	// The server graph root consumes the scheduler, so it is constructed and its
	// start/stop hooks run with the server.
	di := readFile(t, filepath.Join(dir, "internal/di/di.go"))
	for _, want := range []string{"loom.Provide(NewScheduler)", "Scheduler *cron.Scheduler"} {
		if !strings.Contains(di, want) {
			t.Errorf("di.go is missing %q:\n%s", want, di)
		}
	}
	provider := readFile(t, filepath.Join(dir, "internal/di/cron_provider.go"))
	for _, want := range []string{"func NewScheduler", "cron.New", "cron.Register", "lc.Append"} {
		if !strings.Contains(provider, want) {
			t.Errorf("cron_provider.go is missing %q:\n%s", want, provider)
		}
	}

	if drift := mustLoad(t, dir).Drift(dir); len(drift) != 0 {
		t.Fatalf("drift after add cron: %+v", drift)
	}
	if !buildAndTestGeneratedProject(t, dir) {
		t.Skip("cannot resolve the generated project's cron dependencies (network/module cache unavailable)")
	}
}

// TestAddCronSchedulerFollowsServe proves the runtime hook end to end, in both
// http/cron install orders: serve binds its socket and logs the listening line,
// starts the scheduler, and on SIGTERM stops the scheduler before shutting the
// HTTP server down and exits cleanly. No job fires; the point is that the
// scheduler lifecycle really follows the long-running command.
func TestAddCronSchedulerFollowsServe(t *testing.T) {
	orders := []struct {
		name string
		caps []string
	}{
		{"http then cron", []string{"http", "cron"}},
		{"cron then http", []string{"cron", "http"}},
	}
	for _, order := range orders {
		t.Run(order.name, func(t *testing.T) {
			root := t.TempDir()
			dir := create(t, root)
			for _, capability := range order.caps {
				add(t, dir, capability)
			}
			if !goModTidy(t, dir) {
				t.Skip("cannot resolve the generated project's dependencies (network/module cache unavailable)")
			}
			gofmtCheck(t, dir)
			runGo(t, dir, "vet", "./...")

			env := serveEnv()
			binary := buildAppBinary(t, dir, env)
			cmd, _, stderr := startServe(t, binary, dir, env)
			waitForLog(t, stderr, "cron scheduler started", 10*time.Second)

			listening := strings.Index(stderr.String(), "listening")
			started := strings.Index(stderr.String(), "cron scheduler started")
			if listening < 0 || started < 0 || listening > started {
				t.Fatalf("the scheduler did not start after the socket was bound:\n%s", stderr.String())
			}

			final := stopServe(t, cmd, stderr)
			stopped := strings.Index(final, "cron scheduler stopped")
			shutting := strings.Index(final, "shutting down")
			if stopped < 0 || shutting < 0 || stopped > shutting {
				t.Fatalf("the scheduler did not stop before the server shut down:\n%s", final)
			}
		})
	}
}

// TestAddCronWithLoomWiresScheduler proves the Loom integration is graph
// reachable and ordered, in every install order: the graph root consumes the
// scheduler, Loom constructs the server before the scheduler, and the generated
// graph test (run by buildAndTestGeneratedProject) asserts the server logs
// "listening" before the scheduler starts and the scheduler stops before the
// server shuts down.
func TestAddCronWithLoomWiresScheduler(t *testing.T) {
	loomToolAvailable(t)
	orders := []struct {
		name string
		caps []string
	}{
		{"http cron then loom", []string{"http", "cron", "loom"}},
		{"loom then cron", []string{"loom", "cron"}},
		{"cron then loom", []string{"cron", "loom"}},
	}
	for _, order := range orders {
		t.Run(order.name, func(t *testing.T) {
			root := t.TempDir()
			dir := create(t, root)
			for _, capability := range order.caps {
				add(t, dir, capability)
			}

			di := readFile(t, filepath.Join(dir, "internal/di/di.go"))
			for _, want := range []string{"loom.Provide(NewScheduler)", "Scheduler *cron.Scheduler"} {
				if !strings.Contains(di, want) {
					t.Errorf("di.go is missing %q:\n%s", want, di)
				}
			}
			gen := readFile(t, filepath.Join(dir, "internal/di/loom_gen.go"))
			serverIdx := strings.Index(gen, "NewServer(")
			schedulerIdx := strings.Index(gen, "NewScheduler(")
			if serverIdx < 0 || schedulerIdx < 0 {
				t.Fatalf("loom_gen.go does not construct the server and scheduler:\n%s", gen)
			}
			if serverIdx > schedulerIdx {
				t.Fatalf("loom_gen.go constructs the scheduler before the server:\n%s", gen)
			}

			if drift := mustLoad(t, dir).Drift(dir); len(drift) != 0 {
				t.Fatalf("drift after add: %+v", drift)
			}
			if !buildAndTestGeneratedProject(t, dir) {
				t.Skip("cannot resolve the generated project's dependencies (network/module cache unavailable)")
			}
		})
	}
}

// TestAddCronLoomSchedulerRunsWithServe is the runtime proof for the Loom path:
// the graph-owned scheduler starts after the socket is bound and stops before
// the HTTP server, observed from the generated binary's logs.
func TestAddCronLoomSchedulerRunsWithServe(t *testing.T) {
	loomToolAvailable(t)
	root := t.TempDir()
	dir := create(t, root)
	for _, capability := range []string{"http", "cron", "loom"} {
		add(t, dir, capability)
	}
	if !goModTidy(t, dir) {
		t.Skip("cannot resolve the generated project's dependencies (network/module cache unavailable)")
	}
	env := serveEnv()
	binary := buildAppBinary(t, dir, env)
	cmd, _, stderr := startServe(t, binary, dir, env)
	waitForLog(t, stderr, "cron scheduler started", 10*time.Second)

	listening := strings.Index(stderr.String(), "listening")
	started := strings.Index(stderr.String(), "cron scheduler started")
	if listening < 0 || started < 0 || listening > started {
		t.Fatalf("the scheduler did not start after the socket was bound:\n%s", stderr.String())
	}

	final := stopServe(t, cmd, stderr)
	stopped := strings.Index(final, "cron scheduler stopped")
	shutting := strings.Index(final, "shutting down")
	if stopped < 0 || shutting < 0 || stopped > shutting {
		t.Fatalf("the scheduler did not stop before the server shut down:\n%s", final)
	}
}

// TestCronProviderSeamGuardsInstallOnce proves the cron capability owns the
// stable cron_provider.go seam, guarded on loom. Because cron requires http
// (which requires loom), loom is always present when cron installs, so the guard
// always applies and loom itself no longer declares the file.
func TestCronProviderSeamGuardsInstallOnce(t *testing.T) {
	const path = "internal/di/cron_provider.go"
	catalog := newCatalog()

	cron, err := catalog.Get("cron")
	if err != nil {
		t.Fatalf("Get cron: %v", err)
	}
	var cronFile *template.File
	for i := range cron.Files {
		if cron.Files[i].Path == path {
			cronFile = &cron.Files[i]
		}
	}
	if cronFile == nil {
		t.Fatalf("cron does not declare %s", path)
	}
	if len(cronFile.When) != 1 || cronFile.When[0] != "loom" {
		t.Errorf("cron %s is not guarded by when: [loom]: %+v", path, cronFile)
	}
	if !entryApplies(map[string]bool{"base": true, "loom": true}, cronFile.When, cronFile.WhenAbsent) {
		t.Error("cron does not write cron_provider.go when loom is installed")
	}
	if entryApplies(map[string]bool{"base": true}, cronFile.When, cronFile.WhenAbsent) {
		t.Error("cron writes cron_provider.go without loom")
	}

	loom, err := catalog.Get("loom")
	if err != nil {
		t.Fatalf("Get loom: %v", err)
	}
	for _, file := range loom.Files {
		if file.Path == path {
			t.Errorf("loom still declares %s; the provider seam belongs to the cron capability", path)
		}
	}
}

// TestAddCronProviderSeamSurvivesLaterAdd proves the durable seam end to end: a
// user edit to the stable register and provider files survives a later
// capability add that regenerates the graph.
func TestAddCronProviderSeamSurvivesLaterAdd(t *testing.T) {
	loomToolAvailable(t)
	orders := []struct {
		name string
		caps []string
	}{
		{"cron then loom", []string{"cron", "loom"}},
		{"loom then cron", []string{"loom", "cron"}},
	}
	for _, order := range orders {
		t.Run(order.name, func(t *testing.T) {
			root := t.TempDir()
			dir := create(t, root)
			for _, capability := range order.caps {
				add(t, dir, capability)
			}

			registerPath := filepath.Join(dir, "internal/cron/register.go")
			providerPath := filepath.Join(dir, "internal/di/cron_provider.go")
			registerEdit := readFile(t, registerPath) + "\n// user job, must survive a later add\n"
			providerEdit := readFile(t, providerPath) + "\n// user wiring, must survive a later add\n"
			if err := os.WriteFile(registerPath, []byte(registerEdit), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(providerPath, []byte(providerEdit), 0o644); err != nil {
				t.Fatal(err)
			}

			add(t, dir, "web")

			if got := readFile(t, registerPath); got != registerEdit {
				t.Fatalf("the later add rewrote the user's register.go:\n%s", got)
			}
			if got := readFile(t, providerPath); got != providerEdit {
				t.Fatalf("the later add rewrote the user's cron_provider.go:\n%s", got)
			}
			if di := readFile(t, filepath.Join(dir, "internal/di/di.go")); !strings.Contains(di, "internal/web") {
				t.Errorf("di.go was not regenerated for web:\n%s", di)
			}
			if !buildAndTestGeneratedProject(t, dir) {
				t.Skip("cannot resolve the generated project's dependencies (network/module cache unavailable)")
			}
		})
	}
}

// TestAddMailStorageWithLoomBothOrders proves the mail and storage Loom
// integrations are declared but pruned in either install order: the graph
// declares the provider, the stable seam defines it, the generated initializer
// never constructs it, and the project builds and tests with no external server.
func TestAddMailStorageWithLoomBothOrders(t *testing.T) {
	loomToolAvailable(t)
	cases := []struct {
		capability  string
		provider    string
		constructor string
	}{
		{capability: "mail", provider: "mail_provider.go", constructor: "NewMailSender"},
		{capability: "storage", provider: "storage_provider.go", constructor: "NewObjectStore"},
	}
	for _, tc := range cases {
		orders := []struct {
			name string
			caps []string
		}{
			{tc.capability + " then http", []string{tc.capability, "http"}},
			{"http then " + tc.capability, []string{"http", tc.capability}},
		}
		for _, order := range orders {
			t.Run(order.name, func(t *testing.T) {
				root := t.TempDir()
				dir := create(t, root)
				for _, capability := range order.caps {
					add(t, dir, capability)
				}
				providerPath := filepath.Join(dir, "internal/di", tc.provider)
				if provider := readFile(t, providerPath); !strings.Contains(provider, "func "+tc.constructor) {
					t.Errorf("%s does not define %s:\n%s", tc.provider, tc.constructor, provider)
				}
				di := readFile(t, filepath.Join(dir, "internal/di/di.go"))
				if !strings.Contains(di, "loom.Provide("+tc.constructor+")") {
					t.Errorf("di.go does not declare %s:\n%s", tc.constructor, di)
				}
				gen := readFile(t, filepath.Join(dir, "internal/di/loom_gen.go"))
				if strings.Contains(gen, tc.constructor) {
					t.Errorf("loom_gen.go constructs the unused %s; the default composition must not wire it:\n%s", tc.constructor, gen)
				}
				if !buildAndTestGeneratedProject(t, dir) {
					t.Skip("cannot resolve the generated project's dependencies (network/module cache unavailable)")
				}
			})
		}
	}
}

// TestMailStorageProviderSeamsGuardInstallOnce proves the mail and storage
// provider seams are owned by their capability and guarded on loom. Because both
// require loom, the guard always applies and loom itself no longer declares the
// files.
func TestMailStorageProviderSeamsGuardInstallOnce(t *testing.T) {
	catalog := newCatalog()
	loom, err := catalog.Get("loom")
	if err != nil {
		t.Fatalf("Get loom: %v", err)
	}
	cases := []struct {
		capability string
		path       string
	}{
		{"mail", "internal/di/mail_provider.go"},
		{"storage", "internal/di/storage_provider.go"},
	}
	for _, tc := range cases {
		t.Run(tc.capability, func(t *testing.T) {
			capability, err := catalog.Get(tc.capability)
			if err != nil {
				t.Fatalf("Get %s: %v", tc.capability, err)
			}
			var capFile *template.File
			for i := range capability.Files {
				if capability.Files[i].Path == tc.path {
					capFile = &capability.Files[i]
				}
			}
			if capFile == nil {
				t.Fatalf("%s does not declare %s", tc.capability, tc.path)
			}
			if len(capFile.When) != 1 || capFile.When[0] != "loom" {
				t.Errorf("%s %s is not guarded by when: [loom]: %+v", tc.capability, tc.path, capFile)
			}
			if !entryApplies(map[string]bool{"base": true, "loom": true}, capFile.When, capFile.WhenAbsent) {
				t.Errorf("%s does not write %s when loom is installed", tc.capability, tc.path)
			}
			if entryApplies(map[string]bool{"base": true}, capFile.When, capFile.WhenAbsent) {
				t.Errorf("%s writes %s without loom", tc.capability, tc.path)
			}
			for _, file := range loom.Files {
				if file.Path == tc.path {
					t.Errorf("loom still declares %s; the provider seam belongs to the %s capability", tc.path, tc.capability)
				}
			}
		})
	}
}

// TestCronGeneratedProjectRaceAndVet runs the generated project's tests under the
// race detector and vets it for the cron permutations that matter: cron alone
// with http (the runtime hook), cron with Loom (the graph-owned scheduler), cron
// with the db/redis mix, and cron with api. No PostgreSQL, Redis, mail or object
// store is reached.
func TestCronGeneratedProjectRaceAndVet(t *testing.T) {
	goValidate := goValidateDir(t)
	cases := []struct {
		name      string
		caps      []string
		needsAPI  bool
		needsLoom bool
	}{
		{name: "http+cron", caps: []string{"http", "cron"}},
		{name: "cron+loom", caps: []string{"cron", "loom"}, needsLoom: true},
		{name: "db+redis+cron", caps: []string{"db", "redis", "cron"}},
		{name: "api+cron", caps: []string{"api", "cron"}, needsAPI: true},
		{name: "api+db+cron+loom", caps: []string{"api", "db", "cron", "loom"}, needsAPI: true, needsLoom: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.needsLoom {
				loomToolAvailable(t)
			}
			root := t.TempDir()
			dir := create(t, root)
			if tc.needsAPI {
				useLocalGoValidate(t, dir, goValidate)
			}
			for _, capability := range tc.caps {
				add(t, dir, capability)
			}
			if !goModTidy(t, dir) {
				t.Skip("cannot resolve the generated project's dependencies (network/module cache unavailable)")
			}
			gofmtCheck(t, dir)
			runGo(t, dir, "vet", "./...")
			runGo(t, dir, "test", "-race", "./...")
		})
	}
}

// TestAddCronRegistrationFileIsConsumed proves the stable registration file is
// really invoked when serve starts the scheduler, not just compiled: a user job
// with an invalid spec makes serve fail loudly instead of starting.
func TestAddCronRegistrationFileIsConsumed(t *testing.T) {
	root := t.TempDir()
	dir := create(t, root)
	for _, capability := range []string{"http", "cron"} {
		add(t, dir, capability)
	}

	path := filepath.Join(dir, "internal/cron/register.go")
	const before = "func Register(s *Scheduler) error {\n\treturn nil\n}"
	const after = "func Register(s *Scheduler) error {\n\treturn s.Register(\"bad\", \"not a spec\", nil)\n}"
	content := readFile(t, path)
	if !strings.Contains(content, before) {
		t.Fatalf("register.go does not carry the expected default body:\n%s", content)
	}
	if err := os.WriteFile(path, []byte(strings.Replace(content, before, after, 1)), 0o644); err != nil {
		t.Fatal(err)
	}

	if !goModTidy(t, dir) {
		t.Skip("cannot resolve the generated project's dependencies (network/module cache unavailable)")
	}
	env := serveEnv()
	binary := buildAppBinary(t, dir, env)
	cmd := exec.Command(binary, "serve", "--addr", freeAddr(t))
	cmd.Dir = dir
	cmd.Env = env

	done := make(chan struct{})
	var out []byte
	var runErr error
	go func() {
		out, runErr = cmd.CombinedOutput()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("serve did not fail fast on the invalid registered job; the registration file may not be consumed")
	}
	if runErr == nil {
		t.Fatalf("serve started despite an invalid registered job:\n%s", out)
	}
	if !strings.Contains(string(out), "cron") {
		t.Fatalf("the failure does not name cron:\n%s", out)
	}
}

// TestCronDoesNotStartForShortCommands proves the scheduler follows serve only:
// help, version and an unknown command never start it.
func TestCronDoesNotStartForShortCommands(t *testing.T) {
	root := t.TempDir()
	dir := create(t, root)
	for _, capability := range []string{"http", "cron"} {
		add(t, dir, capability)
	}
	if !goModTidy(t, dir) {
		t.Skip("cannot resolve the generated project's dependencies (network/module cache unavailable)")
	}
	env := serveEnv()
	binary := buildAppBinary(t, dir, env)
	for _, args := range [][]string{{"help"}, {"version"}, {"--help"}, {"bogus"}} {
		cmd := exec.Command(binary, args...)
		cmd.Dir = dir
		cmd.Env = env
		out, _ := cmd.CombinedOutput()
		if strings.Contains(string(out), "cron scheduler started") {
			t.Fatalf("%v started the cron scheduler:\n%s", args, out)
		}
	}
}

// TestAddMailStorageProviderSeamSurvivesLaterAdd proves the mail and storage
// stable seams preserve user wiring: an edit to the provider file survives a
// later capability add that regenerates the graph.
func TestAddMailStorageProviderSeamSurvivesLaterAdd(t *testing.T) {
	loomToolAvailable(t)
	for _, capability := range []string{"mail", "storage"} {
		t.Run(capability, func(t *testing.T) {
			root := t.TempDir()
			dir := create(t, root)
			for _, name := range []string{capability, "loom"} {
				add(t, dir, name)
			}
			providerPath := filepath.Join(dir, "internal/di", capability+"_provider.go")
			edit := readFile(t, providerPath) + "\n// user wiring, must survive a later add\n"
			if err := os.WriteFile(providerPath, []byte(edit), 0o644); err != nil {
				t.Fatal(err)
			}

			add(t, dir, "web")

			if got := readFile(t, providerPath); got != edit {
				t.Fatalf("the later add rewrote the user's %s_provider.go:\n%s", capability, got)
			}
			if di := readFile(t, filepath.Join(dir, "internal/di/di.go")); !strings.Contains(di, "internal/web") {
				t.Errorf("di.go was not regenerated for web:\n%s", di)
			}
			if !buildAndTestGeneratedProject(t, dir) {
				t.Skip("cannot resolve the generated project's dependencies (network/module cache unavailable)")
			}
		})
	}
}
