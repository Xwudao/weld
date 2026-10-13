// Package events holds the descriptor contract test for the events capability.
// The payloads under files/ are Go templates for a generated project, so their
// tests are the templates themselves; this test guards the capability descriptor,
// the stable registration seam and the promise that installing events adds no
// dependency and patches no shared file.
package events

import (
	"encoding/json"
	"io/fs"
	"path"
	"strings"
	"testing"

	weldtemplate "github.com/Xwudao/weld/internal/weldtemplate"
)

type descriptor struct {
	Name     string   `json:"name"`
	Version  string   `json:"version"`
	Kind     string   `json:"kind"`
	Summary  string   `json:"summary"`
	Requires []string `json:"requires"`
	Files    []struct {
		Path   string   `json:"path"`
		Source string   `json:"source"`
		When   []string `json:"when"`
	} `json:"files"`
	Patches []struct {
		Path   string `json:"path"`
		Marker string `json:"marker"`
		Source string `json:"source"`
	} `json:"patches"`
}

func read(t *testing.T) descriptor {
	t.Helper()
	raw, err := fs.ReadFile(weldtemplate.FS(), "capabilities/events/capability.json")
	if err != nil {
		t.Fatalf("read descriptor: %v", err)
	}
	var d descriptor
	if err := json.Unmarshal(raw, &d); err != nil {
		t.Fatalf("invalid descriptor: %v", err)
	}
	return d
}

// TestEventsCapabilityRequiresLoomWithoutHTTP guards the events contract: it
// adopts Loom (which brings config) but stays independent of the HTTP surface, so
// a CLI-only project can dispatch events.
func TestEventsCapabilityRequiresLoomWithoutHTTP(t *testing.T) {
	t.Parallel()
	d := read(t)
	if d.Kind != "add" {
		t.Fatalf("kind = %q, want add", d.Kind)
	}
	if d.Version == "" || d.Summary == "" {
		t.Fatal("events needs a version and a summary")
	}
	if strings.Join(d.Requires, ",") != "loom" {
		t.Fatalf("requires = %v, want [loom]", d.Requires)
	}
	for _, forbidden := range []string{"http", "web", "db", "redis", "mail", "storage", "cron"} {
		for _, required := range d.Requires {
			if required == forbidden {
				t.Errorf("events requires %q; the dispatcher must stay independent of it", forbidden)
			}
		}
	}

	paths := map[string]string{}
	for _, file := range d.Files {
		paths[file.Path] = file.Source
	}
	for _, want := range []string{
		"internal/events/dispatcher.go",
		"internal/events/dispatcher_test.go",
		"internal/events/recorder.go",
		"internal/events/README.md",
		"internal/di/events_provider.go",
		"internal/di/events_listeners.go",
		"internal/di/events_bindings.go",
		"internal/events/registration.go",
	} {
		if _, ok := paths[want]; !ok {
			t.Errorf("events does not ship %s", want)
		}
	}
	if got := d.Files[len(d.Files)-1]; got.Path != "internal/di/events_provider.go" ||
		len(got.When) != 1 || got.When[0] != "loom" {
		t.Errorf("the Loom provider seam is not guarded by when: [loom]: %+v", got)
	}
	for _, file := range d.Files {
		if strings.Contains(file.Path, "route") || strings.Contains(file.Path, "handler") {
			t.Errorf("events ships an HTTP payload %q", file.Path)
		}
	}
}

// TestEventsShipsNoDependencyAndPatchesNothing proves installing events touches
// no shared file at all: the dispatcher is standard library only, so it adds no
// go.mod requirement, no configuration section and no Makefile target.
func TestEventsShipsNoDependencyAndPatchesNothing(t *testing.T) {
	t.Parallel()
	d := read(t)
	if len(d.Patches) != 0 {
		t.Fatalf("events patches %+v; it must extend no shared file", d.Patches)
	}
	for _, file := range d.Files {
		raw, err := fs.ReadFile(weldtemplate.FS(), path.Join("capabilities/events", file.Source))
		if err != nil {
			t.Fatalf("read payload %q: %v", file.Source, err)
		}
		body := string(raw)
		if strings.Contains(body, "github.com/") && !strings.Contains(body, "__module__/internal") {
			t.Errorf("payload %q pulls in a third-party module; the dispatcher is standard library only:\n%s", file.Source, body)
		}
		if strings.Contains(body, "slog.Default()") {
			t.Errorf("payload %q reaches for the process logger; it must use the injected one", file.Source)
		}
	}
}

// TestEventsRegistrationSeamIsStableAndExplicit guards the documented seam: a
// project registers listeners in one hand-owned file, the provider runs it once
// during graph construction, and nothing discovers listeners implicitly.
func TestEventsRegistrationSeamIsStableAndExplicit(t *testing.T) {
	t.Parallel()
	listeners, err := fs.ReadFile(weldtemplate.FS(), "capabilities/events/files/events_listeners.go.tmpl")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"func NewEventListeners(d *events.Dispatcher) (*EventListeners, error)", "return &EventListeners{}, nil"} {
		if !strings.Contains(string(listeners), want) {
			t.Fatalf("missing business registration seam %q", want)
		}
	}
	provider, err := fs.ReadFile(weldtemplate.FS(), "capabilities/events/files/events_provider.go.tmpl")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(provider), "events.New(logger)") {
		t.Error("publisher does not construct an injected dispatcher")
	}
	if strings.Contains(string(provider), "events.Register(") {
		t.Error("publisher still calls the removed low-level registration seam")
	}

	// Registration is explicit: no payload scans packages or reflects over types
	// to discover listeners.
	for _, source := range []string{
		"capabilities/events/files/dispatcher.go.tmpl",
		"capabilities/events/files/events_provider.go.tmpl",
		"capabilities/events/files/events_listeners.go.tmpl",
	} {
		raw, err := fs.ReadFile(weldtemplate.FS(), source)
		if err != nil {
			t.Fatalf("read %s: %v", source, err)
		}
		for _, forbidden := range []string{"go/packages", "plugin.Open", "WalkDir"} {
			if strings.Contains(string(raw), forbidden) {
				t.Errorf("%s discovers listeners implicitly through %q", source, forbidden)
			}
		}
	}
}

// TestEventsSeamWiresAsyncBehindASwitch guards the opt-in contract: the shipped
// seam really constructs events.Async when enabled (it is not a doc-only example),
// defaults to synchronous delivery, and returns the Publisher interface so the
// graph binds the interface either way.
func TestEventsSeamWiresAsyncBehindASwitch(t *testing.T) {
	t.Parallel()
	provider, err := fs.ReadFile(weldtemplate.FS(), "capabilities/events/files/events_provider.go.tmpl")
	if err != nil {
		t.Fatalf("read provider payload: %v", err)
	}
	body := string(provider)
	for _, want := range []string{
		"var asyncEvents = false",
		"events.NewAsync(",
		"events.Publisher",
		"loom.Hook{",
		"publisher.Start(ctx)",
		"publisher.Stop(ctx)",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("events seam is missing %q:\n%s", want, body)
		}
	}
}
