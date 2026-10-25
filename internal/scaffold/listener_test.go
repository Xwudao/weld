package scaffold

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Xwudao/weld/internal/project"
)

// addListener plans and applies `weld add listener <name>`.
func addListener(t *testing.T, dir, name string) *Result {
	t.Helper()
	result, err := AddListener(Request{Dir: dir, Catalog: newCatalog()}, name)
	if err != nil {
		t.Fatalf("AddListener %s: %v", name, err)
	}
	if err := result.Apply(); err != nil {
		t.Fatalf("Apply add listener %s: %v", name, err)
	}
	return result
}

func TestValidateListenerName(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"item_created", "created", "order_paid2", "type"} {
		if err := ValidateListenerName(name); err != nil {
			t.Errorf("ValidateListenerName(%q) = %v, want nil", name, err)
		}
	}
	for _, name := range []string{"", "ItemCreated", "item-created", "_item", "item__created", "item_", "1item", "item_test", "sync_linux", "build_amd64"} {
		if err := ValidateListenerName(name); err == nil {
			t.Errorf("ValidateListenerName(%q) = nil, want an error", name)
		}
	}
}

func TestAddListenerWritesSeamsAndNotesRegistration(t *testing.T) {
	t.Parallel()
	dir := create(t, t.TempDir())
	add(t, dir, "events")
	wiringBefore := readFile(t, filepath.Join(dir, "internal/di/events_listeners.go"))

	result := addListener(t, dir, "item_created")

	body := readFile(t, filepath.Join(dir, "internal/listeners/item_created.go"))
	for _, want := range []string{"type ItemCreated struct{}", "func HandleItemCreated(ctx context.Context, event ItemCreated) error"} {
		if !strings.Contains(body, want) {
			t.Fatalf("item_created.go lacks %q:\n%s", want, body)
		}
	}
	test := readFile(t, filepath.Join(dir, "internal/listeners/item_created_test.go"))
	if !strings.Contains(test, `"`+testModule+`/internal/events"`) || !strings.Contains(test, "events.On(dispatcher, HandleItemCreated)") {
		t.Fatalf("item_created_test.go does not register the listener:\n%s", test)
	}
	manifest := mustLoad(t, dir)
	for _, path := range []string{"internal/listeners/item_created.go", "internal/listeners/item_created_test.go"} {
		file, ok := manifest.FileEntry(path)
		if !ok {
			t.Fatalf("%s is not recorded in the manifest", path)
		}
		if file.Capability != "listener:item_created" || file.Role != project.RoleSeam {
			t.Fatalf("%s recorded as %s/%s, want listener:item_created/seam", path, file.Capability, file.Role)
		}
	}
	if readFile(t, filepath.Join(dir, "internal/di/events_listeners.go")) != wiringBefore {
		t.Fatal("add listener rewrote internal/di/events_listeners.go")
	}
	if notes := strings.Join(result.Notes, "\n"); !strings.Contains(notes, "events.On(d, listeners.HandleItemCreated)") || !strings.Contains(notes, testModule+"/internal/listeners") {
		t.Fatalf("notes do not explain the registration:\n%s", notes)
	}
}

func TestAddListenerIsIdempotent(t *testing.T) {
	t.Parallel()
	dir := create(t, t.TempDir())
	add(t, dir, "events")
	addListener(t, dir, "item_created")
	path := filepath.Join(dir, "internal/listeners/item_created.go")
	edited := readFile(t, path) + "\n// edited by the project\n"
	if err := os.WriteFile(path, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := AddListener(Request{Dir: dir, Catalog: newCatalog()}, "item_created")
	if err != nil {
		t.Fatalf("second AddListener: %v", err)
	}
	if len(result.Operations) != 0 || !strings.Contains(strings.Join(result.Notes, "\n"), "already installed") {
		t.Fatalf("second AddListener = %d operations, notes %q; want a no-op", len(result.Operations), result.Notes)
	}
	if readFile(t, path) != edited {
		t.Fatal("a repeat add changed the project's listener")
	}
}

func TestAddListenerRefusesCollidingIdentifier(t *testing.T) {
	t.Parallel()
	dir := create(t, t.TempDir())
	add(t, dir, "events")
	addListener(t, dir, "a_b1")

	_, err := AddListener(Request{Dir: dir, Catalog: newCatalog()}, "a_b_1")
	conflict, ok := err.(*project.ConflictError)
	if !ok {
		t.Fatalf("AddListener a_b_1 after a_b1 = %v, want *project.ConflictError", err)
	}
	if conflict.Path != "internal/listeners/a_b1.go" {
		t.Fatalf("conflict path = %q", conflict.Path)
	}
}

func TestAddListenerRequiresEvents(t *testing.T) {
	t.Parallel()
	dir := create(t, t.TempDir())

	_, err := AddListener(Request{Dir: dir, Catalog: newCatalog()}, "item_created")
	missing, ok := err.(*MissingRequirementsError)
	if !ok {
		t.Fatalf("AddListener error = %v, want *MissingRequirementsError", err)
	}
	if got, want := strings.Join(missing.Missing, ","), "config,loom,events"; got != want {
		t.Fatalf("missing = %q, want %q", got, want)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "internal/listeners")); !os.IsNotExist(statErr) {
		t.Fatal("a failed AddListener wrote files")
	}
}

func TestAddListenerWithDependenciesInstallsChain(t *testing.T) {
	t.Parallel()
	dir := create(t, t.TempDir())

	result, err := AddListener(Request{Dir: dir, Catalog: newCatalog(), WithDependencies: true}, "item_created")
	if err != nil {
		t.Fatalf("AddListener --with-dependencies: %v", err)
	}
	if got, want := strings.Join(result.Installed, ","), "config,loom,events"; got != want {
		t.Fatalf("installed = %q, want %q", got, want)
	}
	if err := result.Apply(); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	for _, path := range []string{"internal/events/dispatcher.go", "internal/listeners/item_created.go"} {
		if _, err := os.Stat(filepath.Join(dir, path)); err != nil {
			t.Errorf("expected %s: %v", path, err)
		}
	}
}

// TestAddListenerRegisteredProjectBuildsAndTests follows the plan's note — it
// registers the listener in the events seam by hand — and proves the result
// compiles, is gofmt-clean and passes its tests.
func TestAddListenerRegisteredProjectBuildsAndTests(t *testing.T) {
	t.Parallel()
	loomToolAvailable(t)
	dir := create(t, t.TempDir())
	add(t, dir, "http", "events")
	addListener(t, dir, "item_created")

	seamPath := filepath.Join(dir, "internal/di/events_listeners.go")
	seam := readFile(t, seamPath)
	seam = strings.Replace(seam, `import "`+testModule+`/internal/events"`,
		"import (\n\t\""+testModule+"/internal/events\"\n\t\""+testModule+"/internal/listeners\"\n)", 1)
	seam = strings.Replace(seam, "(*EventListeners, error) {\n\treturn", "(*EventListeners, error) {\n\tevents.On(d, listeners.HandleItemCreated)\n\treturn", 1)
	if !strings.Contains(seam, "events.On(d, listeners.HandleItemCreated)") || !strings.Contains(seam, "/internal/listeners\"") {
		t.Fatalf("could not register the listener in the shipped seam:\n%s", seam)
	}
	if err := os.WriteFile(seamPath, []byte(seam), 0o644); err != nil {
		t.Fatal(err)
	}
	if !buildAndTestGeneratedProject(t, dir) {
		t.Skip("cannot resolve the generated project's dependencies (network/module cache unavailable)")
	}
}
