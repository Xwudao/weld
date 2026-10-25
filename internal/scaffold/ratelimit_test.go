package scaffold

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Xwudao/weld/internal/project"
)

// TestAddRateLimitInstallsFilesAndConfig proves ratelimit writes its package,
// configuration section and httpserver glue, extends the shared config through
// its marker regions, and leaves the project-owned middleware chain alone.
func TestAddRateLimitInstallsFilesAndConfig(t *testing.T) {
	t.Parallel()
	dir := create(t, t.TempDir())
	add(t, dir, "http")
	chainBefore := readFile(t, filepath.Join(dir, "internal/httpserver/middleware.go"))

	result := add(t, dir, "ratelimit")
	if got, want := strings.Join(result.Installed, ","), "ratelimit"; got != want {
		t.Fatalf("installed = %q, want %q", got, want)
	}
	for _, path := range []string{
		"internal/ratelimit/ratelimit.go",
		"internal/ratelimit/ratelimit_test.go",
		"internal/ratelimit/README.md",
		"internal/config/ratelimit.go",
		"internal/config/ratelimit_test.go",
		"internal/httpserver/ratelimit.go",
		"internal/httpserver/ratelimit_test.go",
	} {
		if _, err := os.Stat(filepath.Join(dir, path)); err != nil {
			t.Errorf("expected %s: %v", path, err)
		}
	}
	configGo := readFile(t, filepath.Join(dir, "internal/config/config.go"))
	for _, want := range []string{"weld:ratelimit:installed", "RateLimit RateLimit `yaml:\"rate_limit\"`", "cfg.applyRateLimitEnv(l.lookupEnv)", "cfg.applyRateLimitDefaults()"} {
		if !strings.Contains(configGo, want) {
			t.Errorf("config.go is missing %q", want)
		}
	}
	for _, path := range []string{"config.yml", "config.example.yml"} {
		if body := readFile(t, filepath.Join(dir, path)); !strings.Contains(body, "rate_limit:") || !strings.Contains(body, "trusted_proxies: []") {
			t.Errorf("%s has no rate_limit section:\n%s", path, body)
		}
	}
	if readFile(t, filepath.Join(dir, "internal/httpserver/middleware.go")) != chainBefore {
		t.Fatal("add ratelimit rewrote the project-owned middleware chain")
	}
	if !strings.Contains(strings.Join(result.Notes, "\n"), "RateLimitMiddleware") {
		t.Fatalf("notes do not explain the wiring: %q", result.Notes)
	}
	file, ok := mustLoad(t, dir).FileEntry("internal/ratelimit/ratelimit.go")
	if !ok || file.Capability != "ratelimit" || (file.Role != project.RolePayload && file.Role != "") {
		t.Fatalf("ratelimit.go manifest entry = %+v, %v", file, ok)
	}
}

func TestAddRateLimitRequiresHTTP(t *testing.T) {
	t.Parallel()
	dir := create(t, t.TempDir())
	_, err := Add(Request{Dir: dir, Catalog: newCatalog()}, "ratelimit")
	missing, ok := err.(*MissingRequirementsError)
	if !ok {
		t.Fatalf("Add ratelimit error = %v, want *MissingRequirementsError", err)
	}
	if got, want := strings.Join(missing.Missing, ","), "config,loom,http"; got != want {
		t.Fatalf("missing = %q, want %q", got, want)
	}

	result, err := Add(Request{Dir: dir, Catalog: newCatalog(), WithDependencies: true}, "ratelimit")
	if err != nil {
		t.Fatalf("Add ratelimit --with-dependencies: %v", err)
	}
	if got, want := strings.Join(result.Installed, ","), "config,loom,http,ratelimit"; got != want {
		t.Fatalf("installed = %q, want %q", got, want)
	}
}

// TestRateLimitWiredProjectBuildsAndTests installs ratelimit, follows its
// post-install note — it adds the middleware to the shipped chain — and proves
// the project compiles, is gofmt-clean and passes its tests, in both
// installation orders against cron, which patches the same config regions.
func TestRateLimitWiredProjectBuildsAndTests(t *testing.T) {
	t.Parallel()
	loomToolAvailable(t)
	for _, order := range [][]string{{"ratelimit", "cron"}, {"cron", "ratelimit"}} {
		t.Run(strings.Join(order, "+"), func(t *testing.T) {
			t.Parallel()
			dir := create(t, t.TempDir())
			for _, capability := range order {
				add(t, dir, capability)
			}
			wireRateLimit(t, dir)
			if !buildAndTestGeneratedProject(t, dir) {
				t.Skip("cannot resolve the generated project's dependencies (network/module cache unavailable)")
			}
		})
	}
}

// wireRateLimit edits the shipped middleware chain the way the ratelimit README
// describes.
func wireRateLimit(t *testing.T, dir string) {
	t.Helper()
	path := filepath.Join(dir, "internal/httpserver/middleware.go")
	chain := readFile(t, path)
	wired := strings.Replace(chain, `	return append(chain,
		httpx.RequestID(),
		httpx.AccessLog(logger),
		httpx.Recover(logger),
	)
`, `	chain = append(chain,
		httpx.RequestID(),
		httpx.AccessLog(logger),
		httpx.Recover(logger),
	)
	limit, err := RateLimitMiddleware(cfg)
	if err != nil {
		panic(err)
	}
	if limit != nil {
		chain = append(chain, limit)
	}
	return chain
`, 1)
	if wired == chain {
		t.Fatalf("could not wire the rate limit into the shipped chain:\n%s", chain)
	}
	if err := os.WriteFile(path, []byte(wired), 0o644); err != nil {
		t.Fatal(err)
	}
}
