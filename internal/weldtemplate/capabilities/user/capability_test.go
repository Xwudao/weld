// Package user holds the descriptor contract test for the user capability. The
// payloads under files/ are templates for a generated project, so their own tests
// are the templates themselves; this test guards the capability's scope, the
// data/auth organization and the promise that installing it creates no account.
package user

import (
	"encoding/json"
	"io/fs"
	"strings"
	"testing"

	weldtemplate "github.com/Xwudao/weld/internal/weldtemplate"
)

type descriptor struct {
	Name     string   `json:"name"`
	Version  string   `json:"version"`
	Kind     string   `json:"kind"`
	Summary  string   `json:"summary"`
	Requires []string `json:"requires"`
	Files    []struct {
		Path      string   `json:"path"`
		Source    string   `json:"source"`
		Role      string   `json:"role"`
		Versioned bool     `json:"versioned"`
		When      []string `json:"when"`
	} `json:"files"`
	Patches []struct {
		Path   string `json:"path"`
		Marker string `json:"marker"`
		Source string `json:"source"`
	} `json:"patches"`
	PostInstall []string `json:"postInstall"`
}

func read(t *testing.T) descriptor {
	t.Helper()
	raw, err := fs.ReadFile(weldtemplate.FS(), "capabilities/user/capability.json")
	if err != nil {
		t.Fatalf("read descriptor: %v", err)
	}
	var d descriptor
	if err := json.Unmarshal(raw, &d); err != nil {
		t.Fatalf("invalid descriptor: %v", err)
	}
	return d
}

func TestUserCapabilityRequiresDBAndJWT(t *testing.T) {
	d := read(t)
	if d.Kind != "add" || d.Version == "" || d.Summary == "" {
		t.Fatalf("descriptor = %+v", d)
	}
	if got, want := strings.Join(d.Requires, ","), "db,jwt"; got != want {
		t.Fatalf("requires = %q, want %q", got, want)
	}
	lower := strings.ToLower(d.Summary)
	if strings.Contains(lower, "default administrator") && !strings.Contains(lower, "no administrator") {
		t.Fatal("summary suggests a default administrator")
	}
}

func TestUserUsesTheDataAndAuthPackages(t *testing.T) {
	d := read(t)
	paths := map[string]string{}
	for _, file := range d.Files {
		paths[file.Path] = file.Source
	}
	for _, want := range []string{
		"db/migrations/create_users.sql",
		"db/query/users.sql",
		"internal/data/user_repo.go",
		"internal/data/user_repo_test.go",
		"internal/auth/user.go",
		"internal/auth/user_test.go",
		"internal/auth/README.md",
		"internal/di/user_provider.go",
	} {
		if _, ok := paths[want]; !ok {
			t.Errorf("user does not ship %s", want)
		}
	}
	for path := range paths {
		if strings.HasPrefix(path, "internal/user/") {
			t.Errorf("user adds a top-level internal/user package: %s", path)
		}
		if strings.HasPrefix(path, "internal/data/sqlc/") || strings.Contains(path, "usersqlc") {
			t.Errorf("user ships generated sqlc output instead of letting make sqlc write it: %s", path)
		}
	}
	// The migration is versioned: weld chooses its goose version at install time so
	// it sorts after the project's own migrations.
	var versioned bool
	for _, file := range d.Files {
		if file.Path == "db/migrations/create_users.sql" {
			versioned = file.Versioned
		}
	}
	if !versioned {
		t.Error("user migration is not declared versioned")
	}
	var providerSeam bool
	for _, file := range d.Files {
		if file.Path == "internal/di/user_provider.go" {
			providerSeam = file.Role == "seam" && len(file.When) == 1 && file.When[0] == "loom"
		}
	}
	if !providerSeam {
		t.Errorf("the Loom provider seam is not a seam guarded by when: [loom]: %+v", d.Files)
	}
}

func TestUserUsesTheExistingSQLCConfigAndGenerator(t *testing.T) {
	d := read(t)
	for _, patch := range d.Patches {
		if patch.Path == "sqlc.yaml" {
			t.Errorf("user patches sqlc.yaml; the existing query directory already includes its query: %+v", patch)
		}
	}
	if len(d.PostInstall) == 0 || !strings.Contains(strings.Join(d.PostInstall, " "), "make sqlc") {
		t.Errorf("user does not tell the caller to run make sqlc: %+v", d.PostInstall)
	}
	config, err := fs.ReadFile(weldtemplate.FS(), "capabilities/db/files/sqlc.yaml")
	if err != nil {
		t.Fatalf("read db sqlc config: %v", err)
	}
	for _, want := range []string{`queries: "db/query"`, `out: "internal/data/sqlc"`} {
		if !strings.Contains(string(config), want) {
			t.Errorf("db sqlc config is missing %q:\n%s", want, config)
		}
	}
	repo, err := fs.ReadFile(weldtemplate.FS(), "capabilities/user/files/user_repo.go.tmpl")
	if err != nil {
		t.Fatalf("read repository template: %v", err)
	}
	if !strings.Contains(string(repo), `sqlc "__module__/internal/data/sqlc"`) {
		t.Error("the repository does not use the project's existing internal/data/sqlc package")
	}
}

func TestUserMigrationHasTheAccountColumnsWithoutPlaintextPasswords(t *testing.T) {
	raw, err := fs.ReadFile(weldtemplate.FS(), "capabilities/user/files/migrations/create_users.sql")
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	migration := string(raw)
	for _, want := range []string{
		"-- +goose Up",
		"-- +goose Down",
		"id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY",
		"username      text NOT NULL UNIQUE",
		"password_hash text NOT NULL",
		"roles         text[] NOT NULL DEFAULT '{}'",
		"created_at    timestamptz NOT NULL DEFAULT now()",
		"updated_at    timestamptz NOT NULL DEFAULT now()",
		"users_username_lowercase",
	} {
		if !strings.Contains(migration, want) {
			t.Errorf("migration is missing %q:\n%s", want, migration)
		}
	}
	if strings.Contains(strings.ToLower(migration), " password ") || strings.Contains(migration, "password text") {
		t.Errorf("migration stores a plaintext password column:\n%s", migration)
	}
}

func TestUserServiceFailsClosed(t *testing.T) {
	service, err := fs.ReadFile(weldtemplate.FS(), "capabilities/user/files/auth_user.go.tmpl")
	if err != nil {
		t.Fatalf("read service template: %v", err)
	}
	body := string(service)
	for _, want := range []string{
		"bcrypt.GenerateFromPassword",
		"bcrypt.CompareHashAndPassword",
		"ErrInvalidCredentials",
		"compareDummy",
		"func (s *UserService) SetRoles",
		"func (s *UserService) Login",
		"func (s *UserService) IssueToken",
		"ErrUsernameTaken",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("service is missing %q", want)
		}
	}
	for _, forbidden := range []string{"newAccountID", "crypto/rand", "defaultPassword", "adminPassword", "changeme", "password123"} {
		if strings.Contains(strings.ToLower(body), forbidden) {
			t.Errorf("service contains a default credential: %q", forbidden)
		}
	}
}

func TestUserPinsBcryptAndDeclaresTheGraphBinding(t *testing.T) {
	deps, err := fs.ReadFile(weldtemplate.FS(), "capabilities/user/files/go.mod.snippet")
	if err != nil {
		t.Fatalf("read go.mod snippet: %v", err)
	}
	if !strings.Contains(string(deps), "golang.org/x/crypto v0.55.0") {
		t.Errorf("go.mod snippet does not pin x/crypto:\n%s", deps)
	}
	graph, err := fs.ReadFile(weldtemplate.FS(), "capabilities/loom/files/di.go.tmpl")
	if err != nil {
		t.Fatalf("read loom graph: %v", err)
	}
	if !strings.Contains(string(graph), `{{- if .Caps.Has "user"}}`) ||
		!strings.Contains(string(graph), "loom.Provide(NewUserService)") {
		t.Error("the Loom graph does not declare the user service binding")
	}
	if strings.Contains(string(graph), "func NewUserService") {
		t.Error("loom defines NewUserService; the provider must live in user_provider.go")
	}
	if strings.Contains(string(graph), "*user.Service") {
		t.Error("the graph still refers to a top-level user package")
	}
}
