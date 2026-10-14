package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDockerCommands(t *testing.T) {
	var out bytes.Buffer
	run := func(args ...string) error { out.Reset(); return Run(args, nil, &out, &out) }
	if err := run("docker", "init", "--help"); err != nil || !strings.Contains(out.String(), "config.yml") {
		t.Fatalf("%v %s", err, out.String())
	}
	parent := t.TempDir()
	if err := run("new", "demo", "--dir", parent); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(parent, "demo")
	if err := run("docker", "init", "--dir", dir, "--dry-run"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "Dockerfile")); !os.IsNotExist(err) {
		t.Fatal("dry run wrote")
	}
	if err := run("docker", "init", "--dir", dir); err != nil {
		t.Fatal(err)
	}
	if err := run("docker", "init", "--dir", dir); err != nil || !strings.Contains(out.String(), "kept") {
		t.Fatalf("%v %s", err, out.String())
	}
	if err := os.WriteFile(filepath.Join(dir, "weld.yml"), []byte("version: 1\nbuild:\n  docker:\n    image: configured:test\n    platform: linux/arm64\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := run("build", "--docker", "--dry-run", "--image", "override:test", "--dir", dir); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "override:test") || !strings.Contains(out.String(), "linux/arm64") {
		t.Fatal(out.String())
	}
	if err := run("build", "--image", "invalid", "--dir", dir); err == nil {
		t.Fatal("expected Docker flag validation")
	}
}
