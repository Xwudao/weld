package scaffold

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
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
	// The base go.mod carries the weld:deps extension point but requires nothing
	// until a capability patches it.
	goMod := readFile(t, filepath.Join(dir, "go.mod"))
	if !strings.Contains(goMod, "weld:deps:begin") || !strings.Contains(goMod, "weld:deps:end") {
		t.Errorf("base go.mod is missing the weld:deps extension point:\n%s", goMod)
	}
	if strings.Contains(goMod, "go-validate") {
		t.Errorf("base go.mod already requires go-validate:\n%s", goMod)
	}
	if app := readFile(t, filepath.Join(dir, "internal/app/app.go")); strings.Contains(app, "net/http") || strings.Contains(app, "\"serve\"") {
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

	if got, want := strings.Join(result.Installed, ","), "http,web"; got != want {
		t.Fatalf("installed = %q, want %q", got, want)
	}
	for _, path := range []string{
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
		"internal/httpserver/web_route.go",
		// http capability, installed as a dependency
		"internal/httpserver/http.go",
		"internal/httpserver/server.go",
		"internal/httpserver/http_test.go",
		"internal/app/serve.go",
		"internal/app/serve_test.go",
	} {
		if _, err := os.Stat(filepath.Join(dir, path)); err != nil {
			t.Errorf("expected %s: %v", path, err)
		}
	}

	// The SPA route is wired through the httpserver extension point.
	httpGo := readFile(t, filepath.Join(dir, "internal/httpserver/http.go"))
	if !strings.Contains(httpGo, "routes = append(routes, installWebRoute)") {
		t.Errorf("routes extension point not patched:\n%s", httpGo)
	}
	if !strings.Contains(httpGo, "weld:web:installed") {
		t.Errorf("web sentinel missing:\n%s", httpGo)
	}
	if strings.Count(httpGo, "weld:routes:begin") != 1 {
		t.Errorf("routes region duplicated:\n%s", httpGo)
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

	gofmtCheck(t, dir)
	goBuild(t, dir)
	goTest(t, dir, "./...")
}

func TestAddHTTPAloneInstallsOnlyHTTP(t *testing.T) {
	root := t.TempDir()
	dir := create(t, root)

	result := add(t, dir, "http")
	if got, want := strings.Join(result.Installed, ","), "http"; got != want {
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
	// With only http installed the serve command exists but serves nothing.
	if httpGo := readFile(t, filepath.Join(dir, "internal/httpserver/http.go")); !strings.Contains(httpGo, "weld:routes:begin") {
		t.Fatalf("routes region missing:\n%s", httpGo)
	}

	gofmtCheck(t, dir)
	goBuild(t, dir)
	goTest(t, dir, "./...")
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
	goBuild(t, dir)
	goTest(t, dir, "./...")
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

	untouched := []string{
		"main.go",
		"internal/app/app.go",
		"README.md",
		"go.mod",
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

// goValidateDir returns the sibling go-validate working copy, or "" when it is
// not checked out.
//
// Generated API projects depend on go-validate's Spec/Constraint API, which is
// not published yet (the latest tag is v0.1.1). Local integration tests point a
// test-only replace at the working copy; a real release must publish and pin
// the new version first, and the integration tests below skip visibly until
// then.
func goValidateDir(t *testing.T) string {
	t.Helper()
	if dir := os.Getenv("WELD_GO_VALIDATE_DIR"); dir != "" {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		return ""
	}
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		candidate := filepath.Join(dir, "go-validate")
		if _, err := os.Stat(filepath.Join(candidate, "go.mod")); err == nil {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// useLocalGoValidate appends a test-only replace to a generated go.mod. It is
// never written by weld into a scaffolded project; it exists so a local test
// can compile against the unpublished go-validate working copy.
func useLocalGoValidate(t *testing.T, dir, goValidateDir string) {
	t.Helper()
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
	if got, want := strings.Join(result.Installed, ","), "http,api"; got != want {
		t.Fatalf("installed = %q, want %q", got, want)
	}
	for _, path := range []string{
		"internal/api/dto.go",
		"internal/api/service.go",
		"internal/api/handler.go",
		"internal/api/openapi.go",
		"internal/api/api_test.go",
		"internal/api/openapi_test.go",
		"internal/httpserver/api_route.go",
		"internal/httpserver/api_test.go",
		"internal/httpserver/http.go",
	} {
		if _, err := os.Stat(filepath.Join(dir, path)); err != nil {
			t.Errorf("expected %s: %v", path, err)
		}
	}
	// api requires http but never web, db or loom.
	for _, path := range []string{"internal/web", "web"} {
		if _, err := os.Stat(filepath.Join(dir, path)); !os.IsNotExist(err) {
			t.Errorf("add api created %s", path)
		}
	}

	// The API route is wired through the httpserver extension point and the
	// dependency region of go.mod is patched.
	httpGo := readFile(t, filepath.Join(dir, "internal/httpserver/http.go"))
	if !strings.Contains(httpGo, "routes = append(routes, installAPIRoute)") {
		t.Errorf("routes extension point not patched:\n%s", httpGo)
	}
	if !strings.Contains(httpGo, "weld:api:installed") {
		t.Errorf("api sentinel missing:\n%s", httpGo)
	}
	if strings.Count(httpGo, "weld:routes:begin") != 1 {
		t.Errorf("routes region duplicated:\n%s", httpGo)
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
// API project with the go-validate working copy and the kin-openapi test
// dependency. It skips visibly when either is unavailable.
func TestAddAPIGeneratedProjectBuildsAndTests(t *testing.T) {
	goValidate := goValidateDir(t)
	if goValidate == "" {
		t.Skip("generated API projects require the unpublished go-validate Spec API; sibling go-validate not found (set WELD_GO_VALIDATE_DIR). Publishing and pinning go-validate is required before release.")
	}
	root := t.TempDir()
	dir := create(t, root)
	add(t, dir, "api")

	gofmtCheck(t, dir)
	useLocalGoValidate(t, dir, goValidate)
	goBuild(t, dir)
	if !goModTidy(t, dir) {
		t.Skip("cannot resolve the generated project's test dependencies (network/module cache unavailable)")
	}
	goTest(t, dir, "./...")
	runGo(t, dir, "vet", "./...")
}

// TestAddAPIAndWebInEitherOrder proves the two capabilities compose on one
// server in both orders: each requires http, both patch the one routes region,
// and the generated project compiles and tests either way.
func TestAddAPIAndWebInEitherOrder(t *testing.T) {
	goValidate := goValidateDir(t)
	if goValidate == "" {
		t.Skip("generated API projects require the unpublished go-validate Spec API; sibling go-validate not found (set WELD_GO_VALIDATE_DIR). Publishing and pinning go-validate is required before release.")
	}
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

			httpGo := readFile(t, filepath.Join(dir, "internal/httpserver/http.go"))
			for _, want := range []string{
				"routes = append(routes, installWebRoute)",
				"routes = append(routes, installAPIRoute)",
			} {
				if !strings.Contains(httpGo, want) {
					t.Errorf("%q missing from the routes region:\n%s", want, httpGo)
				}
			}
			if strings.Count(httpGo, "weld:routes:begin") != 1 || strings.Count(httpGo, "weld:routes:end") != 1 {
				t.Fatalf("routes region duplicated:\n%s", httpGo)
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
			goBuild(t, dir)
			if !goModTidy(t, dir) {
				t.Skip("cannot resolve the generated project's test dependencies (network/module cache unavailable)")
			}
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
	if got, want := strings.Join(result.Installed, ","), "db"; got != want {
		t.Fatalf("installed = %q, want %q", got, want)
	}
	for _, path := range []string{
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
// the JSON API stays independent of the sqlc rows. api needs the unpublished
// go-validate Spec API, so it uses the same test-only local replace as the
// other api integration tests and skips visibly when that working copy is
// absent.
func TestAddDBComposesWithAPI(t *testing.T) {
	goValidate := goValidateDir(t)
	if goValidate == "" {
		t.Skip("generated API projects require the unpublished go-validate Spec API; sibling go-validate not found (set WELD_GO_VALIDATE_DIR). Publishing and pinning go-validate is required before release.")
	}
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

func TestAddLoomInstallsHTTPAndRendersGraph(t *testing.T) {
	loomToolAvailable(t)
	root := t.TempDir()
	dir := create(t, root)

	result := add(t, dir, "loom")
	if got, want := strings.Join(result.Installed, ","), "http,loom"; got != want {
		t.Fatalf("installed = %q, want %q", got, want)
	}
	for _, path := range []string{
		"internal/di/di.go",
		"internal/di/loom_gen.go",
		"internal/di/di_test.go",
		"internal/app/serve_loom.go",
		"tools/loom/go.mod",
		"tools/loom/go.sum",
	} {
		if _, err := os.Stat(filepath.Join(dir, path)); err != nil {
			t.Errorf("expected %s: %v", path, err)
		}
	}
	// The initializer is generator output, not hand-written.
	gen := readFile(t, filepath.Join(dir, "internal/di/loom_gen.go"))
	if !strings.Contains(gen, "Code generated by loom. DO NOT EDIT.") {
		t.Errorf("loom_gen.go is not generator output:\n%s", gen)
	}
	if !strings.Contains(gen, "func InitApp(ctx context.Context)") {
		t.Errorf("loom_gen.go has no InitApp:\n%s", gen)
	}
	// Exactly one serve command remains, now driven by the graph.
	serve := readFile(t, filepath.Join(dir, "internal/app/serve.go"))
	if strings.Count(serve, `Name:    "serve"`) != 1 {
		t.Errorf("serve command count = %d, want 1:\n%s", strings.Count(serve, `Name:    "serve"`), serve)
	}
	if !strings.Contains(serve, "runServeLoom") {
		t.Errorf("serve command is not wired to the graph:\n%s", serve)
	}
	// opt-in Go floor is raised and Loom is a direct dependency.
	goMod := readFile(t, filepath.Join(dir, "go.mod"))
	if !strings.Contains(goMod, "go 1.25.0") || !strings.Contains(goMod, "require github.com/Xwudao/loom v0.3.1") {
		t.Errorf("go.mod is missing the Loom floor/dependency:\n%s", goMod)
	}
	// Loom pulls http only; it never pulls db, api or web.
	for _, path := range []string{"internal/data", "internal/api", "internal/web"} {
		if _, err := os.Stat(filepath.Join(dir, path)); !os.IsNotExist(err) {
			t.Errorf("add loom created %s", path)
		}
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
	for _, path := range []string{"internal/di/di.go", "internal/di/loom_gen.go", "go.mod", project.ManifestName} {
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

// TestAddLoomGraphFollowsInstalledCapabilities proves the graph is regenerated
// for the installed set in either order: db installed before or after loom both
// end with the pool and repository wired into the initializer.
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
			if !strings.Contains(di, "data.Repository") || !strings.Contains(di, "NewPool") {
				t.Errorf("di.go does not bind the repository:\n%s", di)
			}
			gen := readFile(t, filepath.Join(dir, "internal/di/loom_gen.go"))
			for _, want := range []string{"NewPool", "NewRepository"} {
				if !strings.Contains(gen, want) {
					t.Errorf("loom_gen.go does not construct %s; db is not wired:\n%s", want, gen)
				}
			}
			// The pool's constructor cleanup must reach the lifecycle, so the pool
			// cannot leak on construction failure or normal stop.
			if !strings.Contains(gen, "AddCleanup") {
				t.Errorf("loom_gen.go does not register the pool cleanup:\n%s", gen)
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

// TestAddLoomDBWithoutDSNNeedsNoDatabase is the regression for the implicit
// production dsn: with db and loom installed and DATABASE_URL unset, the
// generated project must still build and test (the graph tests inject a fake
// environment), and only resolving the graph may fail. The failure must name
// DATABASE_URL and never invent or echo a dsn.
func TestAddLoomDBWithoutDSNNeedsNoDatabase(t *testing.T) {
	loomToolAvailable(t)
	root := t.TempDir()
	dir := create(t, root)
	add(t, dir, "db")
	add(t, dir, "loom")

	clean := envWithout("DATABASE_URL")
	gofmtCheck(t, dir)
	if !goModTidy(t, dir) {
		t.Skip("cannot resolve the generated project's dependencies (network/module cache unavailable)")
	}
	runGoWithEnv(t, dir, clean, "build", "./...")
	runGoWithEnv(t, dir, clean, "vet", "./...")
	runGoWithEnv(t, dir, clean, "test", "./...")

	// The serve command resolves the graph, and that is the only step that needs
	// DATABASE_URL. Running it without the variable must fail with a message that
	// names the variable and no dsn.
	binary := filepath.Join(t.TempDir(), "app")
	build := exec.Command("go", "build", "-o", binary, ".")
	build.Dir = dir
	build.Env = append(clean, "GOPROXY=off", "GOWORK=off")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	run := exec.Command(binary, "serve")
	run.Dir = dir
	run.Env = clean
	out, err := run.CombinedOutput()
	if err == nil {
		t.Fatalf("serve succeeded without DATABASE_URL:\n%s", out)
	}
	if !strings.Contains(string(out), "DATABASE_URL") {
		t.Fatalf("serve error does not name DATABASE_URL:\n%s", out)
	}
	if strings.Contains(string(out), "postgres://") {
		t.Fatalf("serve error invents or echoes a dsn:\n%s", out)
	}
}

// TestAddLoomGenerationIsReproducible runs the pinned generator in dry-run and
// requires it to report no change, so the committed initializer cannot drift
// from the graph.
func TestAddLoomGenerationIsReproducible(t *testing.T) {
	loomToolAvailable(t)
	root := t.TempDir()
	dir := create(t, root)
	add(t, dir, "loom")

	out, ok := runLoomTool(t, dir, "generate", "-dry-run", "./internal/di")
	if !ok {
		t.Skipf("cannot run the pinned Loom generator: %s", out)
	}
	if !strings.Contains(out, "unchanged") {
		t.Fatalf("regeneration drifted from the committed initializer:\n%s", out)
	}
}

// TestAddLoomIsNotAutoInstalled guards that no other capability pulls Loom in.
func TestAddLoomIsNotAutoInstalled(t *testing.T) {
	catalog := newCatalog()
	for _, name := range []string{"base", "http", "web", "api", "db"} {
		capability, err := catalog.Get(name)
		if err != nil {
			t.Fatalf("Get %s: %v", name, err)
		}
		for _, required := range capability.Requires {
			if required == "loom" {
				t.Errorf("capability %s requires loom", name)
			}
		}
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

// TestAddLoomLinksAPIServiceToRepository proves the graph wires the JSON API
// service to the PostgreSQL repository through the generated adapter, in either
// install order. api needs the unpublished go-validate Spec API, so it uses the
// same test-only local replace as the other api integration tests and skips
// visibly when that working copy is absent. The generated di_test.go exercises
// the adapter with an injected in-memory repository, so no PostgreSQL is needed.
func TestAddLoomLinksAPIServiceToRepository(t *testing.T) {
	goValidate := goValidateDir(t)
	if goValidate == "" {
		t.Skip("generated API projects require the unpublished go-validate Spec API; sibling go-validate not found (set WELD_GO_VALIDATE_DIR). Publishing and pinning go-validate is required before release.")
	}
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
			for _, want := range []string{"NewAPIService", "repositoryService", "data.Repository"} {
				if !strings.Contains(di, want) {
					t.Errorf("di.go is missing %q:\n%s", want, di)
				}
			}
			gen := readFile(t, filepath.Join(dir, "internal/di/loom_gen.go"))
			for _, want := range []string{"NewAPIService", "NewRepository"} {
				if !strings.Contains(gen, want) {
					t.Errorf("loom_gen.go does not wire %s into the graph:\n%s", want, gen)
				}
			}
			gofmtCheck(t, dir)
			if !buildAndTestGeneratedProject(t, dir) {
				t.Skip("cannot resolve the generated project's dependencies (network/module cache unavailable)")
			}
		})
	}
}
