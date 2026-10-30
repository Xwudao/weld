package dev

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Xwudao/weld/internal/projectcfg"
)

func TestWatchGlob(t *testing.T) {
	for _, tc := range []struct {
		pattern, name string
		want          bool
	}{
		{"**/*.go", "main.go", true},
		{"**/*.go", "internal/app/app.go", true},
		{"*.go", "internal/app.go", false},
		{"internal/**/app?.[gh]o", "internal/app1.go", true},
		{"internal/**/app?.[gh]o", "internal/a/b/app2.go", true},
		{"internal/**", "internal/a/b.go", true},
		{"**/*.go", "main.go.tmp", false},
		{"config.yml", "config.yml", true},
	} {
		if got := matchWatchGlob(tc.pattern, tc.name); got != tc.want {
			t.Errorf("match(%q, %q) = %v, want %v", tc.pattern, tc.name, got, tc.want)
		}
	}
	for _, pattern := range []string{"", "[", "/tmp/*.go", "../*.go", "./*.go", "foo/**bar", `foo\*.go`, "C:/foo", "foo//bar"} {
		if _, err := newFileWatcher(t.TempDir(), []string{pattern}, nil); err == nil {
			t.Errorf("accepted invalid glob %q", pattern)
		}
	}
	if _, err := newFileWatcher(t.TempDir(), nil, []string{"["}); err == nil {
		t.Fatal("accepted invalid exclusion")
	}
	if w, err := newFileWatcher(t.TempDir(), nil, nil); w != nil || err != nil {
		t.Fatalf("default watcher = %v, %v", w, err)
	}
}

func writeWatchFile(t *testing.T, dir, name, body string) {
	t.Helper()
	file := filepath.Join(dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestWatcherChangesAndDebounce(t *testing.T) {
	dir := t.TempDir()
	writeWatchFile(t, dir, "main.go", "before")
	w, err := newFileWatcher(dir, []string{"**/*.go"}, []string{"**/*_test.go"})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	poll := func(after time.Duration, want bool) {
		t.Helper()
		now = now.Add(after)
		got, err := w.poll(now)
		if err != nil || got != want {
			t.Fatalf("poll = %v, %v; want %v", got, err, want)
		}
	}
	poll(time.Second, false)                    // Initial files do not trigger a restart.
	writeWatchFile(t, dir, "main.go", "after!") // Same-length content change.
	poll(0, false)
	writeWatchFile(t, dir, "main.go", "again!")
	poll(200*time.Millisecond, false)
	poll(200*time.Millisecond, false)
	poll(100*time.Millisecond, true)
	poll(time.Second, false)
	writeWatchFile(t, dir, "main.go", "again!") // Identical rewrites are ignored.
	writeWatchFile(t, dir, "internal/app_test.go", "ignored")
	writeWatchFile(t, dir, "README.md", "ignored")
	for _, name := range []string{".git", ".weld", "node_modules", "web/node_modules"} {
		writeWatchFile(t, dir, name+"/ignored.go", "ignored")
	}
	poll(time.Second, false)
	writeWatchFile(t, dir, "new/nested/app.go", "created")
	poll(0, false)
	poll(watchDebounce, true)
	if err := os.Rename(filepath.Join(dir, "new"), filepath.Join(dir, "renamed")); err != nil {
		t.Fatal(err)
	}
	poll(0, false)
	poll(watchDebounce, true)
	if err := os.Remove(filepath.Join(dir, "main.go")); err != nil {
		t.Fatal(err)
	}
	poll(0, false)
	poll(watchDebounce, true)
}

func TestWatcherDoesNotFollowSymlinks(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	writeWatchFile(t, outside, "outside.go", "outside")
	if err := os.Symlink(outside, filepath.Join(dir, "linked")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := os.Symlink(filepath.Join(outside, "outside.go"), filepath.Join(dir, "linked.go")); err != nil {
		t.Fatal(err)
	}
	w, err := newFileWatcher(dir, []string{"**/*.go"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(w.previous) != 0 {
		t.Fatalf("followed symlinks: %v", w.previous)
	}
}

// Watch validation must happen before any backend is launched.
func TestRunRejectsInvalidWatchBeforeStarting(t *testing.T) {
	dir := t.TempDir()
	cfg := &projectcfg.Config{Dev: projectcfg.DevConfig{Backend: projectcfg.DevBackendConfig{
		Cmd: "nonexistent-weld-test-command", Watch: []string{"["},
	}}}
	err := Run(Request{Dir: dir, Config: cfg, Stdin: strings.NewReader("quit\n"), Stdout: io.Discard, Stderr: io.Discard})
	if err == nil || !strings.Contains(err.Error(), "dev.backend.watch") {
		t.Fatalf("Run error = %v", err)
	}
}

type watchOutput chan string

func (out watchOutput) Write(p []byte) (int, error) {
	out <- string(p)
	return len(p), nil
}

func TestSupervisorWatchRestartsOnlyBackend(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sh")
	}
	dir := t.TempDir()
	w, err := newFileWatcher(dir, []string{"**/*.go"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	output := make(watchOutput, 128)
	s := newSupervisor(ctx, []spec{
		{Name: backendName, Path: "sh", Args: []string{"-c", "exec sleep 30"}, Dir: dir},
		{Name: frontendName, Path: "sh", Args: []string{"-c", "exec sleep 30"}, Dir: dir},
	}, strings.NewReader(""), output, output)
	s.watcher = w
	done := make(chan error, 1)
	go func() { done <- s.run() }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("run: %v", err)
			}
		case <-time.After(15 * time.Second):
			t.Error("supervisor did not stop")
		}
	})
	waitFor := func(want string) {
		t.Helper()
		timeout := time.After(10 * time.Second)
		for {
			select {
			case line := <-output:
				if strings.Contains(line, "restarting frontend") {
					t.Fatal("watch restarted frontend")
				}
				if strings.Contains(line, want) {
					return
				}
			case <-timeout:
				t.Fatalf("timed out waiting for %q", want)
			}
		}
	}
	waitFor("started frontend")
	writeWatchFile(t, dir, "nested/app.go", "first")
	waitFor("restarting backend")
	waitFor("started backend")
	// The watcher stays active after a restart rather than stopping with the child.
	writeWatchFile(t, dir, "nested/app.go", "second")
	waitFor("restarting backend")
	waitFor("started backend")
}
