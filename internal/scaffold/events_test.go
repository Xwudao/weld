package scaffold

import (
	"go/format"
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
		"internal/events/async.go",
		"internal/events/async_test.go",
		"internal/events/recorder.go",
		"internal/events/README.md",
		"internal/di/events_provider.go",
		"internal/di/events_listeners.go",
		"internal/di/events_bindings.go",
		"internal/events/registration.go",
		"internal/events/registration_test.go",
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
		t.Errorf("the Loom graph does not declare the event publisher:\n%s", graph)
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

	paths := []string{"internal/di/events_listeners.go"}
	const listener = "\t// my listener\n"
	for _, path := range paths {
		edited(t, dir, path, readFile(t, filepath.Join(dir, path))+listener)
	}

	repeat := add(t, dir, "events")
	if len(repeat.Operations) != 0 {
		t.Fatalf("repeat add planned %v", plannedPaths(repeat))
	}
	add(t, dir, "redis")
	for _, path := range paths {
		if got := readFile(t, filepath.Join(dir, path)); !strings.Contains(got, listener) {
			t.Fatalf("a later add rewrote %s:\n%s", path, got)
		}
	}
}

// TestBusinessEventListeners proves the real generated HTTP graph consumes the
// wiring seam, and a consumer can depend on and publish through the same publisher.
func TestBusinessEventListeners(t *testing.T) {
	t.Parallel()
	loomToolAvailable(t)
	dir := create(t, t.TempDir())
	add(t, dir, "events")
	addModule(t, dir, "widget")
	formatGo := func(body string) string {
		t.Helper()
		formatted, err := format.Source([]byte(body))
		if err != nil {
			t.Fatal(err)
		}
		return string(formatted)
	}

	servicePath := "internal/modules/widget/service.go"
	service := readFile(t, filepath.Join(dir, servicePath))
	service = strings.Replace(service, `"context"`, `"context"
	"example.com/demo/internal/events"`, 1)
	// Generated projects use the module from create(), not the repository's.
	mod := strings.Fields(readFile(t, filepath.Join(dir, "go.mod")))[1]
	service = strings.ReplaceAll(service, "example.com/demo/internal/events", mod+"/internal/events")
	service = strings.Replace(service, "func NewService() Service {\n\treturn NewDemoService()\n}", `func NewService(publisher events.Publisher) Service {
	return &eventService{Service: NewDemoService(), publisher: publisher}
}`, 1)
	service += `
// Note and Handled are business events shared by this example.
type Note struct { Text string }
type Handled struct { Text string }

type eventService struct {
	Service
	publisher events.Publisher
}

func (s *eventService) Greet(ctx context.Context, req Request) (Response, error) {
	response, err := s.Service.Greet(ctx, req)
	if err != nil { return response, err }
	return response, events.TryPublish(ctx, s.publisher, Handled{Text: req.Name})
}
`
	edited(t, dir, servicePath, formatGo(service))
	listeners := `package di

import (
	"context"
	"__module__/internal/events"
	"__module__/internal/modules/widget"
)

type EventListeners struct{}
var wiredPublisher events.Publisher
var handled chan string

func NewEventListeners(d *events.Dispatcher, b widget.Service, publisher events.Publisher) (*EventListeners, error) {
	wiredPublisher = publisher
	handled = make(chan string, 2)
	// Capture this graph's channel, not the variable replaced by the next graph.
	delivered := handled
	events.On(d, func(ctx context.Context, event widget.Note) error {
		_, err := b.Greet(ctx, widget.Request{Name: event.Text})
		return err
	})
	events.On(d, func(_ context.Context, event widget.Handled) error {
		delivered <- event.Text
		return nil
	})
	return &EventListeners{}, nil
}
`
	listeners = formatGo(strings.ReplaceAll(listeners, "__module__", mod))
	edited(t, dir, "internal/di/events_listeners.go", listeners)
	// A later capability regenerates the graph, preserving the old publisher
	// seam and our new business listener seam. No raw-dispatcher split is needed.
	provider := readFile(t, filepath.Join(dir, "internal/di/events_provider.go"))
	add(t, dir, "redis")
	if got := readFile(t, filepath.Join(dir, "internal/di/events_provider.go")); got != provider {
		t.Fatal("existing publisher seam was changed")
	}
	if got := readFile(t, filepath.Join(dir, "internal/di/events_listeners.go")); got != listeners {
		t.Fatal("business listener seam was changed")
	}
	gen := readFile(t, filepath.Join(dir, "internal/di/loom_gen.go"))
	if !strings.Contains(gen, "NewEventListeners(") {
		t.Fatal("HTTP graph pruned business listener registration")
	}
	test := `package di

import (
	"context"
	"testing"
	"time"
	"__module__/internal/modules/widget"
)

func TestBusinessListenersFromGeneratedGraph(t *testing.T) {
	original := asyncEvents
	defer func() { asyncEvents = original }()
	for _, asynchronous := range []bool{false, true} {
		asyncEvents = asynchronous
		ctx := WithAddr(WithConfigPath(context.Background(), "../../config.yml"), "127.0.0.1:0")
		_, lifecycle, err := InitApp(ctx)
		if err != nil { t.Fatal(err) }
		if asynchronous {
			if err := lifecycle.Start(ctx); err != nil { t.Fatal(err) }
		}
		if err := wiredPublisher.Publish(ctx, widget.Note{Text: "hello"}); err != nil {
			t.Fatal(err)
		}
		select {
		case got := <-handled:
			if got != "hello" { t.Fatalf("got %q", got) }
		case <-time.After(3*time.Second):
			t.Fatal("business listener did not run")
		}
		stopCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		err = lifecycle.Stop(stopCtx)
		cancel()
		if err != nil { t.Fatal(err) }
		select {
		case <-handled: t.Fatal("duplicate listener registration")
		default:
		}
	}
}
`
	edited(t, dir, "internal/di/business_events_test.go", formatGo(strings.ReplaceAll(test, "__module__", mod)))
	if !buildAndTestGeneratedProject(t, dir) {
		t.Skip("cannot resolve generated project dependencies")
	}
}
