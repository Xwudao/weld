package scaffold

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Xwudao/weld/internal/project"
)

// TestAddUserUsesDataAndAuth proves the account capability follows the project's
// data/auth split: the schema and generated queries live with the database code,
// the repository is internal/data/user_repo.go, the service is internal/auth, and
// no top-level internal/user package appears.
func TestAddUserUsesDataAndAuth(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	dir := create(t, root)

	result := add(t, dir, "user")
	if got, want := strings.Join(result.Installed, ","), "config,loom,db,http,jwt,user"; got != want {
		t.Fatalf("installed = %q, want %q", got, want)
	}
	for _, path := range []string{
		"db/query/users.sql",
		"internal/data/user_repo.go",
		"internal/data/user_repo_test.go",
		"internal/auth/user.go",
		"internal/auth/user_test.go",
		"internal/auth/README.md",
		"internal/di/user_provider.go",
	} {
		if _, err := os.Stat(filepath.Join(dir, path)); err != nil {
			t.Errorf("expected %s: %v", path, err)
		}
	}
	// Generated sqlc output is produced by the command, not shipped as a
	// capability payload.
	for _, path := range []string{
		"internal/data/sqlc/users.sql.go",
		"internal/data/usersqlc",
	} {
		if _, err := os.Stat(filepath.Join(dir, path)); !os.IsNotExist(err) {
			t.Errorf("add user wrote generated sqlc output %s", path)
		}
	}
	notes := strings.Join(result.Notes, "\n")
	if !strings.Contains(notes, "make sqlc") {
		t.Errorf("add user does not explain the sqlc step: %q", notes)
	}
	for _, path := range []string{
		"internal/user",
		"internal/data/user",
		"sqlc.user.yaml",
	} {
		if _, err := os.Stat(filepath.Join(dir, path)); !os.IsNotExist(err) {
			t.Errorf("add user created %s", path)
		}
	}

	migration := readFile(t, migrationFile(t, dir, "create_users"))
	for _, want := range []string{"bigint GENERATED ALWAYS AS IDENTITY", "username", "password_hash", "created_at", "updated_at", "roles"} {
		if !strings.Contains(migration, want) {
			t.Errorf("migration is missing %q:\n%s", want, migration)
		}
	}

	sqlcConfig := readFile(t, filepath.Join(dir, "sqlc.yaml"))
	for _, want := range []string{
		`queries: "db/query"`,
		`out: "internal/data/sqlc"`,
	} {
		if !strings.Contains(sqlcConfig, want) {
			t.Errorf("sqlc.yaml is missing %q:\n%s", want, sqlcConfig)
		}
	}
	if strings.Contains(sqlcConfig, "usersqlc") || strings.Contains(sqlcConfig, "weld:user") {
		t.Errorf("add user added a second sqlc target:\n%s", sqlcConfig)
	}

	goMod := readFile(t, filepath.Join(dir, "go.mod"))
	for _, want := range []string{"golang.org/x/crypto", "weld:user:installed"} {
		if !strings.Contains(goMod, want) {
			t.Errorf("go.mod is missing %q:\n%s", want, goMod)
		}
	}

	provider := readFile(t, filepath.Join(dir, "internal", "di", "user_provider.go"))
	for _, want := range []string{
		"func NewUserService(pool *pgxpool.Pool, tokens *jwtauth.Manager) (*auth.UserService, error)",
		"data.NewUserRepository(pool)",
	} {
		if !strings.Contains(provider, want) {
			t.Errorf("user_provider.go is missing %q:\n%s", want, provider)
		}
	}
	di := readFile(t, filepath.Join(dir, "internal", "di", "di.go"))
	if !strings.Contains(di, "loom.Provide(NewUserService)") {
		t.Errorf("di.go does not declare the user service binding:\n%s", di)
	}
	// The service is an available binding, not part of the default composition:
	// an ordinary serve must not construct it or require a database/JWT secret.
	gen := readFile(t, filepath.Join(dir, "internal", "di", "loom_gen.go"))
	if strings.Contains(gen, "NewUserService") {
		t.Errorf("loom_gen.go constructs the unused user service:\n%s", gen)
	}

	if drift := mustLoad(t, dir).Drift(dir); len(drift) != 0 {
		t.Fatalf("drift after add user: %+v", drift)
	}

	// The account repository refers to sqlc.User before it exists. The documented
	// command is what creates it; the generated project is checked after that.
	if !runDBTool(t, dir, "go", "tool", "sqlc", "generate", "-f", "../../sqlc.yaml") {
		t.Skip("cannot run the pinned sqlc tool (network/module cache unavailable)")
	}
	for _, path := range []string{
		"internal/data/sqlc/users.sql.go",
	} {
		if _, err := os.Stat(filepath.Join(dir, path)); err != nil {
			t.Fatalf("make sqlc did not generate %s: %v", path, err)
		}
	}
	if models := readFile(t, filepath.Join(dir, "internal", "data", "sqlc", "models.go")); !strings.Contains(models, "type User struct") || !strings.Contains(models, "ID           int64") {
		t.Errorf("sqlc did not add an int64 User identity to models.go:\n%s", models)
	}
	usersSQL := readFile(t, filepath.Join(dir, "internal", "data", "sqlc", "users.sql.go"))
	if strings.Contains(usersSQL, "type CreateUserParams struct {\n\tID") {
		t.Errorf("CreateUser still sends a client-generated id:\n%s", usersSQL)
	}
	gofmtCheck(t, dir)
	if !buildAndTestGeneratedProject(t, dir) {
		t.Skip("cannot resolve the generated project's dependencies (network/module cache unavailable)")
	}
}

// TestAddUserSQLCGenerationIsReproducible runs the documented generator twice
// and requires the second run to leave internal/data/sqlc byte-identical.
func TestAddUserSQLCGenerationIsReproducible(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	dir := create(t, root)
	add(t, dir, "user")

	generated := filepath.Join(dir, "internal", "data", "sqlc")
	if !runDBTool(t, dir, "go", "tool", "sqlc", "generate", "-f", "../../sqlc.yaml") {
		t.Skip("cannot run the pinned sqlc tool (network/module cache unavailable)")
	}
	before := snapshotDir(t, generated)
	if !runDBTool(t, dir, "go", "tool", "sqlc", "generate", "-f", "../../sqlc.yaml") {
		t.Skip("cannot run the pinned sqlc tool (network/module cache unavailable)")
	}
	after := snapshotDir(t, generated)
	if len(before) != len(after) {
		t.Fatalf("regeneration changed the sqlc file set: before %v after %v", before, after)
	}
	for path, content := range before {
		if after[path] != content {
			t.Errorf("regeneration changed %s", path)
		}
	}
}

// TestAddUserIsIdempotent proves a repeat add writes nothing and keeps the
// local account files untouched.
func TestAddUserIsIdempotent(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	dir := create(t, root)
	add(t, dir, "user")

	before := map[string]string{}
	for _, path := range []string{
		"sqlc.yaml",
		"internal/data/user_repo.go",
		"internal/auth/user.go",
		"internal/di/user_provider.go",
		"go.mod",
		project.ManifestName,
	} {
		before[path] = readFile(t, filepath.Join(dir, path))
	}
	result, err := Add(Request{Dir: dir, Catalog: newCatalog(), WithDependencies: true}, "user")
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
