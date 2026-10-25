package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigCheckCommand(t *testing.T) {
	root := t.TempDir()
	if _, err := run(t, "new", "demo", "--module", "example.com/demo", "--dir", root); err != nil {
		t.Fatalf("new: %v", err)
	}
	dir := filepath.Join(root, "demo")

	// A bare CLI has no config capability; weld.yml is optional.
	out, err := run(t, "config", "check", "--dir", dir)
	if err != nil || !strings.Contains(out, "weld.yml: absent") || !strings.Contains(out, "nothing to compare") {
		t.Fatalf("config check on a bare CLI: %q %v", out, err)
	}

	// Every capability that patches config.yml leaves it matching the example.
	if _, err := run(t, "add", "http", "db", "redis", "jwt", "cron", "ratelimit", "--dir", dir); err != nil {
		t.Fatalf("add: %v", err)
	}
	if out, err := run(t, "config", "check", "--strict", "--dir", dir); err != nil || !strings.Contains(out, "matches config.example.yml") {
		t.Fatalf("config check on a fresh project: %q %v", out, err)
	}

	configPath := filepath.Join(dir, "config.yml")
	original, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	// A typo is a warning: it passes, except under --strict.
	typo := strings.Replace(string(original), "  burst: 20\n", "  burts: 20\n", 1)
	if typo == string(original) {
		t.Fatal("the generated config.yml has no rate_limit.burst to mistype")
	}
	if err := os.WriteFile(configPath, []byte(typo), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err = run(t, "config", "check", "--dir", dir)
	if err != nil || !strings.Contains(out, "rate_limit.burts") || !strings.Contains(out, "0 failure(s), 2 warning(s)") {
		t.Fatalf("config check with a typo: %q %v", out, err)
	}
	if _, err := run(t, "config", "check", "--strict", "--dir", dir); err == nil {
		t.Fatal("--strict passed with warnings")
	}

	// An undecodable value fails, and its value is never printed.
	broken := strings.Replace(string(original), "  enabled: true\n  requests_per_second", "  enabled: \"sentinel-value\"\n  requests_per_second", 1)
	if broken == string(original) {
		t.Fatal("the generated config.yml has no rate_limit.enabled to break")
	}
	if err := os.WriteFile(configPath, []byte(broken), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err = run(t, "config", "check", "--json", "--dir", dir)
	if err == nil || !strings.Contains(err.Error(), "1 failure(s)") {
		t.Fatalf("config check with a broken value error = %v", err)
	}
	if strings.Contains(out, "sentinel-value") {
		t.Fatalf("config check printed a configuration value:\n%s", out)
	}
	var report configReport
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("--json output is not JSON: %v\n%s", err, out)
	}
	if report.Failures != 1 || len(report.Findings) != 1 || report.Findings[0].Key != "rate_limit.enabled" {
		t.Fatalf("report = %+v", report)
	}

	// A weld.yml that does not load is a failure too.
	if err := os.WriteFile(configPath, original, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "weld.yml"), []byte("buidl: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, "config", "check", "--dir", dir); err == nil {
		t.Fatal("config check passed with an invalid weld.yml")
	}
}
