package seed

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// projectWith writes a minimal weld.json that records the given capabilities.
func projectWith(t *testing.T, capabilities ...string) string {
	t.Helper()
	dir := t.TempDir()
	parts := make([]string, len(capabilities))
	for i, capability := range capabilities {
		parts[i] = fmt.Sprintf(`{"name":%q,"version":"0.1.0"}`, capability)
	}
	body := fmt.Sprintf(`{"kind":"weld-project","manifestVersion":1,"name":"demo","module":"example.com/demo","createdWith":"0.2.1","base":{"name":"base","version":"0.3.0"},"capabilities":[%s],"files":[]}`, strings.Join(parts, ","))
	if err := os.WriteFile(filepath.Join(dir, "weld.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestRunInvokesGoRunWithSeedTag(t *testing.T) {
	dir := projectWith(t, "db", "seed")
	var gotName, gotDir string
	var gotArgs []string
	err := Run(Request{
		Dir:    dir,
		Name:   "items",
		Stdout: io.Discard,
		Stderr: io.Discard,
		Runner: func(name, runDir string, args ...string) error {
			gotName, gotDir, gotArgs = name, runDir, args
			return nil
		},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if gotName != "go" {
		t.Errorf("command = %q, want go", gotName)
	}
	if gotDir != dir {
		t.Errorf("dir = %q, want %q", gotDir, dir)
	}
	want := "run,-tags,seed,.,seed,items"
	if got := strings.Join(gotArgs, ","); got != want {
		t.Errorf("args = %q, want %q", got, want)
	}
}

func TestRunWithoutNameRunsEverySeeder(t *testing.T) {
	dir := projectWith(t, "seed")
	var gotArgs []string
	err := Run(Request{
		Dir: dir,
		Runner: func(name, runDir string, args ...string) error {
			gotArgs = args
			return nil
		},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := strings.Join(gotArgs, ","); got != "run,-tags,seed,.,seed" {
		t.Errorf("args = %q, want no trailing seeder name", got)
	}
}

func TestRunHonorsCustomTag(t *testing.T) {
	dir := projectWith(t, "seed")
	var gotArgs []string
	err := Run(Request{
		Dir: dir,
		Tag: "devseed",
		Runner: func(name, runDir string, args ...string) error {
			gotArgs = args
			return nil
		},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(strings.Join(gotArgs, ","), "-tags,devseed") {
		t.Errorf("args = %q, want the custom tag", gotArgs)
	}
}

func TestRunReadsTagFromWeldYml(t *testing.T) {
	dir := projectWith(t, "seed")
	if err := os.WriteFile(filepath.Join(dir, "weld.yml"), []byte("version: 1\nseed:\n  tag: devseed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var gotArgs []string
	err := Run(Request{
		Dir: dir,
		Runner: func(name, runDir string, args ...string) error {
			gotArgs = args
			return nil
		},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(strings.Join(gotArgs, ","), "-tags,devseed") {
		t.Errorf("args = %q, want the weld.yml seed.tag", gotArgs)
	}
}

func TestRunRequiresTheSeedCapability(t *testing.T) {
	dir := projectWith(t, "db")
	err := Run(Request{
		Dir: dir,
		Runner: func(name, runDir string, args ...string) error {
			t.Fatal("the runner ran without the seed capability")
			return nil
		},
	})
	if err == nil || !strings.Contains(err.Error(), "not installed") {
		t.Fatalf("Run error = %v, want a not-installed error", err)
	}
}

func TestRunRequiresAProject(t *testing.T) {
	if err := Run(Request{Dir: t.TempDir()}); err == nil {
		t.Fatal("expected an error for a directory without weld.json")
	}
}
