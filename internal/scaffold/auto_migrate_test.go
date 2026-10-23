package scaffold

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestAddPreservesAutoMigrate covers regeneration after startup migrations have
// already been enabled. Checking only the provider declaration misses Loom
// pruning it when the application root stops consuming the runner.
func TestAddPreservesAutoMigrate(t *testing.T) {
	dir := create(t, t.TempDir())
	add(t, dir, "db", "http")
	configPath := filepath.Join(dir, "config.yml")
	cfg := strings.Replace(readFile(t, configPath), "auto_migrate: false", "auto_migrate: true", 1)
	if !strings.Contains(cfg, "auto_migrate: true") {
		t.Fatal("database.auto_migrate setting not found")
	}
	if err := os.WriteFile(configPath, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}

	// Exercise the actual initializer without a database: with migration enabled
	// an empty database section must fail validation. With it disabled the same
	// graph must initialize successfully, without connecting or binding a socket.
	const probe = `package di

import (
    "context"
    "os"
    "path/filepath"
    "strings"
    "testing"
)

func TestAutoMigrateSurvivesAdd(t *testing.T) {
    for _, key := range []string{"DATABASE_URL", "DB_HOST", "DB_USER", "DB_NAME", "HTTP_ADDR"} {
        t.Setenv(key, "")
    }
    t.Setenv("HTTP_ADDR", "127.0.0.1:0")
    path := filepath.Join(t.TempDir(), "config.yml")
    if err := os.WriteFile(path, []byte("http:\n  addr: 127.0.0.1:0\ndatabase:\n  auto_migrate: true\n"), 0o644); err != nil {
        t.Fatal(err)
    }
    ctx := WithConfigPath(context.Background(), path)
    t.Setenv("DB_AUTO_MIGRATE", "true")
    app, lc, err := InitApp(ctx)
    if err == nil {
        if lc != nil {
            _ = lc.Stop(ctx)
        }
        t.Fatalf("enabled auto migration was not reached: app=%v", app)
    }
    if !strings.Contains(err.Error(), "config: database.user is required") {
        t.Fatalf("expected migration database validation, got %v", err)
    }
    t.Setenv("DB_AUTO_MIGRATE", "false")
    _, lc, err = InitApp(ctx)
    if err != nil {
        t.Fatalf("disabled auto migration: %v", err)
    }
    if err := lc.Stop(ctx); err != nil {
        t.Fatal(err)
    }
}
`
	if err := os.WriteFile(filepath.Join(dir, "internal/di/auto_migrate_regression_test.go"), []byte(probe), 0o644); err != nil {
		t.Fatal(err)
	}
	check := func(t *testing.T) {
		t.Helper()
		if got := readFile(t, configPath); !strings.Contains(got, "auto_migrate: true") {
			t.Fatal("add lost the enabled auto_migrate configuration")
		}
		for path, want := range map[string]string{
			"internal/di/di.go":       "loom.Provide(NewAutoMigrate)",
			"internal/di/loom_gen.go": "NewAutoMigrate(ctx,",
		} {
			if !strings.Contains(readFile(t, filepath.Join(dir, path)), want) {
				t.Fatalf("%s lost %s", path, want)
			}
		}
		runGo(t, dir, "test", "./internal/di", "-run", "^TestAutoMigrateSurvivesAdd$", "-count=1")
	}
	t.Run("before", check)
	add(t, dir, "user")
	if !runDBTool(t, dir, "go", "tool", "sqlc", "generate", "-f", "../../sqlc.yaml") {
		t.Skip("cannot run the pinned sqlc tool (network/module cache unavailable)")
	}
	t.Run("after_user", check)
	addModule(t, dir, "widget")
	t.Run("after_module", check)
}
