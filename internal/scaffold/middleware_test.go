package scaffold

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Xwudao/weld/internal/project"
)

// addMiddleware plans and applies `weld add middleware <name>`.
func addMiddleware(t *testing.T, dir, name string) *Result {
	t.Helper()
	result, err := AddMiddleware(Request{Dir: dir, Catalog: newCatalog()}, name)
	if err != nil {
		t.Fatalf("AddMiddleware %s: %v", name, err)
	}
	if err := result.Apply(); err != nil {
		t.Fatalf("Apply add middleware %s: %v", name, err)
	}
	return result
}

func TestValidateMiddlewareName(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"audit", "tenant", "a1"} {
		if err := ValidateMiddlewareName(name); err != nil {
			t.Errorf("ValidateMiddlewareName(%q) = %v, want nil", name, err)
		}
	}
	for _, name := range []string{"", "Audit", "audit-log", "audit_log", "1audit", "func", "type"} {
		if err := ValidateMiddlewareName(name); err == nil {
			t.Errorf("ValidateMiddlewareName(%q) = nil, want an error", name)
		}
	}
}

func TestAddMiddlewareWritesSeamsAndNotesWiring(t *testing.T) {
	t.Parallel()
	dir := create(t, t.TempDir())
	add(t, dir, "http")
	chainBefore := readFile(t, filepath.Join(dir, "internal/httpserver/middleware.go"))

	result := addMiddleware(t, dir, "audit")

	body := readFile(t, filepath.Join(dir, "internal/middleware/audit.go"))
	if !strings.Contains(body, "func Audit() httpx.Middleware") {
		t.Fatalf("audit.go has no Audit constructor:\n%s", body)
	}
	if !strings.Contains(body, `"`+testModule+`/internal/httpx"`) {
		t.Fatalf("audit.go does not import the project's httpx:\n%s", body)
	}
	if !strings.Contains(readFile(t, filepath.Join(dir, "internal/middleware/audit_test.go")), "func TestAuditCallsNext(t *testing.T)") {
		t.Fatal("audit_test.go has no test")
	}
	manifest := mustLoad(t, dir)
	for _, path := range []string{"internal/middleware/audit.go", "internal/middleware/audit_test.go"} {
		file, ok := manifest.FileEntry(path)
		if !ok {
			t.Fatalf("%s is not recorded in the manifest", path)
		}
		if file.Capability != "middleware:audit" || file.Role != project.RoleSeam {
			t.Fatalf("%s recorded as %s/%s, want middleware:audit/seam", path, file.Capability, file.Role)
		}
	}
	// The project-owned chain is never edited; the plan says what to add instead.
	if readFile(t, filepath.Join(dir, "internal/httpserver/middleware.go")) != chainBefore {
		t.Fatal("add middleware rewrote internal/httpserver/middleware.go")
	}
	if notes := strings.Join(result.Notes, "\n"); !strings.Contains(notes, "middleware.Audit()") || !strings.Contains(notes, testModule+"/internal/middleware") {
		t.Fatalf("notes do not explain the wiring:\n%s", notes)
	}
}

func TestAddMiddlewareIsIdempotent(t *testing.T) {
	t.Parallel()
	dir := create(t, t.TempDir())
	add(t, dir, "http")
	addMiddleware(t, dir, "audit")
	path := filepath.Join(dir, "internal/middleware/audit.go")
	edited := readFile(t, path) + "\n// edited by the project\n"
	if err := os.WriteFile(path, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := AddMiddleware(Request{Dir: dir, Catalog: newCatalog()}, "audit")
	if err != nil {
		t.Fatalf("second AddMiddleware: %v", err)
	}
	if len(result.Operations) != 0 {
		t.Fatalf("second AddMiddleware planned %d operations, want 0", len(result.Operations))
	}
	if !strings.Contains(strings.Join(result.Notes, "\n"), "already installed") {
		t.Fatalf("second AddMiddleware notes = %q", result.Notes)
	}
	if readFile(t, path) != edited {
		t.Fatal("a repeat add changed the project's middleware")
	}
}

func TestAddMiddlewareRefusesUnmanagedFile(t *testing.T) {
	t.Parallel()
	dir := create(t, t.TempDir())
	add(t, dir, "http")
	if err := os.MkdirAll(filepath.Join(dir, "internal/middleware"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "internal/middleware/audit.go"), []byte("package middleware\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := AddMiddleware(Request{Dir: dir, Catalog: newCatalog()}, "audit")
	if _, ok := err.(*project.ConflictError); !ok {
		t.Fatalf("AddMiddleware over an unmanaged file = %v, want *project.ConflictError", err)
	}
}

func TestAddMiddlewareRequiresHTTP(t *testing.T) {
	t.Parallel()
	dir := create(t, t.TempDir())

	_, err := AddMiddleware(Request{Dir: dir, Catalog: newCatalog()}, "audit")
	missing, ok := err.(*MissingRequirementsError)
	if !ok {
		t.Fatalf("AddMiddleware error = %v, want *MissingRequirementsError", err)
	}
	if got, want := strings.Join(missing.Missing, ","), "config,loom,http"; got != want {
		t.Fatalf("missing = %q, want %q", got, want)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "internal/middleware/audit.go")); !os.IsNotExist(statErr) {
		t.Fatal("a failed AddMiddleware wrote the middleware file")
	}
}

func TestAddMiddlewareWithDependenciesInstallsChain(t *testing.T) {
	t.Parallel()
	dir := create(t, t.TempDir())

	result, err := AddMiddleware(Request{Dir: dir, Catalog: newCatalog(), WithDependencies: true}, "audit")
	if err != nil {
		t.Fatalf("AddMiddleware --with-dependencies: %v", err)
	}
	if got, want := strings.Join(result.Installed, ","), "config,loom,http"; got != want {
		t.Fatalf("installed = %q, want %q", got, want)
	}
	if err := result.Apply(); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	for _, path := range []string{"internal/httpserver/middleware.go", "internal/middleware/audit.go"} {
		if _, err := os.Stat(filepath.Join(dir, path)); err != nil {
			t.Errorf("expected %s: %v", path, err)
		}
	}
}

// TestAddMiddlewareWiredProjectBuildsAndTests follows the plan's note — it wires
// the middleware into the chain by hand — and proves the result compiles, is
// gofmt-clean and passes its tests.
func TestAddMiddlewareWiredProjectBuildsAndTests(t *testing.T) {
	t.Parallel()
	loomToolAvailable(t)
	dir := create(t, t.TempDir())
	add(t, dir, "http")
	addMiddleware(t, dir, "audit")

	chainPath := filepath.Join(dir, "internal/httpserver/middleware.go")
	chain := readFile(t, chainPath)
	chain = strings.Replace(chain, `"`+testModule+`/internal/httpx"`, `"`+testModule+`/internal/httpx"`+"\n\t\""+testModule+`/internal/middleware"`, 1)
	chain = strings.Replace(chain, "httpx.Recover(logger),\n", "httpx.Recover(logger),\n\t\tmiddleware.Audit(),\n", 1)
	if !strings.Contains(chain, "middleware.Audit()") {
		t.Fatal("could not wire the middleware into the shipped chain")
	}
	if err := os.WriteFile(chainPath, []byte(chain), 0o644); err != nil {
		t.Fatal(err)
	}
	if !buildAndTestGeneratedProject(t, dir) {
		t.Skip("cannot resolve the generated project's dependencies (network/module cache unavailable)")
	}
}
