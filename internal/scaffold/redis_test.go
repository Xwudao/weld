package scaffold

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestUpgradeRedisAddsKeyNamespaces proves the new helper ships without
// rewriting deployed YAML that omits the optional prefix.
func TestUpgradeRedisAddsKeyNamespaces(t *testing.T) {
	t.Parallel()
	dir := create(t, t.TempDir())
	add(t, dir, "redis")

	const payload = "internal/config/redis.go"
	const helper = "internal/rediskey/rediskey.go"
	weldWrote(t, dir, "redis", payload, "package config\n\n// Older Redis config.\n")
	if err := os.Remove(filepath.Join(dir, helper)); err != nil {
		t.Fatal(err)
	}
	forget(t, dir, helper)
	downgrade(t, dir, "redis", "0.1.0")

	configs := map[string]string{}
	for _, path := range []string{"config.yml", "config.example.yml"} {
		content := readFile(t, filepath.Join(dir, path))
		withoutPrefix := strings.Replace(content, "  prefix: \"\"\n", "", 1)
		if withoutPrefix == content {
			t.Fatalf("%s has no default redis.prefix to remove", path)
		}
		edited(t, dir, path, withoutPrefix)
		configs[path] = withoutPrefix
	}

	result := upgrade(t, dir, []string{"redis"}, false)
	for path := range configs {
		advisory := findAdvisory(t, result.Advisories, path, ReasonPatch)
		if !strings.Contains(advisory.Detail, "prefix:") {
			t.Fatalf("%s patch advisory does not mention prefix: %s", path, advisory.Detail)
		}
	}
	if err := result.Apply(); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if got := readFile(t, filepath.Join(dir, payload)); !strings.Contains(got, "func (c *Config) RedisKeys()") {
		t.Fatalf("%s was not updated", payload)
	}
	if _, err := os.Stat(filepath.Join(dir, helper)); err != nil {
		t.Fatalf("missing upgraded key helper: %v", err)
	}
	for path, want := range configs {
		if got := readFile(t, filepath.Join(dir, path)); got != want {
			t.Errorf("upgrade rewrote %s", path)
		}
	}
}
