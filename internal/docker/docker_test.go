package docker

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Xwudao/weld/internal/projectcfg"
)

func projectDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/demo\n\ngo 1.25\n"), 0644); err != nil {
		t.Fatal(err)
	}
	return dir
}
func TestFilesCapabilities(t *testing.T) {
	for _, web := range []bool{false, true} {
		files, err := Files(projectDir(t), nil, web, web)
		if err != nil {
			t.Fatal(err)
		}
		df := files["Dockerfile"]
		for _, want := range []string{"golang:1.25-bookworm", "ARG CGO_ENABLED=0", "nonroot", `ENTRYPOINT ["/app/app"]`, "BUILD_TAGS", "BUILD_LDFLAGS"} {
			if !strings.Contains(df, want) {
				t.Errorf("missing %s", want)
			}
		}
		if strings.Contains(df, "node:22") != web || strings.Contains(df, "COPY --from=web") != web || strings.Contains(df, "EXPOSE 8080") != web {
			t.Fatalf("incorrect capability selection: %s", df)
		}
		if !strings.Contains(df, "FROM --platform=$BUILDPLATFORM golang:") {
			t.Fatalf("build stage does not use the build platform: %s", df)
		}
		if !strings.Contains(df, metaMarker) {
			t.Fatalf("missing capability marker: %s", df)
		}
		if strings.Contains(df, "# syntax=") {
			t.Fatalf("unexpected external frontend directive: %s", df)
		}
		if !strings.Contains(files[".dockerignore"], "**/config.yml") || !strings.Contains(files[".dockerignore"], ".env.*") {
			t.Fatal("missing secret exclusions")
		}
	}
}
func TestFilesRejectInvalidFrontend(t *testing.T) {
	for _, frontend := range []projectcfg.DevFrontendConfig{{Dir: "../outside"}, {Dir: "/tmp/web"}, {Pm: "sh"}} {
		cfg := &projectcfg.Config{Dev: projectcfg.DevConfig{Frontend: frontend}}
		if _, err := Files(projectDir(t), cfg, true, true); err == nil {
			t.Fatalf("accepted %+v", frontend)
		}
	}
}
func TestInitProtectsFilesAndDryRun(t *testing.T) {
	dir := projectDir(t)
	files, err := Files(dir, nil, false, false)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := Init(dir, files, true, &out); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "Dockerfile")); !os.IsNotExist(err) {
		t.Fatal("dry run wrote file")
	}
	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("custom"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := Init(dir, files, false, &out); err != nil {
		t.Fatal(err)
	}
	if err := Init(dir, files, false, &out); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "Dockerfile"))
	if string(got) != "custom" {
		t.Fatal("overwrote user file")
	}
}
func TestBuildSettingsOverridesAndErrors(t *testing.T) {
	dir := projectDir(t)
	if err := os.WriteFile(filepath.Join(dir, "Customfile"), []byte("FROM scratch"), 0644); err != nil {
		t.Fatal(err)
	}
	cfg := &projectcfg.Config{Build: projectcfg.BuildConfig{Docker: projectcfg.DockerConfig{Image: "configured:old", Dockerfile: "Customfile", Platform: "linux/arm64"}, Tags: []string{"prod"}, Ldflags: []projectcfg.Ldflag{{Package: "main", Vars: map[string]string{"Version": "${year}"}}}}}
	var args []string
	req := Request{Dir: dir, Name: "demo", Config: cfg, Overrides: projectcfg.DockerConfig{Image: "override:new"}, Runner: func(name, gotDir string, env []string, got ...string) error {
		if name != "docker" || gotDir != dir || len(env) != 0 {
			t.Fatal("incorrect runner")
		}
		args = got
		return nil
	}}
	image, err := Run(req)
	if err != nil {
		t.Fatal(err)
	}
	if image != "override:new" {
		t.Fatal(image)
	}
	want := []string{"build", "--load", "--file", "Customfile", "--tag", "override:new", "--platform", "linux/arm64", "--build-arg", "BUILD_TAGS=prod"}
	if !reflect.DeepEqual(args[:len(want)], want) || !strings.Contains(strings.Join(args, " "), "-X 'main.Version=") {
		t.Fatal(args)
	}
	sentinel := errors.New("failed")
	req.Runner = func(string, string, []string, ...string) error { return sentinel }
	if _, err := Run(req); !errors.Is(err, sentinel) {
		t.Fatal(err)
	}
	req.DryRun = true
	req.Stdout = &bytes.Buffer{}
	if _, err := Run(req); err != nil {
		t.Fatal(err)
	}
	req.Overrides.Platform = "darwin/arm64"
	if _, err := Run(req); err == nil {
		t.Fatal("accepted non-Linux")
	}
	req.Overrides.Platform = "linux/amd64"
	req.Overrides.Dockerfile = "missing"
	if _, err := Run(req); err == nil || !strings.Contains(err.Error(), "weld docker init") {
		t.Fatal(err)
	}
}
func TestBuildDefaultsAndCGO(t *testing.T) {
	dir := projectDir(t)
	files, err := Files(dir, nil, false, false)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := Init(dir, files, false, &out); err != nil {
		t.Fatal(err)
	}
	req := Request{Dir: dir, Name: "Demo", Runner: func(_, _ string, _ []string, args ...string) error {
		if !strings.Contains(strings.Join(args, " "), "linux/amd64") {
			t.Fatal(args)
		}
		return nil
	}}
	image, err := Run(req)
	if err != nil || image != "demo:latest" {
		t.Fatalf("%s %v", image, err)
	}
	enabled := true
	req.Config = &projectcfg.Config{Build: projectcfg.BuildConfig{Cgo: &enabled}}
	if _, err := Run(req); err == nil || !strings.Contains(err.Error(), "custom Dockerfile") {
		t.Fatal(err)
	}
}

func TestRunWarnsOnStaleDockerfile(t *testing.T) {
	dir := projectDir(t)
	files, err := Files(dir, nil, false, false)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := Init(dir, files, false, &out); err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	called := false
	req := Request{Dir: dir, Name: "demo", WebInstalled: true, HTTPInstalled: true, Stderr: &stderr, Runner: func(string, string, []string, ...string) error {
		called = true
		return nil
	}}
	if _, err := Run(req); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("runner not called")
	}
	if !strings.Contains(stderr.String(), "warning") || !strings.Contains(stderr.String(), "docker init") {
		t.Fatalf("missing stale warning: %s", stderr.String())
	}
}

func TestRunDoesNotWarnWhenMetadataMatches(t *testing.T) {
	dir := projectDir(t)
	files, err := Files(dir, nil, true, true)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := Init(dir, files, false, &out); err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	req := Request{Dir: dir, WebInstalled: true, HTTPInstalled: true, Stderr: &stderr, Runner: func(string, string, []string, ...string) error { return nil }}
	if _, err := Run(req); err != nil {
		t.Fatal(err)
	}
	if stderr.Len() != 0 {
		t.Fatalf("unexpected warning: %s", stderr.String())
	}
}
