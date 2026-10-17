package scaffold

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Xwudao/weld/internal/project"
)

// TestAddJWTInstallsHTTPAndConfig proves jwt adopts the HTTP surface (which
// brings config and Loom), writes the manager and middleware, extends the typed
// configuration, and declares the manager as a pruned Loom binding.
func TestAddJWTInstallsHTTPAndConfig(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	dir := create(t, root)

	result := add(t, dir, "jwt")
	if got, want := strings.Join(result.Installed, ","), "config,loom,http,jwt"; got != want {
		t.Fatalf("installed = %q, want %q", got, want)
	}
	for _, path := range []string{
		"internal/config/jwt.go",
		"internal/config/jwt_test.go",
		"internal/jwtauth/jwtauth.go",
		"internal/jwtauth/jwtauth_test.go",
		"internal/jwtauth/README.md",
		"internal/httpserver/jwt.go",
		"internal/di/jwt_provider.go",
		"internal/di/di.go",
		"config.yml",
		"config.example.yml",
	} {
		if _, err := os.Stat(filepath.Join(dir, path)); err != nil {
			t.Errorf("expected %s: %v", path, err)
		}
	}
	// Installing jwt must not invent an issuing endpoint.
	for _, path := range []string{
		"internal/jwtauth/login.go",
		"internal/httpserver/login.go",
		"internal/modules/auth",
	} {
		if _, err := os.Stat(filepath.Join(dir, path)); !os.IsNotExist(err) {
			t.Errorf("add jwt created %s", path)
		}
	}

	configGo := readFile(t, filepath.Join(dir, "internal/config/config.go"))
	for _, want := range []string{
		"weld:jwt:installed",
		"JWT JWT `yaml:\"jwt\"`",
		"cfg.applyJWTEnv(l.lookupEnv)",
		"cfg.applyJWTDefaults()",
	} {
		if !strings.Contains(configGo, want) {
			t.Errorf("config.go is missing %q:\n%s", want, configGo)
		}
	}
	cfg := readFile(t, filepath.Join(dir, "config.yml"))
	for _, want := range []string{"jwt:", "weld:jwt:installed", `secret: ""`, "ttl_seconds:"} {
		if !strings.Contains(cfg, want) {
			t.Errorf("config.yml is missing %q:\n%s", want, cfg)
		}
	}

	goMod := readFile(t, filepath.Join(dir, "go.mod"))
	for _, want := range []string{"github.com/golang-jwt/jwt/v5", "weld:jwt:installed"} {
		if !strings.Contains(goMod, want) {
			t.Errorf("go.mod is missing %q:\n%s", want, goMod)
		}
	}

	provider := readFile(t, filepath.Join(dir, "internal/di/jwt_provider.go"))
	for _, want := range []string{"func NewJWTAuth(cfg *config.Config) (*jwtauth.Manager, error)", "NewHTTPPolicies"} {
		if !strings.Contains(provider, want) {
			t.Errorf("jwt_provider.go is missing %q:\n%s", want, provider)
		}
	}
	di := readFile(t, filepath.Join(dir, "internal/di/di.go"))
	if !strings.Contains(di, "loom.Provide(NewJWTAuth)") {
		t.Errorf("di.go does not declare the JWT manager binding:\n%s", di)
	}
	// The manager is an available binding, not part of the default composition:
	// an ordinary serve must not construct it or require a JWT secret.
	gen := readFile(t, filepath.Join(dir, "internal/di/loom_gen.go"))
	if strings.Contains(gen, "NewJWTAuth") {
		t.Errorf("loom_gen.go constructs the unused JWT manager:\n%s", gen)
	}

	if drift := mustLoad(t, dir).Drift(dir); len(drift) != 0 {
		t.Fatalf("drift after add jwt: %+v", drift)
	}
	gofmtCheck(t, dir)
	if !buildAndTestGeneratedProject(t, dir) {
		t.Skip("cannot resolve the generated project's dependencies (network/module cache unavailable)")
	}
}

// TestAddJWTIsIdempotent proves a repeat add writes nothing and preserves the
// user's local config, including a secret placeholder or value.
func TestAddJWTIsIdempotent(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	dir := create(t, root)
	add(t, dir, "jwt")

	before := map[string]string{}
	for _, path := range []string{
		"internal/config/config.go",
		"internal/jwtauth/jwtauth.go",
		"internal/di/jwt_provider.go",
		"config.yml",
		"config.example.yml",
		"go.mod",
		project.ManifestName,
	} {
		before[path] = readFile(t, filepath.Join(dir, path))
	}
	result, err := Add(Request{Dir: dir, Catalog: newCatalog(), WithDependencies: true}, "jwt")
	if err != nil {
		t.Fatalf("second Add: %v", err)
	}
	if len(result.Operations) != 0 || len(result.Installed) != 0 {
		t.Fatalf("second Add planned %d operations and installed %v, want none", len(result.Operations), result.Installed)
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
