package scaffold

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/Xwudao/weld/internal/project"
	"github.com/Xwudao/weld/internal/template"
)

// listenerDir is the package `weld add listener` writes into, relative to the
// project root. It is separate from the events capability's own internal/events
// package, which weld owns and may rewrite on upgrade.
const listenerDir = "internal/listeners"

// ValidateListenerName reports why a listener name is unusable. The name is the
// <name>.go file and, in CamelCase, both the event type and the Handle<Event>
// function, so it is lower_snake_case (item_created -> ItemCreated).
func ValidateListenerName(name string) error {
	if !enumValuePattern.MatchString(name) {
		return fmt.Errorf("invalid listener name %q: use lower_snake_case starting with a letter, for example item_created", name)
	}
	return checkFileNameSuffix("listener", name)
}

// checkCamelCollision refuses a lower_snake_case name whose CamelCase form is
// already declared by another <name>.go in dir: a_b1 and a_b_1 are different
// files but the same AB1 identifier, so the second would not compile.
func checkCamelCollision(root, dir, name string) error {
	matches, err := filepath.Glob(filepath.Join(root, dir, "*.go"))
	if err != nil {
		return fmt.Errorf("inspect %s: %w", dir, err)
	}
	for _, match := range matches {
		other := strings.TrimSuffix(filepath.Base(match), ".go")
		if strings.HasSuffix(other, "_test") || other == name {
			continue
		}
		if camel(other) == camel(name) {
			return &project.ConflictError{
				Path:   filepath.ToSlash(filepath.Join(dir, other+".go")),
				Reason: fmt.Sprintf("it already declares %s, the identifier %q would use", camel(name), name),
			}
		}
	}
	return nil
}

// fileNameSuffixes are the trailing _<word> segments the go command reads from a
// file name: _test makes a test file, and a GOOS or GOARCH builds the file only on
// that platform. A generated <name>.go must not end in one, or it would silently
// drop out of the normal build.
var fileNameSuffixes = map[string]bool{
	"test": true,
	"aix":  true, "android": true, "darwin": true, "dragonfly": true, "freebsd": true,
	"hurd": true, "illumos": true, "ios": true, "js": true, "linux": true, "nacl": true,
	"netbsd": true, "openbsd": true, "plan9": true, "solaris": true, "wasip1": true,
	"windows": true, "zos": true,
	"386": true, "amd64": true, "amd64p32": true, "arm": true, "arm64": true,
	"arm64be": true, "armbe": true, "loong64": true, "mips": true, "mips64": true,
	"mips64le": true, "mips64p32": true, "mips64p32le": true, "mipsle": true,
	"ppc": true, "ppc64": true, "ppc64le": true, "riscv": true, "riscv64": true,
	"s390": true, "s390x": true, "sparc": true, "sparc64": true, "wasm": true,
}

// checkFileNameSuffix rejects a lower_snake_case name whose last segment the go
// command would read as a test or build-constraint suffix (item_test,
// sync_linux).
func checkFileNameSuffix(kind, name string) error {
	i := strings.LastIndex(name, "_")
	if i < 0 {
		return nil
	}
	if suffix := name[i+1:]; fileNameSuffixes[suffix] {
		return fmt.Errorf("invalid %s name %q: the go command reads a trailing _%s in %s.go as a test or platform suffix", kind, name, suffix, name)
	}
	return nil
}

// AddListener plans an event listener named name: internal/listeners/<name>.go
// with the event type and its Handle<Event> listener, and a test that registers
// it on a real dispatcher and publishes the event.
//
// The files are project-owned seams, written once and never rewritten. The
// listener is not registered automatically: registration belongs in the
// project-owned internal/di/events_listeners.go, which carries no marker region
// weld may patch, so the plan notes the events.On line to add there instead.
//
// The events capability (and its loom/config chain) must be installed first; a
// missing requirement is an error unless Request.WithDependencies is set.
func AddListener(req Request, name string) (*Result, error) {
	if err := ValidateListenerName(name); err != nil {
		return nil, err
	}
	manifest, err := project.Load(req.Dir)
	if err != nil {
		return nil, err
	}

	path := filepath.ToSlash(filepath.Join(listenerDir, name+".go"))
	testPath := filepath.ToSlash(filepath.Join(listenerDir, name+"_test.go"))
	result := &Result{Dir: req.Dir}
	if manifest.Owns(path) {
		result.Notes = []string{fmt.Sprintf("listener %s is already installed", name)}
		return result, nil
	}

	if err := checkCamelCollision(req.Dir, listenerDir, name); err != nil {
		return nil, err
	}

	p := newPlanner(req, manifest, result)
	if err := p.requireCapability("events", fmt.Sprintf("listener %q", name)); err != nil {
		return nil, err
	}

	owner := "listener:" + name
	vars := template.Vars{Name: manifest.Name, Module: manifest.Module}
	if err := p.planNewFile(path, owner, "", project.RoleSeam, listenerFile(name)); err != nil {
		return nil, err
	}
	if err := p.planNewFile(testPath, owner, "", project.RoleSeam, template.Render(listenerTestFile(name), vars)); err != nil {
		return nil, err
	}
	if err := p.finish(); err != nil {
		return nil, err
	}
	result.Notes = append(result.Notes, fmt.Sprintf(
		"listener %s is not registered yet: add events.On(d, listeners.Handle%s) to NewEventListeners in internal/di/events_listeners.go (import %q)",
		name, camel(name), manifest.Module+"/"+listenerDir))
	return result, nil
}

// listenerFile renders the <name>.go event and listener. Like a seeder it is
// built in code: only the identifiers change between names.
func listenerFile(name string) []byte {
	event := camel(name)
	return []byte(fmt.Sprintf(`package listeners

import "context"

// %[1]s is the event Handle%[1]s receives. The dispatcher keys listeners on the
// event's concrete type, so publish the value, not a pointer:
//
//	publisher.Publish(ctx, listeners.%[1]s{})
//
// Add the fields the event carries. When another package publishes it, move the
// type to a domain package both sides import, as internal/events/README.md
// describes, and keep this listener.
type %[1]s struct{}

// Handle%[1]s handles %[1]s. It is project-owned: weld writes it once and never
// rewrites it.
//
// Register it by adding events.On(d, listeners.Handle%[1]s) to NewEventListeners
// in internal/di/events_listeners.go. Delivery is synchronous by default, so keep
// it fast; a returned error is joined into the publisher's result and logged.
// When it needs a service, make it a method on that service instead and pass the
// service to NewEventListeners.
func Handle%[1]s(ctx context.Context, event %[1]s) error {
	_ = ctx
	_ = event
	return nil
}
`, event))
}

// listenerTestFile renders the hermetic test for the <name>.go listener.
func listenerTestFile(name string) []byte {
	event := camel(name)
	return []byte(fmt.Sprintf(`package listeners

import (
	"context"
	"testing"

	"__module__/internal/events"
	"__module__/internal/logging"
)

// TestHandle%[1]sReceivesThePublishedEvent registers the listener the way
// internal/di/events_listeners.go does and publishes the event through the
// dispatcher. Extend it when the listener gains behavior.
func TestHandle%[1]sReceivesThePublishedEvent(t *testing.T) {
	dispatcher := events.New(logging.Nop())
	events.On(dispatcher, Handle%[1]s)

	if err := dispatcher.Publish(context.Background(), %[1]s{}); err != nil {
		t.Fatalf("Publish(%[1]s) = %%v, want nil", err)
	}
}
`, event))
}
