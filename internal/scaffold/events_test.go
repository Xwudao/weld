package scaffold

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestAddEventsInstallsLoomAndDispatcher proves events is a Loom-only
// capability: adding it to a fresh CLI installs the shared config, Loom and the
// dispatcher, and no HTTP surface.
func TestAddEventsInstallsLoomAndDispatcher(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	dir := create(t, root)

	result := add(t, dir, "events")
	if got, want := strings.Join(result.Installed, ","), "config,loom,events"; got != want {
		t.Fatalf("installed = %q, want %q", got, want)
	}
	for _, path := range []string{
		"internal/events/dispatcher.go",
		"internal/events/dispatcher_test.go",
		"internal/events/recorder.go",
		"internal/events/register.go",
		"internal/events/README.md",
		"internal/di/events_provider.go",
	} {
		if _, err := os.Stat(filepath.Join(dir, path)); err != nil {
			t.Errorf("expected %s: %v", path, err)
		}
	}
	// The dispatcher stays off the HTTP surface, so a CLI-only project can use it.
	for _, path := range []string{"internal/httpserver", "internal/app/serve.go", "web"} {
		if _, err := os.Stat(filepath.Join(dir, path)); !os.IsNotExist(err) {
			t.Errorf("events installed the HTTP surface %s", path)
		}
	}
	graph := readFile(t, filepath.Join(dir, "internal/di/di.go"))
	if !strings.Contains(graph, "loom.Provide(NewEventDispatcher)") {
		t.Errorf("the Loom graph does not declare the event dispatcher:\n%s", graph)
	}
	if drift := mustLoad(t, dir).Drift(dir); len(drift) != 0 {
		t.Fatalf("drift after add: %+v", drift)
	}
	if !buildAndTestGeneratedProject(t, dir) {
		t.Skip("cannot resolve the generated project's dependencies (network/module cache unavailable)")
	}
}

// TestAddEventsIsIdempotentAndKeepsTheRegistrationSeam proves a repeat add is a
// no-op and registering a listener is not undone by a later capability.
func TestAddEventsIsIdempotentAndKeepsTheRegistrationSeam(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	dir := create(t, root)
	add(t, dir, "events")

	const path = "internal/events/register.go"
	const listener = "\t// my listener\n"
	edited(t, dir, path, readFile(t, filepath.Join(dir, path))+listener)

	repeat := add(t, dir, "events")
	if len(repeat.Operations) != 0 {
		t.Fatalf("repeat add planned %v", plannedPaths(repeat))
	}
	add(t, dir, "redis")
	if got := readFile(t, filepath.Join(dir, path)); !strings.Contains(got, listener) {
		t.Fatalf("a later add rewrote the registration seam:\n%s", got)
	}
}

// TestAddEventsDispatchesInTheGeneratedProject is the end-to-end proof: a
// listener registered through the Loom provider runs when the application
// publishes, in a project generated here and compiled with the real toolchain.
func TestAddEventsDispatchesInTheGeneratedProject(t *testing.T) {
	t.Parallel()
	loomToolAvailable(t)
	root := t.TempDir()
	dir := create(t, root)
	add(t, dir, "events")

	// Register a listener the way a project does, and assert delivery from the
	// generated package's own test run.
	edited(t, dir, "internal/events/register.go", `package events

import "context"

// Note is the event the registered listener records.
type Note struct{ Text string }

var delivered []string

func Register(d *Dispatcher) error {
	On(d, func(_ context.Context, event Note) error {
		delivered = append(delivered, event.Text)
		return nil
	})
	return nil
}

// Delivered returns the events the registered listener observed.
func Delivered() []string { return delivered }
`)
	edited(t, dir, "internal/events/register_gen_test.go", `package events

import (
	"context"
	"testing"
)

func TestRegisteredListenerRunsOnPublish(t *testing.T) {
	d := New(nil)
	if err := Register(d); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := d.Publish(context.Background(), Note{Text: "hello"}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if got := Delivered(); len(got) != 1 || got[0] != "hello" {
		t.Fatalf("delivered = %v", got)
	}
}
`)
	if !buildAndTestGeneratedProject(t, dir) {
		t.Skip("cannot resolve the generated project's dependencies (network/module cache unavailable)")
	}
}
