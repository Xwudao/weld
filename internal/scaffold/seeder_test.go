package scaffold

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// addSeeder plans and applies `weld add seed <name>`.
func addSeeder(t *testing.T, dir, name string) *Result {
	t.Helper()
	result, err := AddSeeder(Request{Dir: dir, Catalog: newCatalog()}, name)
	if err != nil {
		t.Fatalf("AddSeeder %s: %v", name, err)
	}
	if err := result.Apply(); err != nil {
		t.Fatalf("Apply add seeder %s: %v", name, err)
	}
	return result
}

func TestValidateSeederName(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"items", "users", "a1", "catalog2"} {
		if err := ValidateSeederName(name); err != nil {
			t.Errorf("ValidateSeederName(%q) = %v, want nil", name, err)
		}
	}
	for _, name := range []string{"", "Users", "user-1", "1user", "_user", "user_1", "func", "range"} {
		if err := ValidateSeederName(name); err == nil {
			t.Errorf("ValidateSeederName(%q) = nil, want an error", name)
		}
	}
}

func TestAddSeederWritesFileAndRegistersIt(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	dir := create(t, root)
	add(t, dir, "seed")

	result, err := AddSeeder(Request{Dir: dir, Catalog: newCatalog()}, "users")
	if err != nil {
		t.Fatalf("AddSeeder: %v", err)
	}
	if err := result.Apply(); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	seederPath := filepath.Join(dir, "internal/data/seeder/seed_users.go")
	body := readFile(t, seederPath)
	if !strings.Contains(body, "func seedUsers(ctx context.Context, deps *Deps) error") {
		t.Fatalf("seed_users.go has no seedUsers function:\n%s", body)
	}
	register := readFile(t, filepath.Join(dir, "internal/data/seeder/register.go"))
	if !strings.Contains(register, `r.Register("users", seedUsers)`) {
		t.Fatalf("register.go does not register users:\n%s", register)
	}
	if !strings.Contains(register, "weld:seed:users:installed") {
		t.Fatalf("register.go has no idempotency sentinel:\n%s", register)
	}
	if !mustLoad(t, dir).Owns("internal/data/seeder/seed_users.go") {
		t.Fatal("seed_users.go is not recorded in the manifest")
	}
}

func TestAddSeederIsIdempotent(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	dir := create(t, root)
	add(t, dir, "seed")
	addSeeder(t, dir, "users")

	registerPath := filepath.Join(dir, "internal/data/seeder/register.go")
	registerBefore := readFile(t, registerPath)
	seederBefore := readFile(t, filepath.Join(dir, "internal/data/seeder/seed_users.go"))

	result, err := AddSeeder(Request{Dir: dir, Catalog: newCatalog()}, "users")
	if err != nil {
		t.Fatalf("second AddSeeder: %v", err)
	}
	if len(result.Operations) != 0 {
		t.Fatalf("second AddSeeder planned %d operations, want 0", len(result.Operations))
	}
	if len(result.Notes) == 0 {
		t.Fatal("second AddSeeder produced no explanation")
	}
	if err := result.Apply(); err != nil {
		t.Fatalf("second Apply: %v", err)
	}
	if readFile(t, registerPath) != registerBefore {
		t.Fatal("register.go changed on repeat add")
	}
	if readFile(t, filepath.Join(dir, "internal/data/seeder/seed_users.go")) != seederBefore {
		t.Fatal("seed_users.go changed on repeat add")
	}
}

func TestAddSeederRequiresSeed(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	dir := create(t, root)

	_, err := AddSeeder(Request{Dir: dir, Catalog: newCatalog()}, "users")
	missing, ok := err.(*MissingRequirementsError)
	if !ok {
		t.Fatalf("AddSeeder error = %v, want *MissingRequirementsError", err)
	}
	if got, want := strings.Join(missing.Missing, ","), "config,loom,db,seed"; got != want {
		t.Fatalf("missing = %q, want %q", got, want)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "internal/data/seeder/seed_users.go")); !os.IsNotExist(statErr) {
		t.Fatal("a failed AddSeeder wrote the seeder file")
	}
}

func TestAddSeederWithDependenciesInstallsChain(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	dir := create(t, root)

	result, err := AddSeeder(Request{Dir: dir, Catalog: newCatalog(), WithDependencies: true}, "users")
	if err != nil {
		t.Fatalf("AddSeeder --with-dependencies: %v", err)
	}
	if got, want := strings.Join(result.Installed, ","), "config,loom,db,seed"; got != want {
		t.Fatalf("installed = %q, want %q", got, want)
	}
	if err := result.Apply(); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	for _, path := range []string{
		"internal/data/data.go",
		"internal/data/seeder/register.go",
		"internal/data/seeder/seed_users.go",
		"internal/di/di.go",
	} {
		if _, err := os.Stat(filepath.Join(dir, path)); err != nil {
			t.Errorf("expected %s: %v", path, err)
		}
	}
}

func TestAddSeederGeneratedProjectBuildsAndTests(t *testing.T) {
	t.Parallel()
	loomToolAvailable(t)
	root := t.TempDir()
	dir := create(t, root)
	add(t, dir, "seed")
	addSeeder(t, dir, "users")
	if !buildAndTestGeneratedProject(t, dir) {
		t.Skip("cannot resolve the generated project's dependencies (network/module cache unavailable)")
	}
}
