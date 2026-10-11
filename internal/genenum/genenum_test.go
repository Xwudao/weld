package genenum

import (
	"go/format"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const statusSource = `package enums

type Status string

const (
	StatusDraft     Status = "draft"
	StatusPublished Status = "published"
	StatusArchived  Status = "archived"
)
`

func detect(t *testing.T, sources ...Source) *Package {
	t.Helper()
	pkg, err := Detect(sources)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	return pkg
}

func TestDetectFindsStringEnum(t *testing.T) {
	t.Parallel()
	pkg := detect(t, Source{Path: "status.go", Content: []byte(statusSource)})
	if pkg.Name != "enums" {
		t.Fatalf("package name = %q, want enums", pkg.Name)
	}
	if len(pkg.Enums) != 1 {
		t.Fatalf("enums = %+v, want one", pkg.Enums)
	}
	status := pkg.Enums[0]
	if status.Name != "Status" || status.File != "status.go" {
		t.Fatalf("enum = %+v", status)
	}
	want := []Value{
		{Const: "StatusDraft", Raw: "draft"},
		{Const: "StatusPublished", Raw: "published"},
		{Const: "StatusArchived", Raw: "archived"},
	}
	if len(status.Values) != len(want) {
		t.Fatalf("values = %+v, want %d", status.Values, len(want))
	}
	for i, v := range status.Values {
		if v != want[i] {
			t.Errorf("value[%d] = %+v, want %+v", i, v, want[i])
		}
	}
}

// TestDetectIgnoresNonEnumTypes proves the detector only reports named string
// types with constants: an int type, a type without constants, an aliased type
// and a plain string constant are all ignored.
func TestDetectIgnoresNonEnumTypes(t *testing.T) {
	t.Parallel()
	src := `package enums

type Status string

const (
	StatusDraft Status = "draft"
)

type Code int

const (
	CodeOK Code = 1
)

type Name string

const Lonely = "x"

type Alias = string
`
	pkg := detect(t, Source{Path: "mixed.go", Content: []byte(src)})
	if len(pkg.Enums) != 1 || pkg.Enums[0].Name != "Status" {
		t.Fatalf("enums = %+v, want only Status", pkg.Enums)
	}
}

// TestDetectInheritsTypeWithinBlock proves a constant that omits its type takes
// the previous spec's type in the same const block. Only the first spec's value
// repeats, so the alias constant carries the value too and is still one enum.
func TestDetectInheritsTypeWithinBlock(t *testing.T) {
	t.Parallel()
	src := `package enums

type Status string

const (
	StatusDraft Status = "draft"
	StatusPublished     = "published"
)
`
	pkg := detect(t, Source{Path: "status.go", Content: []byte(src)})
	if len(pkg.Enums) != 1 {
		t.Fatalf("enums = %+v", pkg.Enums)
	}
	got := pkg.Enums[0].Values
	if len(got) != 2 || got[1].Const != "StatusPublished" || got[1].Raw != "published" {
		t.Fatalf("values = %+v", got)
	}
}

// TestDetectSkipsAliasSpec proves a spec with neither a type nor a value (which
// repeats the previous value) is not rendered as a second constant, so the
// generated map cannot gain a duplicate key.
func TestDetectSkipsAliasSpec(t *testing.T) {
	t.Parallel()
	src := `package enums

type Status string

const (
	StatusDraft Status = "draft"
	StatusAlso
)
`
	pkg := detect(t, Source{Path: "status.go", Content: []byte(src)})
	if got := pkg.Enums[0].Values; len(got) != 1 || got[0].Const != "StatusDraft" {
		t.Fatalf("values = %+v, want only StatusDraft", got)
	}
}

// TestDetectMultipleNamesPerSpec proves a spec that declares several constants
// at once is expanded in order.
func TestDetectMultipleNamesPerSpec(t *testing.T) {
	t.Parallel()
	src := `package enums

type Tier string

const (
	TierFree, TierPro Tier = "free", "pro"
)
`
	pkg := detect(t, Source{Path: "tier.go", Content: []byte(src)})
	got := pkg.Enums[0].Values
	if len(got) != 2 || got[0].Raw != "free" || got[1].Raw != "pro" {
		t.Fatalf("values = %+v", got)
	}
}

func TestDetectRejectsSyntaxError(t *testing.T) {
	t.Parallel()
	if _, err := Detect([]Source{{Path: "broken.go", Content: []byte("package enums\n\nfunc (")}}); err == nil {
		t.Fatal("expected a parse error")
	}
}

func TestFileName(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"Status":      "status_gen.go",
		"OrderStatus": "order_status_gen.go",
		"Tier":        "tier_gen.go",
	}
	for name, want := range cases {
		if got := FileName(Enum{Name: name}); got != want {
			t.Errorf("FileName(%s) = %q, want %q", name, got, want)
		}
	}
}

// TestRenderIsValidGoAndReferencesConstants proves the rendered output is
// gofmt-clean Go that references the declared constants rather than their
// literal values.
func TestRenderIsValidGoAndReferencesConstants(t *testing.T) {
	t.Parallel()
	pkg := detect(t, Source{Path: "status.go", Content: []byte(statusSource)})
	out, err := Render(pkg, pkg.Enums[0])
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !IsGenerated(out) {
		t.Fatalf("rendered output lacks the generated header:\n%s", out)
	}
	if _, err := format.Source(out); err != nil {
		t.Fatalf("rendered output is not valid Go: %v\n%s", err, out)
	}
	for _, want := range []string{
		"func (v Status) String() string",
		"func StatusValues() []Status",
		"func StatusNames() []string",
		"func ParseStatus(raw string) (Status, error)",
		"func (v Status) IsValid() bool",
		"func (v Status) MarshalText() ([]byte, error)",
		"func (v *Status) UnmarshalText(text []byte) error",
		"func (v Status) Value() (driver.Value, error)",
		"func (v *Status) Scan(src any) error",
		"string(StatusDraft):",
	} {
		if !strings.Contains(string(out), want) {
			t.Errorf("rendered output is missing %q:\n%s", want, out)
		}
	}
}

func TestLoadDirMissingIsNil(t *testing.T) {
	t.Parallel()
	pkg, err := LoadDir(filepath.Join(t.TempDir(), "absent"))
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}
	if pkg != nil {
		t.Fatalf("LoadDir on a missing dir = %+v, want nil", pkg)
	}
}

func TestLoadDirIgnoresTestsAndGenerated(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	for name, content := range map[string]string{
		"status.go":      statusSource,
		"status_gen.go":  "package enums\n\ntype Orphan string\n\nconst OrphanOne Orphan = \"one\"\n",
		"status_test.go": "package enums_test\n\ntype Fixture string\n\nconst FixtureValue Fixture = \"fixture\"\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	pkg, err := LoadDir(dir)
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}
	names := []string{}
	for _, e := range pkg.Enums {
		names = append(names, e.Name)
	}
	if got := strings.Join(names, ","); got != "Status" {
		t.Fatalf("enums = %q, want only Status (generated and test files are skipped)", got)
	}
}

func TestLoadDirOverlayUsesPlannedContent(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "status.go"), []byte(statusSource), 0o644); err != nil {
		t.Fatal(err)
	}
	// The overlay replaces status.go and adds a second enum.
	overlay := map[string][]byte{
		"role.go": []byte("package enums\n\ntype Role string\n\nconst RoleAdmin Role = \"admin\"\n"),
	}
	pkg, err := LoadDirOverlay(dir, overlay)
	if err != nil {
		t.Fatalf("LoadDirOverlay: %v", err)
	}
	names := []string{}
	for _, e := range pkg.Enums {
		names = append(names, e.Name)
	}
	if got := strings.Join(names, ","); got != "Role,Status" {
		t.Fatalf("enums = %q, want Role,Status (sorted by file)", got)
	}
}
