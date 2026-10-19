package db

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Xwudao/weld/internal/project"
)

// fakeProjectAt writes a minimal db-capable project with the given config.yml
// content, so a resolver test controls the environment and the database_test
// block exactly.
func fakeProjectAt(t *testing.T, dir, config string) {
	t.Helper()
	manifest := project.NewManifest("demo", "example.com/demo", "test")
	manifest.Base = project.CapabilityRef{Name: "base", Version: "0.3.0"}
	manifest.AddCapability(project.CapabilityRef{Name: "db", Version: "0.2.0"})
	encoded, err := manifest.Encode()
	if err != nil {
		t.Fatalf("encode manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, project.ManifestName), encoded, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.yml"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
}

// appendConfig appends a database_test block to the fake project's config.yml.
func appendConfig(t *testing.T, dir, block string) {
	t.Helper()
	path := filepath.Join(dir, "config.yml")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config.yml: %v", err)
	}
	if err := os.WriteFile(path, append(raw, []byte(block)...), 0o644); err != nil {
		t.Fatalf("write config.yml: %v", err)
	}
}

// TestResolveTestDSNFromEnvironment proves PG_TEST_DSN wins over the file and
// is returned as its own source.
func TestResolveTestDSNFromEnvironment(t *testing.T) {
	dir := fakeProject(t, true)
	lookup := func(key string) (string, bool) {
		if key == EnvTestDatabaseURL {
			return "postgres://ci/app_test", true
		}
		return "", false
	}
	dsn, source, err := ResolveTestDSN(dir, "", lookup)
	if err != nil {
		t.Fatalf("ResolveTestDSN: %v", err)
	}
	if dsn != "postgres://ci/app_test" || source != EnvTestDatabaseURL {
		t.Fatalf("dsn = %q, source = %q", dsn, source)
	}
}

// TestResolveTestDSNFromConfig proves database_test.name inherits the
// application connection fields.
func TestResolveTestDSNFromConfig(t *testing.T) {
	dir := fakeProject(t, true)
	appendConfig(t, dir, "database_test:\n  name: appdb_test\n")
	dsn, source, err := ResolveTestDSN(dir, "", noEnv)
	if err != nil {
		t.Fatalf("ResolveTestDSN: %v", err)
	}
	if !strings.Contains(dsn, "@db.internal:6543/appdb_test") {
		t.Fatalf("dsn = %q", dsn)
	}
	if source != filepath.Join(dir, "config.yml") {
		t.Fatalf("source = %q", source)
	}
}

// TestResolveTestDSNExplicitConfigDSN proves database_test.dsn is used verbatim.
func TestResolveTestDSNExplicitConfigDSN(t *testing.T) {
	dir := fakeProject(t, true)
	appendConfig(t, dir, "database_test:\n  dsn: postgres://explicit/test\n")
	dsn, _, err := ResolveTestDSN(dir, "", noEnv)
	if err != nil {
		t.Fatalf("ResolveTestDSN: %v", err)
	}
	if dsn != "postgres://explicit/test" {
		t.Fatalf("dsn = %q", dsn)
	}
}

// TestResolveTestDSNNotConfigured proves an application database alone never
// opts the tests in, and that the application dsn is not reused.
func TestResolveTestDSNNotConfigured(t *testing.T) {
	dir := fakeProject(t, true)
	if _, _, err := ResolveTestDSN(dir, "", noEnv); !errors.Is(err, ErrTestDatabaseNotConfigured) {
		t.Fatalf("ResolveTestDSN = %v, want ErrTestDatabaseNotConfigured", err)
	}
}

// TestResolveTestDSNSchemaOnly proves schema_only reuses the application dsn in
// a local environment and is refused elsewhere.
func TestResolveTestDSNSchemaOnly(t *testing.T) {
	local := t.TempDir()
	fakeProjectAt(t, local, "environment: local\ndatabase:\n  host: db.internal\n  port: 6543\n  user: app\n  password: file-secret\n  name: appdb\ndatabase_test:\n  schema_only: true\n")
	dsn, _, err := ResolveTestDSN(local, "", noEnv)
	if err != nil {
		t.Fatalf("ResolveTestDSN: %v", err)
	}
	if !strings.Contains(dsn, "@db.internal:6543/appdb") {
		t.Fatalf("dsn = %q", dsn)
	}

	production := t.TempDir()
	fakeProjectAt(t, production, "environment: production\ndatabase:\n  host: db\n  port: 5432\n  user: app\n  name: appdb\ndatabase_test:\n  schema_only: true\n")
	if _, _, err := ResolveTestDSN(production, "", noEnv); err == nil {
		t.Fatal("expected schema_only to be refused in production")
	}
}

// TestResolveTestDSNSchemaOnlyHonorsAppEnv proves APP_ENV overrides the file's
// environment when deciding whether schema_only is allowed.
func TestResolveTestDSNSchemaOnlyHonorsAppEnv(t *testing.T) {
	dir := t.TempDir()
	fakeProjectAt(t, dir, "environment: production\ndatabase:\n  host: db\n  port: 5432\n  user: app\n  name: appdb\ndatabase_test:\n  schema_only: true\n")
	lookup := func(key string) (string, bool) {
		if key == "APP_ENV" {
			return "local", true
		}
		return "", false
	}
	if _, _, err := ResolveTestDSN(dir, "", lookup); err != nil {
		t.Fatalf("ResolveTestDSN with APP_ENV=local: %v", err)
	}
}

// TestResolveTestDSNInheritsAppEnv proves the inherited application fields see
// the DB_* overrides, matching the generated testdb resolver.
func TestResolveTestDSNInheritsAppEnv(t *testing.T) {
	dir := fakeProject(t, true)
	appendConfig(t, dir, "database_test:\n  name: appdb_test\n")
	lookup := func(key string) (string, bool) {
		switch key {
		case EnvDBUser:
			return "env-user", true
		case EnvDBHost:
			return "env-db", true
		case EnvDBPort:
			return "6545", true
		}
		return "", false
	}
	dsn, _, err := ResolveTestDSN(dir, "", lookup)
	if err != nil {
		t.Fatalf("ResolveTestDSN: %v", err)
	}
	if !strings.Contains(dsn, "env-user:") || !strings.Contains(dsn, "@env-db:6545/appdb_test") {
		t.Fatalf("dsn = %q", dsn)
	}
}

// TestResolveTestDSNTestEnvOverridesFile proves DB_TEST_* overrides the file.
func TestResolveTestDSNTestEnvOverridesFile(t *testing.T) {
	dir := fakeProject(t, true)
	appendConfig(t, dir, "database_test:\n  name: file_test\n")
	lookup := func(key string) (string, bool) {
		switch key {
		case EnvTestDBName:
			return "env_test", true
		case EnvTestDBHost:
			return "env-db", true
		case EnvTestDBPort:
			return "6544", true
		}
		return "", false
	}
	dsn, _, err := ResolveTestDSN(dir, "", lookup)
	if err != nil {
		t.Fatalf("ResolveTestDSN: %v", err)
	}
	if !strings.Contains(dsn, "@env-db:6544/env_test") {
		t.Fatalf("dsn = %q", dsn)
	}
}
