package build

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/Xwudao/weld/internal/projectcfg"
)

type call struct {
	name string
	dir  string
	env  []string
	args []string
}

func recorder(calls *[]call) Runner {
	return func(name, dir string, env []string, args ...string) error {
		*calls = append(*calls, call{name: name, dir: dir, env: env, args: args})
		return nil
	}
}

func TestBuildHostTarget(t *testing.T) {
	dir := t.TempDir()
	var calls []call
	result, err := Run(Request{Dir: dir, Name: "demo", Runner: recorder(&calls)})
	if err != nil {
		t.Fatal(err)
	}
	if result.WebBuilt {
		t.Error("WebBuilt = true without the web capability")
	}
	if got, want := result.Binaries, []string{"bin/demo"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Binaries = %v, want %v", got, want)
	}
	if len(calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(calls))
	}
	if calls[0].name != "go" || calls[0].dir != dir {
		t.Errorf("call = %+v", calls[0])
	}
	want := []string{"build", "-trimpath", "-o", filepath.FromSlash("bin/demo"), "."}
	if !reflect.DeepEqual(calls[0].args, want) {
		t.Errorf("args = %v, want %v", calls[0].args, want)
	}
}

func TestBuildCrossTargetsAndSettings(t *testing.T) {
	dir := t.TempDir()
	cfg := &projectcfg.Config{Build: projectcfg.BuildConfig{
		Tags:    []string{"prod"},
		Cgo:     boolPtr(false),
		Targets: []projectcfg.Target{{OS: "linux", Arch: "amd64"}, {OS: "windows", Arch: "arm64"}},
	}}
	var calls []call
	result, err := Run(Request{Dir: dir, Name: "demo", Config: cfg, Runner: recorder(&calls)})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"bin/demo-linux-amd64", "bin/demo-windows-arm64.exe"}
	if !reflect.DeepEqual(result.Binaries, want) {
		t.Errorf("Binaries = %v, want %v", result.Binaries, want)
	}
	if len(calls) != 2 {
		t.Fatalf("calls = %d, want 2", len(calls))
	}
	if !contains(calls[0].env, "GOOS=linux") || !contains(calls[0].env, "GOARCH=amd64") || !contains(calls[0].env, "CGO_ENABLED=0") {
		t.Errorf("linux env = %v", calls[0].env)
	}
	if !contains(calls[1].args, "-tags", "prod") {
		t.Errorf("windows args = %v, want -tags prod", calls[1].args)
	}
}

func TestBuildRunsWebFirst(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "web"), 0o755); err != nil {
		t.Fatal(err)
	}
	var calls []call
	result, err := Run(Request{Dir: dir, Name: "demo", WebInstalled: true, Runner: recorder(&calls)})
	if err != nil {
		t.Fatal(err)
	}
	if !result.WebBuilt {
		t.Error("WebBuilt = false with the web capability installed")
	}
	if len(calls) != 3 {
		t.Fatalf("calls = %d, want 3 (install, build, go build)", len(calls))
	}
	if calls[0].name != "pnpm" || calls[0].dir != filepath.Join(dir, "web") {
		t.Errorf("web install call = %+v", calls[0])
	}
	if !reflect.DeepEqual(calls[1].args, []string{"run", "build"}) {
		t.Errorf("web build args = %v", calls[1].args)
	}
}

func TestBuildWebMissingDirectory(t *testing.T) {
	_, err := Run(Request{Dir: t.TempDir(), WebInstalled: true, Runner: func(string, string, []string, ...string) error { return nil }})
	if err == nil {
		t.Fatal("expected an error for a missing frontend directory")
	}
}

func TestBuildRejectsIncompleteTarget(t *testing.T) {
	cfg := &projectcfg.Config{Build: projectcfg.BuildConfig{Targets: []projectcfg.Target{{OS: "linux"}}}}
	ran := false
	_, err := Run(Request{Dir: t.TempDir(), Config: cfg, Runner: func(string, string, []string, ...string) error {
		ran = true
		return nil
	}})
	if err == nil {
		t.Fatal("expected an error for a target without an arch")
	}
	if ran {
		t.Error("a command ran before the target was rejected")
	}
}

func TestBuildRunnerErrorPropagates(t *testing.T) {
	want := errors.New("boom")
	_, err := Run(Request{Dir: t.TempDir(), Runner: func(string, string, []string, ...string) error { return want }})
	if !errors.Is(err, want) {
		t.Fatalf("err = %v, want %v", err, want)
	}
}

func boolPtr(v bool) *bool { return &v }

func contains(haystack []string, needles ...string) bool {
	for i := 0; i+len(needles) <= len(haystack); i++ {
		match := true
		for j, needle := range needles {
			if haystack[i+j] != needle {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}
