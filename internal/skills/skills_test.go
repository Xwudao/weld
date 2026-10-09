package skills

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Xwudao/weld/internal/project"
	"github.com/Xwudao/weld/internal/template"
)

// fakeCatalog supplies descriptions for capabilities a test declares, so a
// future capability can be exercised without adding it to the embedded
// template module.
type fakeCatalog struct {
	caps map[string]*template.Capability
}

func (f fakeCatalog) Get(name string) (*template.Capability, error) {
	capability, ok := f.caps[name]
	if !ok {
		return nil, fmt.Errorf("capability %q not found", name)
	}
	return capability, nil
}

func ref(name string) project.CapabilityRef {
	return project.CapabilityRef{Name: name, Version: "0.1.0"}
}

func manifest(caps ...string) *project.Manifest {
	m := project.NewManifest("demo", "example.com/demo", "0.1.0")
	m.Base = ref("base")
	for _, name := range caps {
		m.Capabilities = append(m.Capabilities, ref(name))
	}
	return m
}

func TestRenderDescribesInstalledCapabilities(t *testing.T) {
	body, err := Render(manifest("config", "http", "api", "db", "redis"), template.Load())
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	text := string(body)
	for _, want := range []string{
		"name: weld",
		"# weld project skill: demo",
		"example.com/demo",
		"**base** v0.1.0",
		"**config** v0.1.0",
		"**http** v0.1.0",
		"**api** v0.1.0",
		"**db** v0.1.0",
		"**redis** v0.1.0",
		"## Installed is not the same as wired",
		"in-memory development demo",
		"## Generated and editable files",
		"## Regenerating this skill",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("rendered skill is missing %q", want)
		}
	}
	// A capability that is not installed is not claimed to be installed.
	if strings.Contains(text, "**loom**") {
		t.Error("rendered skill claims loom is installed when it is not")
	}
}

func TestRenderIsDeterministicAndOrderIndependent(t *testing.T) {
	first, err := Render(manifest("db", "api"), template.Load())
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	second, err := Render(manifest("db", "api"), template.Load())
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("Render is not deterministic")
	}
	reordered, err := Render(manifest("api", "db"), template.Load())
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !bytes.Equal(first, reordered) {
		t.Fatal("Render depends on capability install order")
	}
}

func TestRenderListsManagedFilesSorted(t *testing.T) {
	m := manifest("http")
	m.Files = []project.ManagedFile{
		{Path: "internal/httpserver/server.go", Capability: "http"},
		{Path: "go.mod", Capability: "base"},
		{Path: "internal/httpserver/http.go", Capability: "http"},
		{Path: "main.go", Capability: "base"},
	}
	body, err := Render(m, template.Load())
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	text := string(body)
	if !strings.Contains(text, "### Files weld manages in this project") {
		t.Fatalf("managed file index missing:\n%s", text)
	}
	order := []string{"`go.mod`", "`internal/httpserver/http.go`", "`internal/httpserver/server.go`", "`main.go`"}
	previous := -1
	for _, path := range order {
		at := strings.Index(text, path)
		if at < 0 {
			t.Fatalf("managed file %s missing:\n%s", path, text)
		}
		if at < previous {
			t.Fatalf("managed file paths are not sorted:\n%s", text)
		}
		previous = at
	}
}

func TestRenderLoomSeams(t *testing.T) {
	body, err := Render(manifest("config", "http", "api", "loom", "redis"), template.Load())
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	text := string(body)
	for _, want := range []string{
		"`internal/di/di.go` and `internal/di/loom_gen.go`",
		"`internal/di/api_provider.go`",
		"`internal/di/redis_provider.go`",
		"pruned until a provider depends on it",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("loom skill is missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "`internal/httpserver/api_route.go`") {
		t.Error("api_route.go is not a stable seam when loom is installed")
	}
}

func TestRenderWithoutAPIDoesNotClaimTheAPIService(t *testing.T) {
	body, err := Render(manifest("http"), template.Load())
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if strings.Contains(string(body), "in-memory") {
		t.Fatalf("an http-only project claims an in-memory API:\n%s", body)
	}
}

func TestRenderUnknownCapabilityUsesCatalogSummary(t *testing.T) {
	catalog := fakeCatalog{caps: map[string]*template.Capability{
		"metrics": {Name: "metrics", Version: "0.1.0", Summary: "Prometheus metrics and a /metrics route."},
	}}
	body, err := Render(manifest("metrics"), catalog)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(string(body), "Prometheus metrics and a /metrics route.") {
		t.Fatalf("future capability summary missing:\n%s", body)
	}
}

func TestRenderUnknownCapabilityWithoutDescriptionDoesNotLie(t *testing.T) {
	body, err := Render(manifest("mystery"), template.Load())
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	text := string(body)
	if !strings.Contains(text, "**mystery** v0.1.0") {
		t.Fatalf("unknown capability not listed:\n%s", text)
	}
	if !strings.Contains(text, "no description") {
		t.Fatalf("unknown capability should be described generically:\n%s", text)
	}
	if strings.Contains(text, "in-memory") {
		t.Fatal("unknown capability gained an invented claim about wiring")
	}
}

func TestEditedDetectsUserChanges(t *testing.T) {
	body, err := Render(manifest("http"), template.Load())
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if Edited(body) {
		t.Fatal("freshly generated content is reported as edited")
	}
	if !Edited(append(append([]byte(nil), body...), []byte("user note\n")...)) {
		t.Fatal("an appended user note is not detected")
	}
	index := bytes.LastIndex(body, []byte(trailerPrefix))
	if index < 0 {
		t.Fatal("trailer not found")
	}
	if !Edited(body[:index]) {
		t.Fatal("a file without the trailer is not treated as edited")
	}
	if !Edited([]byte("# a user's own skill file\n")) {
		t.Fatal("an unrelated file is not treated as edited")
	}
}

func TestGenerateApplyAndRepeat(t *testing.T) {
	root := t.TempDir()
	m := manifest("http")

	first, err := Generate(root, m, template.Load())
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if first.Existing || !first.Changed {
		t.Fatalf("fresh plan = %+v", first)
	}
	if err := first.Apply(root); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	path := filepath.Join(root, filepath.FromSlash(Path))
	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read generated file: %v", err)
	}

	second, err := Generate(root, m, template.Load())
	if err != nil {
		t.Fatalf("Generate repeat: %v", err)
	}
	if !second.Existing || second.Changed {
		t.Fatalf("repeat plan changed an up-to-date file: %+v", second)
	}
	if err := second.Apply(root); err != nil {
		t.Fatalf("Apply repeat: %v", err)
	}
	again, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read after repeat: %v", err)
	}
	if !bytes.Equal(written, again) {
		t.Fatal("repeat changed the generated file")
	}
}

func TestGenerateRefusesEditedFileAndPreservesOtherFiles(t *testing.T) {
	root := t.TempDir()
	other := filepath.Join(root, ".agents", "skills", "weld", "notes.md")
	if err := os.MkdirAll(filepath.Dir(other), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(other, []byte("my own notes\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	m := manifest("http")
	first, err := Generate(root, m, template.Load())
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if err := first.Apply(root); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	path := filepath.Join(root, filepath.FromSlash(Path))
	edited := append(append([]byte(nil), first.Content...), []byte("more notes\n")...)
	if err := os.WriteFile(path, edited, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Generate(root, m, template.Load()); err == nil {
		t.Fatal("expected Generate to refuse an edited file")
	}
	preserved, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(preserved, edited) {
		t.Fatal("edited skill file was overwritten")
	}
	notes, err := os.ReadFile(other)
	if err != nil {
		t.Fatalf("unrelated file was removed: %v", err)
	}
	if string(notes) != "my own notes\n" {
		t.Fatal("unrelated file was modified")
	}
}

func TestGenerateRefusesEditedFileAfterCapabilityAdd(t *testing.T) {
	root := t.TempDir()
	m := manifest("http")
	first, err := Generate(root, m, template.Load())
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if err := first.Apply(root); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	path := filepath.Join(root, filepath.FromSlash(Path))
	if err := os.WriteFile(path, append([]byte(nil), first.Content...), 0o644); err != nil {
		t.Fatal(err)
	}
	// The user edits the file, then adds a capability the skill would describe.
	if err := os.WriteFile(path, []byte("user-owned\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	after := manifest("http", "db")
	if _, err := Generate(root, after, template.Load()); err == nil {
		t.Fatal("expected Generate to refuse after a capability add")
	}
}

// TestApplyRefusesConcurrentEdit proves a user edit made after Generate is a
// conflict at Apply time rather than a silent overwrite: the plan is rechecked
// against the file before it is written.
func TestApplyRefusesConcurrentEdit(t *testing.T) {
	root := t.TempDir()
	first, err := Generate(root, manifest("http"), template.Load())
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if err := first.Apply(root); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	// Plan an update against the generated file, then let the user edit the file
	// before the plan is applied.
	second, err := Generate(root, manifest("http", "db"), template.Load())
	if err != nil {
		t.Fatalf("Generate update: %v", err)
	}
	if !second.Changed {
		t.Fatal("expected the capability add to change the file")
	}
	path := filepath.Join(root, filepath.FromSlash(Path))
	edited := append(append([]byte(nil), second.Content...), []byte("user note\n")...)
	if err := os.WriteFile(path, edited, 0o644); err != nil {
		t.Fatal(err)
	}

	if err := second.Apply(root); err == nil {
		t.Fatal("expected Apply to refuse after a concurrent edit")
	}
	preserved, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(preserved, edited) {
		t.Fatal("Apply overwrote the concurrent user edit")
	}
}

// TestRenderCronMailStorageLabelsAndSeams proves the new capabilities are
// described accurately and their stable seams are listed, so a coding agent
// learns where to register cron jobs and how mail/storage are wired.
func TestRenderCronMailStorageLabelsAndSeams(t *testing.T) {
	body, err := Render(manifest("cron", "mail", "storage", "loom"), template.Load())
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	text := string(body)
	for _, want := range []string{
		"**cron** v0.1.0",
		"**mail** v0.1.0",
		"**storage** v0.1.0",
		"`internal/cron/register.go`",
		"`internal/di/cron_provider.go`",
		"`internal/di/mail_provider.go`",
		"`internal/di/storage_provider.go`",
		"schedules nothing",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("rendered skill is missing %q:\n%s", want, text)
		}
	}
}

// TestRenderCronAloneStillNamesTheRegistrationSeam proves a cron-only project is
// told where jobs are declared even without Loom.
func TestRenderCronAloneStillNamesTheRegistrationSeam(t *testing.T) {
	body, err := Render(manifest("cron"), template.Load())
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	text := string(body)
	if !strings.Contains(text, "`internal/cron/register.go`") {
		t.Errorf("cron-only skill does not name the registration seam:\n%s", text)
	}
	if strings.Contains(text, "`internal/di/cron_provider.go`") {
		t.Errorf("cron-only skill claims a Loom provider seam that is not installed:\n%s", text)
	}
}
