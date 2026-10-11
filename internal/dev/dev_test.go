package dev

import (
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Xwudao/weld/internal/projectcfg"
)

func TestSpecsBackendDefault(t *testing.T) {
	got, err := specs(Request{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("specs = %d, want 1 (backend only)", len(got))
	}
	if got[0].Path != "go" || !reflect.DeepEqual(got[0].Args, []string{"run", "."}) {
		t.Errorf("backend spec = %+v", got[0])
	}
}

func TestSpecsBackendCustomCommandAndArgs(t *testing.T) {
	cfg := &projectcfg.Config{Dev: projectcfg.DevConfig{
		Backend: projectcfg.DevBackendConfig{Cmd: "air -c .air.toml"},
	}}
	got, err := specs(Request{Dir: t.TempDir(), Config: cfg})
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Path != "air" || !reflect.DeepEqual(got[0].Args, []string{"-c", ".air.toml"}) {
		t.Errorf("backend spec = %+v", got[0])
	}

	cfg.Dev.Backend = projectcfg.DevBackendConfig{Args: []string{"serve", "--addr", ":9000"}}
	got, err = specs(Request{Dir: t.TempDir(), Config: cfg})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got[0].Args, []string{"run", ".", "serve", "--addr", ":9000"}) {
		t.Errorf("backend args = %v", got[0].Args)
	}
}

func TestSpecsFrontendRequiresInstalledCapability(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "web"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := specs(Request{Dir: dir, FrontendInstalled: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("specs = %d, want 2", len(got))
	}
	frontend := got[1]
	if frontend.Path != "pnpm" || !reflect.DeepEqual(frontend.Args, []string{"run", "dev"}) {
		t.Errorf("frontend spec = %+v", frontend)
	}
	if frontend.Dir != filepath.Join(dir, "web") {
		t.Errorf("frontend dir = %q", frontend.Dir)
	}
	if frontend.URL != "http://localhost:5173/" {
		t.Errorf("frontend URL = %q", frontend.URL)
	}
}

func TestSpecsFrontendMissingDir(t *testing.T) {
	if _, err := specs(Request{Dir: t.TempDir(), FrontendInstalled: true}); err == nil {
		t.Fatal("expected an error for a missing frontend directory")
	}
}

func TestDetectFrontendURL(t *testing.T) {
	got, ok := detectFrontendURL("  \x1b[32m➜\x1b[0m  Local:   http://localhost:5173/")
	if !ok || got != "http://localhost:5173/" {
		t.Fatalf("detectFrontendURL = %q, %v", got, ok)
	}
	if _, ok := detectFrontendURL("no url here"); ok {
		t.Fatal("detectFrontendURL matched a line without a URL")
	}
}

func TestInferFrontendURL(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"run", "dev"}, "http://localhost:5173/"},
		{[]string{"run", "dev", "--port", "3000"}, "http://localhost:3000/"},
		{[]string{"run", "dev", "--host", "0.0.0.0"}, "http://localhost:5173/"},
		{[]string{"run", "dev", "--host=127.0.0.1", "--port=8081"}, "http://127.0.0.1:8081/"},
	}
	for _, tc := range cases {
		if got := inferFrontendURL(tc.args); got != tc.want {
			t.Errorf("inferFrontendURL(%v) = %q, want %q", tc.args, got, tc.want)
		}
	}
}

func TestExpandTarget(t *testing.T) {
	order := []string{"backend", "frontend"}
	if got, _ := expandTarget("b", order); !reflect.DeepEqual(got, []string{"backend"}) {
		t.Errorf("b = %v", got)
	}
	if got, _ := expandTarget("all", order); !reflect.DeepEqual(got, order) {
		t.Errorf("all = %v", got)
	}
	if _, err := expandTarget("nope", order); err == nil {
		t.Error("expected an error for an unknown target")
	}
}

// TestSupervisorStartsAndQuits exercises the control loop with a short-lived
// process: the backend prints, then the quit command from stdin ends the
// session cleanly.
func TestSupervisorStartsAndQuits(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sh")
	}
	dir := t.TempDir()
	cfg := &projectcfg.Config{Dev: projectcfg.DevConfig{
		Backend: projectcfg.DevBackendConfig{Cmd: "sh -c 'echo hello-from-dev; sleep 5'"},
	}}
	stdinR, stdinW := io.Pipe()
	var out strings.Builder
	done := make(chan error, 1)
	go func() {
		done <- Run(Request{
			Dir:    dir,
			Config: cfg,
			Stdin:  stdinR,
			Stdout: &out,
			Stderr: &out,
		})
	}()

	// Let the child print before quitting, so the stream assertion is not racing
	// the shutdown.
	time.Sleep(300 * time.Millisecond)
	if _, err := stdinW.Write([]byte("quit\n")); err != nil {
		t.Fatalf("write quit: %v", err)
	}
	_ = stdinW.Close()
	if err := <-done; err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(out.String(), "hello-from-dev") {
		t.Errorf("output did not stream the child's stdout:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "[dev]") {
		t.Errorf("output did not carry the dev prefix:\n%s", out.String())
	}
}
