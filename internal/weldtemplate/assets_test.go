package weldtemplate

import (
	"bytes"
	"encoding/json"
	"go/format"
	"io/fs"
	"path"
	"strings"
	"testing"
	"text/template"
)

type descriptor struct {
	Name     string   `json:"name"`
	Version  string   `json:"version"`
	Kind     string   `json:"kind"`
	Summary  string   `json:"summary"`
	Requires []string `json:"requires"`
	DI       *struct {
		Dir    string `json:"dir"`
		Source string `json:"source"`
		Test   string `json:"test"`
	} `json:"di"`
	Files []struct {
		Path       string   `json:"path"`
		Source     string   `json:"source"`
		When       []string `json:"when"`
		WhenAbsent []string `json:"whenAbsent"`
	} `json:"files"`
	Patches []struct {
		Path       string   `json:"path"`
		Marker     string   `json:"marker"`
		Source     string   `json:"source"`
		Mode       string   `json:"mode"`
		Bootstrap  string   `json:"bootstrap"`
		When       []string `json:"when"`
		WhenAbsent []string `json:"whenAbsent"`
	} `json:"patches"`
}

func readDescriptor(t *testing.T, name string) descriptor {
	t.Helper()
	raw, err := fs.ReadFile(FS(), path.Join("capabilities", name, "capability.json"))
	if err != nil {
		t.Fatalf("%s: read descriptor: %v", name, err)
	}
	var d descriptor
	if err := json.Unmarshal(raw, &d); err != nil {
		t.Fatalf("%s: invalid descriptor: %v", name, err)
	}
	return d
}

func TestCapabilitiesAreWellFormed(t *testing.T) {
	fsys := FS()
	entries, err := fs.ReadDir(fsys, "capabilities")
	if err != nil {
		t.Fatalf("read capabilities: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("no capabilities embedded")
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		dir := path.Join("capabilities", name)
		d := readDescriptor(t, name)
		if d.Name != name {
			t.Errorf("%s: descriptor name is %q", name, d.Name)
		}
		if d.Version == "" {
			t.Errorf("%s: missing version", name)
		}
		switch d.Kind {
		case "base", "add":
		default:
			t.Errorf("%s: unexpected kind %q", name, d.Kind)
		}
		if d.Summary == "" {
			t.Errorf("%s: missing summary", name)
		}
		if len(d.Files) == 0 {
			t.Errorf("%s: no files", name)
		}
		for _, file := range d.Files {
			if file.Path == "" || file.Source == "" {
				t.Errorf("%s: file entry needs path and source", name)
				continue
			}
			if _, err := fs.Stat(fsys, path.Join(dir, file.Source)); err != nil {
				t.Errorf("%s: payload %q missing: %v", name, file.Source, err)
			}
		}
		for _, patch := range d.Patches {
			if patch.Path == "" || patch.Marker == "" || patch.Source == "" {
				t.Errorf("%s: patch entry needs path, marker and source", name)
				continue
			}
			if _, err := fs.Stat(fsys, path.Join(dir, patch.Source)); err != nil {
				t.Errorf("%s: patch payload %q missing: %v", name, patch.Source, err)
			}
		}
	}
}

// TestWebCapabilityRequiresHTTP guards web's single-mode contract: it needs
// http, ships the frontend handler but no serve or route payload (the Loom
// server graph composes web.Handler()), and patches only the Makefile and
// git-ignore regions.
func TestWebCapabilityRequiresHTTP(t *testing.T) {
	d := readDescriptor(t, "web")
	if len(d.Requires) != 1 || d.Requires[0] != "http" {
		t.Fatalf("web requires = %v, want [http]", d.Requires)
	}
	paths := map[string]bool{}
	for _, file := range d.Files {
		paths[file.Path] = true
		if file.Path == "internal/app/serve.go" || strings.HasSuffix(file.Path, "_route.go") {
			t.Errorf("web ships the removed route/serve payload %q", file.Path)
		}
	}
	for _, want := range []string{"internal/web/web.go", "internal/web/web_test.go"} {
		if !paths[want] {
			t.Errorf("web capability does not ship %s", want)
		}
	}
	var makefile bool
	for _, patch := range d.Patches {
		if patch.Path == "internal/httpserver/http.go" {
			t.Errorf("web still patches the removed routes extension point: %+v", patch)
		}
		if patch.Path == "Makefile" && patch.Marker == "web" {
			makefile = true
		}
	}
	if !makefile {
		t.Error("web does not patch the Makefile web extension point")
	}

	// The server graph, not a route seam, registers the frontend handler.
	graph, err := fs.ReadFile(FS(), "capabilities/loom/files/di.go.tmpl")
	if err != nil {
		t.Fatalf("read loom graph: %v", err)
	}
	graphText := string(graph)
	if !strings.Contains(graphText, `{{- if .Caps.Has "web"}}`) || !strings.Contains(graphText, "web.Handler()") {
		t.Error("the Loom graph does not compose web.Handler() under a web guard")
	}
}

func TestHTTPCapabilityOwnsTheServeCommand(t *testing.T) {
	d := readDescriptor(t, "http")
	if d.Kind != "add" {
		t.Fatalf("http kind = %q, want add", d.Kind)
	}
	// http requires loom (which requires config): every add except config is a
	// Loom project, so the server graph is always available.
	if strings.Join(d.Requires, ",") != "loom" {
		t.Fatalf("http requires = %v, want [loom]", d.Requires)
	}
	paths := map[string]bool{}
	for _, file := range d.Files {
		paths[file.Path] = true
	}
	for _, want := range []string{
		"internal/httpserver/http.go",
		"internal/httpserver/server.go",
		"internal/app/serve.go",
	} {
		if !paths[want] {
			t.Errorf("http capability does not ship %s", want)
		}
	}
	// http appends its section to the config capability's local and example
	// YAML, so a db-only project never carries a gratuitous http section, and it
	// declares the tracked example to restore the git-ignored local file from.
	var configLocal, configExample bool
	for _, patch := range d.Patches {
		switch {
		case patch.Path == "config.yml" && patch.Marker == "config":
			configLocal = true
			if patch.Bootstrap != "config.example.yml" {
				t.Errorf("http config.yml patch bootstrap = %q, want config.example.yml", patch.Bootstrap)
			}
		case patch.Path == "config.example.yml" && patch.Marker == "config":
			configExample = true
		}
	}
	if !configLocal || !configExample {
		t.Errorf("http does not append its section to the config files: %+v", d.Patches)
	}
}

// TestAPICapabilityRequiresHTTP guards the API capability's contract: it needs
// http, patches the go.mod dependency region, and depends on neither web nor any
// database. It previously patched an httpserver routes region; the Loom server
// graph now calls api.Register directly, so the capability owns no route seam.
func TestAPICapabilityRequiresHTTP(t *testing.T) {
	d := readDescriptor(t, "api")
	if d.Kind != "add" {
		t.Fatalf("api kind = %q, want add", d.Kind)
	}
	if len(d.Requires) != 1 || d.Requires[0] != "http" {
		t.Fatalf("api requires = %v, want [http]", d.Requires)
	}
	for _, forbidden := range []string{"web", "db", "loom"} {
		for _, required := range d.Requires {
			if required == forbidden {
				t.Errorf("api requires %q", forbidden)
			}
		}
	}
	paths := map[string]bool{}
	for _, file := range d.Files {
		paths[file.Path] = true
	}
	for _, want := range []string{
		"internal/api/dto.go",
		"internal/api/service.go",
		"internal/api/handler.go",
		"internal/api/openapi.go",
		"internal/di/api_provider.go",
	} {
		if !paths[want] {
			t.Errorf("api capability does not ship %s", want)
		}
	}
	for _, file := range d.Files {
		if strings.Contains(file.Path, "httpserver") {
			t.Errorf("api still ships an httpserver payload %q; the graph owns route composition", file.Path)
		}
	}
	// The API provider file is created only once loom is also installed: when
	// api is added after loom. It carries the loom guard so it is never written
	// when loom is absent.
	var providerSeam bool
	for _, file := range d.Files {
		if file.Path == "internal/di/api_provider.go" {
			providerSeam = len(file.When) == 1 && file.When[0] == "loom" && len(file.WhenAbsent) == 0
		}
	}
	if !providerSeam {
		t.Errorf("api api_provider.go is not guarded by when: [loom]: %+v", d.Files)
	}
	var deps bool
	for _, patch := range d.Patches {
		switch {
		case patch.Path == "go.mod" && patch.Marker == "deps":
			deps = true
		case patch.Path == "internal/httpserver/http.go":
			t.Errorf("api still patches the removed routes extension point: %+v", patch)
		}
	}
	if !deps {
		t.Error("api does not patch the go.mod dependency region")
	}

	// The server graph registers the API routes when api is installed.
	graph, err := fs.ReadFile(FS(), "capabilities/loom/files/di.go.tmpl")
	if err != nil {
		t.Fatalf("read loom graph: %v", err)
	}
	if !strings.Contains(string(graph), `{{- if .Caps.Has "api"}}`) ||
		!strings.Contains(string(graph), "api.Register(mux, service)") {
		t.Error("the Loom graph does not call api.Register under an api guard")
	}
}

// TestDBCapabilityRequiresLoomNeverHTTP guards the db capability's contract: it
// adopts Loom (which brings config) but stays independent of the HTTP surface,
// patches the go.mod dependency region and the Makefile db region, and ships the
// SQL sources, the pinned sqlc tool module, the generated sqlc package and a
// repository.
func TestDBCapabilityRequiresLoomNeverHTTP(t *testing.T) {
	d := readDescriptor(t, "db")
	if d.Kind != "add" {
		t.Fatalf("db kind = %q, want add", d.Kind)
	}
	if strings.Join(d.Requires, ",") != "loom" {
		t.Fatalf("db requires = %v, want [loom]", d.Requires)
	}
	for _, forbidden := range []string{"http", "web", "api"} {
		for _, required := range d.Requires {
			if required == forbidden {
				t.Errorf("db requires %q", forbidden)
			}
		}
	}
	paths := map[string]bool{}
	for _, file := range d.Files {
		paths[file.Path] = true
	}
	for _, want := range []string{
		"sqlc.yaml",
		"db/migrations/000001_create_items.sql",
		"db/query/items.sql",
		"db/tools/go.mod",
		"db/tools/go.sum",
		"internal/data/data.go",
		"internal/data/sqlc/db.go",
		"internal/data/sqlc/models.go",
		"internal/data/sqlc/items.sql.go",
		"internal/data/data_test.go",
		"internal/data/postgres_integration_test.go",
		"internal/data/README.md",
	} {
		if !paths[want] {
			t.Errorf("db capability does not ship %s", want)
		}
	}
	var deps, makefile, configLocal, configExample bool
	for _, patch := range d.Patches {
		switch {
		case patch.Path == "go.mod" && patch.Marker == "deps":
			deps = true
		case patch.Path == "Makefile" && patch.Marker == "db":
			makefile = true
		case patch.Path == "config.yml" && patch.Marker == "config":
			configLocal = true
			if patch.Bootstrap != "config.example.yml" {
				t.Errorf("db config.yml patch bootstrap = %q, want config.example.yml", patch.Bootstrap)
			}
		case patch.Path == "config.example.yml" && patch.Marker == "config":
			configExample = true
		}
	}
	if !deps {
		t.Error("db does not patch the go.mod dependency region")
	}
	if !makefile {
		t.Error("db does not patch the Makefile db extension point")
	}
	if !configLocal || !configExample {
		t.Error("db does not merge its section into the local config and the committed example")
	}
}

// TestConfigCapabilityContract guards the config capability: it needs only base,
// ships the shared loader plus the local and example YAML, and patches the
// git-ignore and go.mod dependency regions.
func TestConfigCapabilityContract(t *testing.T) {
	d := readDescriptor(t, "config")
	if d.Kind != "add" {
		t.Fatalf("config kind = %q, want add", d.Kind)
	}
	if len(d.Requires) != 1 || d.Requires[0] != "base" {
		t.Fatalf("config requires = %v, want [base]", d.Requires)
	}
	paths := map[string]bool{}
	for _, file := range d.Files {
		paths[file.Path] = true
	}
	for _, want := range []string{"internal/config/config.go", "internal/config/config_test.go", "config.example.yml", "config.yml"} {
		if !paths[want] {
			t.Errorf("config capability does not ship %s", want)
		}
	}
	var gitignore, deps bool
	for _, patch := range d.Patches {
		switch {
		case patch.Path == ".gitignore" && patch.Marker == "config":
			gitignore = true
		case patch.Path == "go.mod" && patch.Marker == "deps":
			deps = true
		}
	}
	if !gitignore {
		t.Error("config does not patch the git-ignore region")
	}
	if !deps {
		t.Error("config does not patch the go.mod dependency region")
	}

	// The loader keeps DB validation separate and redacts secrets, so a local
	// password never reaches a log and an http-only project needs no DB setting.
	loader, err := fs.ReadFile(FS(), "capabilities/config/files/config.go.tmpl")
	if err != nil {
		t.Fatalf("read config.go: %v", err)
	}
	loaderText := string(loader)
	for _, want := range []string{
		"type Secret string",
		"func (Secret) LogValue() slog.Value",
		"func (Secret) MarshalJSON()",
		"func (c *Config) ValidateDatabase() error",
		"func NewLoader(readFile ReadFileFunc, lookupEnv LookupEnvFunc) *Loader",
		"gopkg.in/yaml.v3",
	} {
		if !strings.Contains(loaderText, want) {
			t.Errorf("config.go is missing %q", want)
		}
	}
	if strings.Contains(loaderText, "os.Getenv") {
		t.Error("config.go reads os.Getenv directly; the loader must take an injectable lookup")
	}

	// The example documents the database section and the local file is ignored.
	gitignoreSnippet, err := fs.ReadFile(FS(), "capabilities/config/files/gitignore.snippet")
	if err != nil {
		t.Fatalf("read gitignore snippet: %v", err)
	}
	if !strings.Contains(string(gitignoreSnippet), "config.yml") {
		t.Errorf("git-ignore snippet does not ignore config.yml:\n%s", gitignoreSnippet)
	}
	depsSnippet, err := fs.ReadFile(FS(), "capabilities/config/files/go.mod.snippet")
	if err != nil {
		t.Fatalf("read go.mod snippet: %v", err)
	}
	if !strings.Contains(string(depsSnippet), "gopkg.in/yaml.v3") {
		t.Errorf("config go.mod snippet does not require yaml.v3:\n%s", depsSnippet)
	}

	// The base scaffold owns the git-ignore extension point config patches.
	baseGitignore, err := fs.ReadFile(FS(), "capabilities/base/files/gitignore.txt")
	if err != nil {
		t.Fatalf("read base gitignore: %v", err)
	}
	if !strings.Contains(string(baseGitignore), "# weld:config:begin") {
		t.Error("base .gitignore is missing the weld:config extension point")
	}
}

// TestDBCapabilityPayloadsAreSqlcOutputAndPinnedTool checks the pieces that make
// the capability reproducible: the generated package really is sqlc output, the
// sqlc tool version is pinned in a nested module, and pgx/v5 is pinned in the
// go.mod snippet.
func TestDBCapabilityPayloadsAreSqlcOutputAndPinnedTool(t *testing.T) {
	for _, file := range []string{
		"capabilities/db/files/sqlc/db.go.tmpl",
		"capabilities/db/files/sqlc/models.go.tmpl",
		"capabilities/db/files/sqlc/items.sql.go.tmpl",
	} {
		raw, err := fs.ReadFile(FS(), file)
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		if !strings.Contains(string(raw), "Code generated by sqlc. DO NOT EDIT.") {
			t.Errorf("%s is not sqlc output", file)
		}
	}
	toolsMod, err := fs.ReadFile(FS(), "capabilities/db/files/tools/go.mod.tmpl")
	if err != nil {
		t.Fatalf("read db/tools go.mod: %v", err)
	}
	// Strip comments so the checks below assert the actual directives, not the
	// explanatory header.
	var toolsModCode []string
	for _, line := range strings.Split(string(toolsMod), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "//") {
			continue
		}
		toolsModCode = append(toolsModCode, line)
	}
	toolsModText := strings.Join(toolsModCode, "\n")
	for _, want := range []string{
		"github.com/sqlc-dev/sqlc/cmd/sqlc",
		"github.com/pressly/goose/v3/cmd/goose",
		"github.com/sqlc-dev/sqlc v1.31.1 // indirect",
		"github.com/pressly/goose/v3 v3.28.0 // indirect",
	} {
		if !strings.Contains(toolsModText, want) {
			t.Errorf("db/tools go.mod is missing %q", want)
		}
	}
	if !strings.Contains(toolsModText, "__module__/db/tools") {
		t.Error("db/tools go.mod does not use the project module placeholder")
	}
	snippet, err := fs.ReadFile(FS(), "capabilities/db/files/go.mod.snippet")
	if err != nil {
		t.Fatalf("read db go.mod snippet: %v", err)
	}
	if !strings.Contains(string(snippet), "require github.com/jackc/pgx/v5 v5.7.6") {
		t.Errorf("db go.mod snippet does not pin pgx/v5:\n%s", snippet)
	}
}

// TestDBCapabilityMigrationAndTestSafety guards the destructive-operation
// guardrails: the generated migration targets must use the pinned goose tool
// with an explicit DATABASE_URL guard and a one-step, confirmed down, and the
// generated integration test must isolate itself in its own schema instead of
// dropping application tables.
func TestDBCapabilityMigrationAndTestSafety(t *testing.T) {
	makefile, err := fs.ReadFile(FS(), "capabilities/db/files/Makefile.snippet")
	if err != nil {
		t.Fatalf("read db Makefile snippet: %v", err)
	}
	makeText := string(makefile)
	if strings.Contains(makeText, "psql") {
		t.Errorf("db Makefile snippet still shells out to psql:\n%s", makeText)
	}
	for _, want := range []string{
		"go tool goose",
		`test -n "$$DATABASE_URL"`,
		"CONFIRM",
		"migrate-status:",
	} {
		if !strings.Contains(makeText, want) {
			t.Errorf("db Makefile snippet is missing %q:\n%s", want, makeText)
		}
	}
	// The down target must revert one migration, never every migration.
	if strings.Contains(makeText, "rolls back ALL") || strings.Contains(makeText, "-all") {
		t.Errorf("db Makefile snippet rolls back more than one migration:\n%s", makeText)
	}

	toolsMod, err := fs.ReadFile(FS(), "capabilities/db/files/tools/go.mod.tmpl")
	if err != nil {
		t.Fatalf("read db/tools go.mod: %v", err)
	}
	if !strings.Contains(string(toolsMod), "tool github.com/pressly/goose/v3/cmd/goose") {
		t.Errorf("the goose migration tool is not pinned in db/tools:\n%s", toolsMod)
	}

	migration, err := fs.ReadFile(FS(), "capabilities/db/files/migrations/000001_create_items.sql")
	if err != nil {
		t.Fatalf("read db migration: %v", err)
	}
	for _, want := range []string{"-- +goose Up", "-- +goose Down"} {
		if !strings.Contains(string(migration), want) {
			t.Errorf("db migration is missing %q:\n%s", want, migration)
		}
	}

	test, err := fs.ReadFile(FS(), "capabilities/db/files/postgres_integration_test.go.tmpl")
	if err != nil {
		t.Fatalf("read db integration test: %v", err)
	}
	testText := string(test)
	if strings.Contains(testText, "DROP TABLE") {
		t.Errorf("db integration test drops tables outside its own schema:\n%s", testText)
	}
	for _, want := range []string{
		"CREATE SCHEMA",
		"search_path",
		"DROP SCHEMA",
		"CASCADE",
		"PG_TEST_DSN",
	} {
		if !strings.Contains(testText, want) {
			t.Errorf("db integration test is missing %q", want)
		}
	}
}

// TestBaseCapabilityIsCLIOnly guards the base scaffold against re-acquiring
// server or frontend payloads, which would break capability independence.
func TestBaseCapabilityIsCLIOnly(t *testing.T) {
	d := readDescriptor(t, "base")
	for _, file := range d.Files {
		switch {
		case file.Path == "internal/app/serve.go" ||
			strings.HasPrefix(file.Path, "internal/httpserver/") ||
			strings.HasPrefix(file.Path, "internal/web/") ||
			strings.HasPrefix(file.Path, "web/"):
			t.Errorf("base capability ships capability-specific file %q", file.Path)
		}
		raw, err := fs.ReadFile(FS(), path.Join("capabilities/base", file.Source))
		if err != nil {
			t.Fatalf("read base payload %q: %v", file.Source, err)
		}
		content := string(raw)
		if strings.Contains(content, "net/http") {
			t.Errorf("base payload %q imports net/http", file.Source)
		}
		if strings.Contains(content, "/api/health") {
			t.Errorf("base payload %q mentions the API health endpoint", file.Source)
		}
	}
}

// TestBaseCapabilityShipsInjectedLogger guards the logging milestone: base ships
// a log/slog factory and main logs the failed command through it, with no
// competing error path and no global logger.
func TestBaseCapabilityShipsInjectedLogger(t *testing.T) {
	d := readDescriptor(t, "base")
	paths := map[string]bool{}
	for _, file := range d.Files {
		paths[file.Path] = true
	}
	for _, want := range []string{"internal/logging/logging.go", "internal/logging/logging_test.go"} {
		if !paths[want] {
			t.Errorf("base capability does not ship %s", want)
		}
	}

	loggingGo, err := fs.ReadFile(FS(), "capabilities/base/files/logging.go.tmpl")
	if err != nil {
		t.Fatalf("read logging.go: %v", err)
	}
	for _, want := range []string{"\"log/slog\"", "NewTextHandler", "NewJSONHandler"} {
		if !strings.Contains(string(loggingGo), want) {
			t.Errorf("logging.go is missing %q", want)
		}
	}

	mainGo, err := fs.ReadFile(FS(), "capabilities/base/files/main.go.tmpl")
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	mainText := string(mainGo)
	for _, want := range []string{"logging.New", "logger.Error"} {
		if !strings.Contains(mainText, want) {
			t.Errorf("main.go is missing %q", want)
		}
	}
	for _, forbidden := range []string{"fmt.Fprintln", "log.Fatal", "slog.SetDefault"} {
		if strings.Contains(mainText, forbidden) {
			t.Errorf("main.go still uses a competing error path %q:\n%s", forbidden, mainText)
		}
	}
}

// TestBaseCapabilityShipsCobraCommandSeams guards the base contract after the
// Cobra migration: the base app builds a Cobra command tree and exposes the
// three contribution seams a capability or business module uses, go.mod pins
// Cobra (with its own requirements) and go.sum ships with the scaffold so a
// fresh project builds with no network access.
func TestBaseCapabilityShipsCobraCommandSeams(t *testing.T) {
	appGo, err := fs.ReadFile(FS(), "capabilities/base/files/app.go.tmpl")
	if err != nil {
		t.Fatalf("read base app.go: %v", err)
	}
	appText := string(appGo)
	for _, want := range []string{
		"github.com/spf13/cobra",
		"type CommandFactory func() *cobra.Command",
		"func RegisterCommand(",
		"func ConfigureRoot(",
		"func SetDefaultRun(",
		"func NewRootCommand() *cobra.Command",
	} {
		if !strings.Contains(appText, want) {
			t.Errorf("base app.go is missing the command seam %q", want)
		}
	}

	mod, err := fs.ReadFile(FS(), "capabilities/base/files/go.mod.tmpl")
	if err != nil {
		t.Fatalf("read base go.mod: %v", err)
	}
	modText := string(mod)
	for _, want := range []string{"require github.com/spf13/cobra v1.9.1", "github.com/spf13/pflag", "github.com/inconshreveable/mousetrap"} {
		if !strings.Contains(modText, want) {
			t.Errorf("base go.mod does not pin %q", want)
		}
	}
	if !strings.Contains(modText, "weld:deps:begin") {
		t.Error("base go.mod lost the weld:deps extension point")
	}
	if _, err := fs.ReadFile(FS(), "capabilities/base/files/go.sum"); err != nil {
		t.Errorf("base go.sum is missing: %v", err)
	}
}

// TestLoomShipsNoServeOrRoutePayloads guards the single-mode contract: the http
// capability owns the serve command and the Loom server graph composes the
// routes, so loom itself ships no serve or route payload and patches no serve
// region.
func TestLoomShipsNoServeOrRoutePayloads(t *testing.T) {
	d := readDescriptor(t, "loom")
	for _, file := range d.Files {
		if strings.HasPrefix(file.Path, "internal/app/") || strings.HasSuffix(file.Path, "_route.go") {
			t.Errorf("loom ships the removed serve/route payload %q; the http capability owns the serve command", file.Path)
		}
	}
	for _, patch := range d.Patches {
		if patch.Path == "internal/app/serve.go" {
			t.Errorf("loom still replaces the serve region; the http capability owns it: %+v", patch)
		}
	}
}

// TestHTTPCapabilityDrivesTheGraph guards the http serve command: it resolves
// the shared flags, enters the generated Loom graph and drives its lifecycle.
// Startup and shutdown logging belong to the graph's injected logger, so the
// command never builds its own logger or writes to the process streams, and
// httpserver/server.go logs nothing directly.
func TestHTTPCapabilityDrivesTheGraph(t *testing.T) {
	serveGo, err := fs.ReadFile(FS(), "capabilities/http/files/serve.go.tmpl")
	if err != nil {
		t.Fatalf("read serve.go: %v", err)
	}
	serveText := string(serveGo)
	for _, want := range []string{"di.InitApp", "di.WithConfigPath", "lifecycle.Start", "lifecycle.Stop"} {
		if !strings.Contains(serveText, want) {
			t.Errorf("serve.go is missing %q:\n%s", want, serveText)
		}
	}
	for _, forbidden := range []string{"logging.New", "os.Stderr", "fmt.Fprintf"} {
		if strings.Contains(serveText, forbidden) {
			t.Errorf("serve.go still owns logging via %q; the graph injects the logger", forbidden)
		}
	}

	serverGo, err := fs.ReadFile(FS(), "capabilities/http/files/server.go.tmpl")
	if err != nil {
		t.Fatalf("read server.go: %v", err)
	}
	serverText := string(serverGo)
	if strings.Contains(serverText, "os.Stderr") || strings.Contains(serverText, "fmt.Fprintf") {
		t.Errorf("server.go still writes to the process streams directly:\n%s", serverText)
	}
}

// TestHTTPCapabilityShipsTheHelperToolkit guards the shared HTTP layer: the http
// capability ships internal/httpx (envelope writers, JSON binding, middleware)
// and the project-owned middleware configuration file, and the toolkit stays
// free of go-validate, which remains a concern of the api capability.
func TestHTTPCapabilityShipsTheHelperToolkit(t *testing.T) {
	d := readDescriptor(t, "http")
	paths := map[string]string{}
	for _, file := range d.Files {
		paths[file.Path] = file.Source
	}
	for _, want := range []string{
		"internal/httpx/httpx.go",
		"internal/httpx/bind.go",
		"internal/httpx/middleware.go",
		"internal/httpx/httpx_test.go",
		"internal/httpx/bind_test.go",
		"internal/httpx/middleware_test.go",
		"internal/httpserver/middleware.go",
	} {
		if _, ok := paths[want]; !ok {
			t.Errorf("http capability does not ship %s", want)
		}
	}

	read := func(source string) string {
		raw, err := fs.ReadFile(FS(), path.Join("capabilities/http", source))
		if err != nil {
			t.Fatalf("read %s: %v", source, err)
		}
		return string(raw)
	}

	httpxGo := read(paths["internal/httpx/httpx.go"])
	for _, want := range []string{
		"type Envelope struct",
		"func JSON(",
		"func Error(",
		"func RawJSON(",
		"func RawText(",
		"func Bytes(",
		"func NoContent(",
	} {
		if !strings.Contains(httpxGo, want) {
			t.Errorf("httpx.go is missing %q", want)
		}
	}
	bindGo := read(paths["internal/httpx/bind.go"])
	for _, want := range []string{"func DecodeJSON[", "func QueryInt(", "type DecodeError struct"} {
		if !strings.Contains(bindGo, want) {
			t.Errorf("bind.go is missing %q", want)
		}
	}
	middlewareGo := read(paths["internal/httpx/middleware.go"])
	for _, want := range []string{"type Middleware func(http.Handler) http.Handler", "func Chain(", "func RequestID()", "func AccessLog(", "func Recover("} {
		if !strings.Contains(middlewareGo, want) {
			t.Errorf("httpx middleware.go is missing %q", want)
		}
	}
	// Binding takes a validator callback rather than importing go-validate, so
	// the toolkit stays usable by a capability that does not install api.
	for _, source := range []string{
		paths["internal/httpx/httpx.go"],
		paths["internal/httpx/bind.go"],
		paths["internal/httpx/middleware.go"],
	} {
		if strings.Contains(read(source), "github.com/Xwudao/go-validate") {
			t.Errorf("http payload %s imports go-validate; binding must take a validator callback", source)
		}
	}

	config := read(paths["internal/httpserver/middleware.go"])
	for _, want := range []string{"func middlewareChain(", "httpx.RequestID()", "httpx.AccessLog(", "httpx.Recover(", "outer-to-inner"} {
		if !strings.Contains(config, want) {
			t.Errorf("httpserver/middleware.go is missing %q", want)
		}
	}
	if !strings.Contains(read(paths["internal/httpserver/http.go"]), "httpx.Chain(middlewareChain(") {
		t.Error("http.go does not compose the configured middleware chain")
	}
}

// TestCapabilitiesShareTheResponseEnvelope guards the wire contract: the
// built-in API and the generated business module both write through the httpx
// envelope, the OpenAPI document is served raw, and the document describes the
// envelope the handlers write.
func TestCapabilitiesShareTheResponseEnvelope(t *testing.T) {
	read := func(pathName string) string {
		raw, err := fs.ReadFile(FS(), pathName)
		if err != nil {
			t.Fatalf("read %s: %v", pathName, err)
		}
		return string(raw)
	}

	handler := read("capabilities/api/files/handler.go.tmpl")
	for _, want := range []string{"httpx.DecodeJSON(", "httpx.JSON(", "httpx.RawJSON(", "func decodeBody["} {
		if !strings.Contains(handler, want) {
			t.Errorf("api handler.go is missing %q", want)
		}
	}
	openapi := read("capabilities/api/files/openapi.go.tmpl")
	for _, want := range []string{"func successEnvelope(", "func errorEnvelope(", "func envelopeSchema("} {
		if !strings.Contains(openapi, want) {
			t.Errorf("api openapi.go is missing %q", want)
		}
	}
	module := read("modules/files/module.go.tmpl")
	for _, want := range []string{"httpx.JSON(", "httpx.Error("} {
		if !strings.Contains(module, want) {
			t.Errorf("the module template is missing %q", want)
		}
	}
	// The OpenAPI artifact is a complete document for tooling, served unwrapped.
	if strings.Contains(handler, "writeJSON(") {
		t.Error("api handler.go still hand-rolls its own JSON writer")
	}
}

// TestLoomCapabilityBindsLogger guards the Loom logging wiring: the graph
// provides a *slog.Logger and the managed server consumes it, while nothing
// installs a process-wide default logger.
func TestLoomCapabilityBindsLogger(t *testing.T) {
	graph, err := fs.ReadFile(FS(), "capabilities/loom/files/di.go.tmpl")
	if err != nil {
		t.Fatalf("read di template: %v", err)
	}
	graphText := string(graph)
	for _, want := range []string{"loom.Provide(NewLogger)", "func NewLogger() *slog.Logger", "logger *slog.Logger", "logging.New"} {
		if !strings.Contains(graphText, want) {
			t.Errorf("di template is missing %q", want)
		}
	}
	if strings.Contains(graphText, "slog.SetDefault") {
		t.Error("di template calls slog.SetDefault; the logger must be injected")
	}
}

// TestLoomCapabilityRequiresConfigAndDeclaresGraph guards the loom capability's
// contract: it is additive, needs only config (never http), declares a
// capability-aware DI graph, and replaces (never appends to) the go directive
// region.
func TestLoomCapabilityRequiresConfigAndDeclaresGraph(t *testing.T) {
	d := readDescriptor(t, "loom")
	if d.Kind != "add" {
		t.Fatalf("loom kind = %q, want add", d.Kind)
	}
	if len(d.Requires) != 1 || d.Requires[0] != "config" {
		t.Fatalf("loom requires = %v, want [config]", d.Requires)
	}
	if d.DI == nil || d.DI.Dir != "internal/di" || d.DI.Source == "" || d.DI.Test == "" {
		t.Fatalf("loom di = %+v, want a dir, source and test", d.DI)
	}
	paths := map[string]bool{}
	for _, file := range d.Files {
		paths[file.Path] = true
	}
	for _, want := range []string{
		"tools/loom/go.mod",
		"tools/loom/go.sum",
	} {
		if !paths[want] {
			t.Errorf("loom capability does not ship %s", want)
		}
	}
	var goversion, makefile bool
	for _, patch := range d.Patches {
		switch {
		case patch.Path == "go.mod" && patch.Marker == "goversion":
			goversion = patch.Mode == "replace"
		case patch.Path == "Makefile" && patch.Marker == "loom":
			makefile = true
		}
	}
	if !goversion {
		t.Error("loom does not replace the go.mod goversion region")
	}
	if !makefile {
		t.Error("loom does not patch the Makefile loom extension point")
	}
}

// TestLoomCapabilityPinsGeneratorAndRaisesFloor checks the reproducibility
// pieces: the generator is pinned in the nested tools/loom module, the go
// directive region is raised to Loom's floor, and the go.mod snippet requires
// only the Loom runtime.
func TestLoomCapabilityPinsGeneratorAndRaisesFloor(t *testing.T) {
	fsys := FS()
	toolsMod, err := fs.ReadFile(fsys, "capabilities/loom/files/tools/go.mod.tmpl")
	if err != nil {
		t.Fatalf("read loom tools go.mod: %v", err)
	}
	toolsText := string(toolsMod)
	for _, want := range []string{
		"tool github.com/Xwudao/loom/cmd/loom",
		"github.com/Xwudao/loom v0.3.1",
		"__module__/tools/loom",
	} {
		if !strings.Contains(toolsText, want) {
			t.Errorf("loom tools go.mod is missing %q:\n%s", want, toolsText)
		}
	}
	if _, err := fs.ReadFile(fsys, "capabilities/loom/files/tools/go.sum"); err != nil {
		t.Errorf("loom tools go.sum is missing: %v", err)
	}
	goversion, err := fs.ReadFile(fsys, "capabilities/loom/files/goversion.snippet")
	if err != nil {
		t.Fatalf("read goversion snippet: %v", err)
	}
	if !strings.Contains(string(goversion), "go 1.25.0") {
		t.Errorf("goversion snippet = %q, want go 1.25.0", goversion)
	}
	deps, err := fs.ReadFile(fsys, "capabilities/loom/files/go.mod.snippet")
	if err != nil {
		t.Fatalf("read loom go.mod snippet: %v", err)
	}
	if !strings.Contains(string(deps), "require github.com/Xwudao/loom v0.3.1") {
		t.Errorf("loom go.mod snippet does not pin the runtime:\n%s", deps)
	}
	if strings.Contains(string(deps), "golang.org/x/tools") {
		t.Errorf("loom go.mod snippet leaks the generator dependency:\n%s", deps)
	}
	graph, err := fs.ReadFile(fsys, "capabilities/loom/files/di.go.tmpl")
	if err != nil {
		t.Fatalf("read di template: %v", err)
	}
	for _, want := range []string{`{{- if .Caps.Has "db"}}`, `{{- if .Caps.Has "api"}}`, "NewPool", "loom.Provide(NewAPIService)"} {
		if !strings.Contains(string(graph), want) {
			t.Errorf("di template is missing %q", want)
		}
	}
	// The provider is defined in the stable api_provider.go seam, not in the
	// regenerated graph: a provider in di.go would be erased by the next
	// capability install, taking the user's persistence wiring with it.
	if strings.Contains(string(graph), "func NewAPIService") {
		t.Error("di template defines NewAPIService; the provider must live in the stable api_provider.go")
	}
	if strings.Contains(string(graph), "repositoryService") {
		t.Error("di template still auto-wires the repository into the API service")
	}
	if strings.Contains(string(graph), "os.Getenv") {
		t.Error("di template reads os.Getenv directly; configuration must use the injected EnvLookup")
	}
}

// TestAPIProviderSeamIsOwnedByAPI guards the provider-ownership contract: the
// capability that needs the binding owns the stable seam, the Loom graph only
// references it, and loom never mirrors it.
func TestAPIProviderSeamIsOwnedByAPI(t *testing.T) {
	apiTemplate, err := fs.ReadFile(FS(), "capabilities/api/files/api_provider.go.tmpl")
	if err != nil {
		t.Fatalf("read api api_provider template: %v", err)
	}
	for _, want := range []string{
		"func NewAPIService() api.Service",
		"return api.NewService()",
		"__module__/internal/api",
	} {
		if !strings.Contains(string(apiTemplate), want) {
			t.Errorf("api_provider.go template is missing %q:\n%s", want, apiTemplate)
		}
	}

	d := readDescriptor(t, "api")
	var providerSeam bool
	for _, file := range d.Files {
		if file.Path != "internal/di/api_provider.go" {
			continue
		}
		providerSeam = file.Source == "files/api_provider.go.tmpl" &&
			len(file.When) == 1 && file.When[0] == "loom" && len(file.WhenAbsent) == 0
	}
	if !providerSeam {
		t.Errorf("api api_provider.go is not declared with source files/api_provider.go.tmpl guarded by when: [loom]: %+v", d.Files)
	}

	// loom must not mirror the provider: the api capability owns it.
	loom := readDescriptor(t, "loom")
	for _, file := range loom.Files {
		if file.Path == "internal/di/api_provider.go" {
			t.Error("loom mirrors api_provider.go; the api capability owns it")
		}
	}
	graph, err := fs.ReadFile(FS(), "capabilities/loom/files/di.go.tmpl")
	if err != nil {
		t.Fatalf("read loom graph: %v", err)
	}
	if !strings.Contains(string(graph), "loom.Provide(NewAPIService)") {
		t.Error("the Loom graph does not reference the api provider seam")
	}
}

// TestEveryCapabilityExceptBaseAndConfigAdoptsLoom guards the single-mode
// contract: base is a plain CLI and config is its pure-configuration
// prerequisite, while every other capability is a Loom project, either
// directly (db/redis/mail/storage/http require loom) or through http
// (api/web/cron require http, which requires loom).
func TestEveryCapabilityExceptBaseAndConfigAdoptsLoom(t *testing.T) {
	entries, err := fs.ReadDir(FS(), "capabilities")
	if err != nil {
		t.Fatal(err)
	}
	requires := map[string][]string{}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		requires[entry.Name()] = readDescriptor(t, entry.Name()).Requires
	}
	adoptsLoom := func(name string) bool {
		seen := map[string]bool{}
		var walk func(string) bool
		walk = func(n string) bool {
			if seen[n] {
				return false
			}
			seen[n] = true
			for _, req := range requires[n] {
				if req == "loom" || walk(req) {
					return true
				}
			}
			return false
		}
		return walk(name)
	}
	for name := range requires {
		switch name {
		case "base", "config":
			if adoptsLoom(name) {
				t.Errorf("%s adopts Loom; it must stay a non-Loom prerequisite", name)
			}
		case "loom":
			// loom is the provider itself, so it does not "adopt" its own graph.
		default:
			if !adoptsLoom(name) {
				t.Errorf("%s does not adopt Loom; every capability except base and config is a Loom project", name)
			}
		}
	}
}

// TestRedisCapabilityRequiresLoomWithoutHTTP guards the redis capability's
// contract: it adopts Loom (which brings config) but stays independent of the
// HTTP surface, ships the typed config extension and the lazy client package,
// and declares its Loom provider seam guarded on loom.
func TestRedisCapabilityRequiresLoomWithoutHTTP(t *testing.T) {
	d := readDescriptor(t, "redis")
	if d.Kind != "add" {
		t.Fatalf("redis kind = %q, want add", d.Kind)
	}
	if strings.Join(d.Requires, ",") != "loom" {
		t.Fatalf("redis requires = %v, want [loom]", d.Requires)
	}
	for _, forbidden := range []string{"http", "web", "api", "db"} {
		for _, required := range d.Requires {
			if required == forbidden {
				t.Errorf("redis requires %q", forbidden)
			}
		}
	}
	paths := map[string]bool{}
	for _, file := range d.Files {
		paths[file.Path] = true
	}
	for _, want := range []string{
		"internal/config/redis.go",
		"internal/config/redis_test.go",
		"internal/redisclient/redisclient.go",
		"internal/redisclient/redisclient_test.go",
		"internal/redisclient/README.md",
		"internal/di/redis_provider.go",
	} {
		if !paths[want] {
			t.Errorf("redis capability does not ship %s", want)
		}
	}
	var providerSeam bool
	for _, file := range d.Files {
		if file.Path == "internal/di/redis_provider.go" {
			providerSeam = len(file.When) == 1 && file.When[0] == "loom" && len(file.WhenAbsent) == 0
		}
	}
	if !providerSeam {
		t.Errorf("redis redis_provider.go is not guarded by when: [loom]: %+v", d.Files)
	}
	var deps, configLocal, configExample bool
	markers := map[string]bool{}
	for _, patch := range d.Patches {
		switch {
		case patch.Path == "go.mod" && patch.Marker == "deps":
			deps = true
		case patch.Path == "config.yml" && patch.Marker == "config":
			configLocal = true
			if patch.Bootstrap != "config.example.yml" {
				t.Errorf("redis config.yml patch bootstrap = %q, want config.example.yml", patch.Bootstrap)
			}
		case patch.Path == "config.example.yml" && patch.Marker == "config":
			configExample = true
		case patch.Path == "internal/config/config.go":
			markers[patch.Marker] = true
		}
	}
	if !deps {
		t.Error("redis does not patch the go.mod dependency region")
	}
	if !configLocal || !configExample {
		t.Error("redis does not merge its section into the local config and the committed example")
	}
	for _, want := range []string{"configfields", "configenv", "configdefaults"} {
		if !markers[want] {
			t.Errorf("redis does not patch the config.go %s extension point: %+v", want, d.Patches)
		}
	}

	// The client is lazy and free of the HTTP, JSON and data surfaces, and the
	// config file keeps the connection settings behind the redacting Secret.
	client, err := fs.ReadFile(FS(), "capabilities/redis/files/redisclient.go.tmpl")
	if err != nil {
		t.Fatalf("read redisclient template: %v", err)
	}
	clientText := string(client)
	if !strings.Contains(clientText, "github.com/redis/go-redis/v9") {
		t.Error("redisclient does not build a go-redis client")
	}
	for _, forbidden := range []string{"net/http", "encoding/json", "internal/data"} {
		if strings.Contains(clientText, forbidden) {
			t.Errorf("redisclient payload references %s", forbidden)
		}
	}
	config, err := fs.ReadFile(FS(), "capabilities/redis/files/redis_config.go.tmpl")
	if err != nil {
		t.Fatalf("read redis config template: %v", err)
	}
	for _, want := range []string{"type Redis struct", "Username Secret", "Password Secret", "func (c *Config) ValidateRedis() error"} {
		if !strings.Contains(string(config), want) {
			t.Errorf("redis config is missing %q", want)
		}
	}

	// Loom's graph is capability aware for redis too.
	graph, err := fs.ReadFile(FS(), "capabilities/loom/files/di.go.tmpl")
	if err != nil {
		t.Fatalf("read di template: %v", err)
	}
	for _, want := range []string{`{{- if .Caps.Has "redis"}}`, "loom.Provide(NewRedisClient)"} {
		if !strings.Contains(string(graph), want) {
			t.Errorf("loom di template is missing %q", want)
		}
	}
	if strings.Contains(string(graph), "func NewRedisClient") {
		t.Error("di template defines NewRedisClient; the provider must live in the stable redis_provider.go")
	}
}

// TestRedisProviderSeamIsOwnedByRedis guards provider ownership: redis ships the
// stable internal/di/redis_provider.go seam (guarded on loom), and loom never
// mirrors it.
func TestRedisProviderSeamIsOwnedByRedis(t *testing.T) {
	redisTemplate, err := fs.ReadFile(FS(), "capabilities/redis/files/redis_provider.go.tmpl")
	if err != nil {
		t.Fatalf("read redis redis_provider template: %v", err)
	}
	for _, want := range []string{"func NewRedisClient(cfg *config.Config) (*redis.Client, loom.Cleanup, error)", "__module__/internal/redisclient"} {
		if !strings.Contains(string(redisTemplate), want) {
			t.Errorf("redis_provider.go template is missing %q:\n%s", want, redisTemplate)
		}
	}

	d := readDescriptor(t, "redis")
	var providerSeam bool
	for _, file := range d.Files {
		if file.Path == "internal/di/redis_provider.go" {
			providerSeam = file.Source == "files/redis_provider.go.tmpl" &&
				len(file.When) == 1 && file.When[0] == "loom" && len(file.WhenAbsent) == 0
		}
	}
	if !providerSeam {
		t.Errorf("redis does not ship files/redis_provider.go.tmpl guarded by when: [loom]: %+v", d.Files)
	}

	loom := readDescriptor(t, "loom")
	for _, file := range loom.Files {
		if file.Path == "internal/di/redis_provider.go" {
			t.Error("loom mirrors redis_provider.go; the redis capability owns it")
		}
	}
}

// TestCronCapabilityRequiresHTTP guards the cron capability's contract: it needs
// http (never web, api, db or loom directly), ships the scheduler library plus a
// stable registration file, and declares its Loom provider seam guarded on loom.
// The plain app runtime seam is gone: the Loom cron provider owns the
// scheduler lifecycle.
func TestCronCapabilityRequiresHTTP(t *testing.T) {
	d := readDescriptor(t, "cron")
	if d.Kind != "add" {
		t.Fatalf("cron kind = %q, want add", d.Kind)
	}
	if strings.Join(d.Requires, ",") != "http" {
		t.Fatalf("cron requires = %v, want [http]", d.Requires)
	}
	for _, forbidden := range []string{"web", "api", "db", "loom"} {
		for _, required := range d.Requires {
			if required == forbidden {
				t.Errorf("cron requires %q", forbidden)
			}
		}
	}
	paths := map[string]string{}
	for _, file := range d.Files {
		paths[file.Path] = file.Source
		if strings.HasPrefix(file.Path, "internal/app/") {
			t.Errorf("cron ships the removed app runtime payload %q; the Loom provider owns it", file.Path)
		}
	}
	for _, want := range []string{
		"internal/cron/cron.go",
		"internal/cron/register.go",
		"internal/cron/README.md",
		"internal/config/cron.go",
		"internal/config/cron_test.go",
		"internal/di/cron_provider.go",
	} {
		if _, ok := paths[want]; !ok {
			t.Errorf("cron does not ship %s", want)
		}
	}
	var providerSeam bool
	for _, file := range d.Files {
		if file.Path == "internal/di/cron_provider.go" {
			providerSeam = file.Source == "files/cron_provider.go.tmpl" &&
				len(file.When) == 1 && file.When[0] == "loom" && len(file.WhenAbsent) == 0
		}
	}
	if !providerSeam {
		t.Errorf("cron cron_provider.go is not guarded by when: [loom]: %+v", d.Files)
	}
	markers := map[string]bool{}
	for _, patch := range d.Patches {
		switch {
		case patch.Path == "go.mod" && patch.Marker == "deps":
			markers["go.mod|deps"] = true
		case patch.Path == "config.yml" && patch.Marker == "config":
			if patch.Bootstrap != "config.example.yml" {
				t.Errorf("cron config.yml patch bootstrap = %q, want config.example.yml", patch.Bootstrap)
			}
			markers["config.yml|config"] = true
		case patch.Path == "config.example.yml" && patch.Marker == "config":
			markers["config.example.yml|config"] = true
		case patch.Path == "internal/config/config.go":
			markers["config.go|"+patch.Marker] = true
		}
	}
	for _, want := range []string{
		"go.mod|deps",
		"config.yml|config",
		"config.example.yml|config",
		"config.go|configfields",
		"config.go|configenv",
		"config.go|configdefaults",
	} {
		if !markers[want] {
			t.Errorf("cron does not patch %s: %+v", want, d.Patches)
		}
	}

	// The scheduler follows serve through the Loom cron provider, the stable
	// runtime that owns the Start/Stop hooks, and it does not schedule anything by
	// itself.
	provider, err := fs.ReadFile(FS(), "capabilities/cron/files/cron_provider.go.tmpl")
	if err != nil {
		t.Fatalf("read cron provider: %v", err)
	}
	providerText := string(provider)
	for _, want := range []string{"cron.New", "cron.Register", "scheduler.Start", "scheduler.Stop", "loom.Hook"} {
		if !strings.Contains(providerText, want) {
			t.Errorf("cron provider is missing %q:\n%s", want, providerText)
		}
	}
	register, err := fs.ReadFile(FS(), "capabilities/cron/files/register.go.tmpl")
	if err != nil {
		t.Fatalf("read cron register: %v", err)
	}
	if !strings.Contains(string(register), "func Register(s *Scheduler) error") {
		t.Errorf("cron register file does not expose Register:\n%s", register)
	}
	if !strings.Contains(string(register), "return nil") {
		t.Errorf("cron register file registers jobs by default:\n%s", register)
	}

	// Loom declares the provider and the graph root consumes the scheduler, so it
	// is not pruned.
	graph, err := fs.ReadFile(FS(), "capabilities/loom/files/di.go.tmpl")
	if err != nil {
		t.Fatalf("read loom di template: %v", err)
	}
	for _, want := range []string{`{{- if .Caps.Has "cron"}}`, "loom.Provide(NewScheduler)", "Scheduler *cron.Scheduler"} {
		if !strings.Contains(string(graph), want) {
			t.Errorf("loom di template is missing %q", want)
		}
	}
	if strings.Contains(string(graph), "func NewScheduler") {
		t.Error("loom di template defines NewScheduler; the provider must live in cron_provider.go")
	}
}

// TestCronProviderSeamIsOwnedByCron guards provider ownership: cron ships the
// stable internal/di/cron_provider.go seam (guarded on loom), and loom never
// mirrors it.
func TestCronProviderSeamIsOwnedByCron(t *testing.T) {
	cronTemplate, err := fs.ReadFile(FS(), "capabilities/cron/files/cron_provider.go.tmpl")
	if err != nil {
		t.Fatalf("read cron provider template: %v", err)
	}
	for _, want := range []string{
		"func NewScheduler(lc *loom.Lifecycle, cfg *config.Config, logger *slog.Logger, server *Server) (*cron.Scheduler, error)",
		"__module__/internal/cron",
		"loom.Hook",
	} {
		if !strings.Contains(string(cronTemplate), want) {
			t.Errorf("cron_provider.go template is missing %q:\n%s", want, cronTemplate)
		}
	}
	loom := readDescriptor(t, "loom")
	for _, file := range loom.Files {
		if file.Path == "internal/di/cron_provider.go" {
			t.Error("loom mirrors cron_provider.go; the cron capability owns it")
		}
	}
}

// TestMailCapabilityRequiresLoomWithoutHTTP guards the mail capability's
// contract: it adopts Loom (which brings config) but stays independent of the
// HTTP surface, ships the typed config extension and the sender package, and
// declares its Loom provider seam guarded on loom.
func TestMailCapabilityRequiresLoomWithoutHTTP(t *testing.T) {
	d := readDescriptor(t, "mail")
	if d.Kind != "add" {
		t.Fatalf("mail kind = %q, want add", d.Kind)
	}
	if strings.Join(d.Requires, ",") != "loom" {
		t.Fatalf("mail requires = %v, want [loom]", d.Requires)
	}
	for _, forbidden := range []string{"http", "web", "api", "db"} {
		for _, required := range d.Requires {
			if required == forbidden {
				t.Errorf("mail requires %q", forbidden)
			}
		}
	}
	paths := map[string]bool{}
	for _, file := range d.Files {
		paths[file.Path] = true
	}
	for _, want := range []string{
		"internal/config/mail.go",
		"internal/config/mail_test.go",
		"internal/mailsender/mailsender.go",
		"internal/mailsender/mailsender_test.go",
		"internal/mailsender/README.md",
		"internal/di/mail_provider.go",
	} {
		if !paths[want] {
			t.Errorf("mail capability does not ship %s", want)
		}
	}
	var providerSeam bool
	for _, file := range d.Files {
		if file.Path == "internal/di/mail_provider.go" {
			providerSeam = len(file.When) == 1 && file.When[0] == "loom" && len(file.WhenAbsent) == 0
		}
	}
	if !providerSeam {
		t.Errorf("mail mail_provider.go is not guarded by when: [loom]: %+v", d.Files)
	}
	markers := map[string]bool{}
	for _, patch := range d.Patches {
		if patch.Path == "config.yml" && patch.Marker == "config" && patch.Bootstrap != "config.example.yml" {
			t.Errorf("mail config.yml patch bootstrap = %q, want config.example.yml", patch.Bootstrap)
		}
		if patch.Path == "internal/config/config.go" {
			markers[patch.Marker] = true
		}
	}
	for _, want := range []string{"configfields", "configenv", "configdefaults"} {
		if !markers[want] {
			t.Errorf("mail does not patch the config.go %s extension point: %+v", want, d.Patches)
		}
	}

	// Loom declares the provider; the graph prunes it until a consumer asks.
	graph, err := fs.ReadFile(FS(), "capabilities/loom/files/di.go.tmpl")
	if err != nil {
		t.Fatalf("read loom di template: %v", err)
	}
	if !strings.Contains(string(graph), "loom.Provide(NewMailSender)") {
		t.Error("loom di template does not declare NewMailSender")
	}
	if strings.Contains(string(graph), "func NewMailSender") {
		t.Error("loom di template defines NewMailSender; the provider must live in mail_provider.go")
	}
}

// TestMailProviderSeamIsOwnedByMail guards provider ownership: mail ships the
// stable internal/di/mail_provider.go seam (guarded on loom), and loom never
// mirrors it.
func TestMailProviderSeamIsOwnedByMail(t *testing.T) {
	mailTemplate, err := fs.ReadFile(FS(), "capabilities/mail/files/mail_provider.go.tmpl")
	if err != nil {
		t.Fatalf("read mail provider template: %v", err)
	}
	for _, want := range []string{
		"func NewMailSender(cfg *config.Config) (*mailsender.Sender, error)",
		"__module__/internal/mailsender",
	} {
		if !strings.Contains(string(mailTemplate), want) {
			t.Errorf("mail_provider.go template is missing %q:\n%s", want, mailTemplate)
		}
	}
	loom := readDescriptor(t, "loom")
	for _, file := range loom.Files {
		if file.Path == "internal/di/mail_provider.go" {
			t.Error("loom mirrors mail_provider.go; the mail capability owns it")
		}
	}
}

// TestStorageProviderSeamIsOwnedByStorage guards provider ownership: storage
// ships the stable internal/di/storage_provider.go seam (guarded on loom), and
// loom never mirrors it.
func TestStorageProviderSeamIsOwnedByStorage(t *testing.T) {
	storageTemplate, err := fs.ReadFile(FS(), "capabilities/storage/files/storage_provider.go.tmpl")
	if err != nil {
		t.Fatalf("read storage provider template: %v", err)
	}
	for _, want := range []string{
		"func NewObjectStore(cfg *config.Config) (*objectstore.Store, loom.Cleanup, error)",
		"__module__/internal/objectstore",
	} {
		if !strings.Contains(string(storageTemplate), want) {
			t.Errorf("storage_provider.go template is missing %q:\n%s", want, storageTemplate)
		}
	}
	loom := readDescriptor(t, "loom")
	for _, file := range loom.Files {
		if file.Path == "internal/di/storage_provider.go" {
			t.Error("loom mirrors storage_provider.go; the storage capability owns it")
		}
	}
}

// TestLoomDeclaresAuxiliaryBindings guards the loom-side graph references that
// make the mail/storage/redis/cron integrations work: the graph declares each
// binding, while the owning capability ships the provider seam and loom ships no
// provider mirror.
func TestLoomDeclaresAuxiliaryBindings(t *testing.T) {
	d := readDescriptor(t, "loom")
	for _, file := range d.Files {
		if strings.HasPrefix(file.Path, "internal/di/") && strings.HasSuffix(file.Path, "_provider.go") {
			t.Errorf("loom ships the provider mirror %q; the owning capability ships it", file.Path)
		}
	}
	graph, err := fs.ReadFile(FS(), "capabilities/loom/files/di.go.tmpl")
	if err != nil {
		t.Fatalf("read loom graph: %v", err)
	}
	graphText := string(graph)
	for name, binding := range map[string]string{
		"redis":   "loom.Provide(NewRedisClient)",
		"mail":    "loom.Provide(NewMailSender)",
		"storage": "loom.Provide(NewObjectStore)",
		"cron":    "loom.Provide(NewScheduler)",
	} {
		if !strings.Contains(graphText, binding) {
			t.Errorf("loom graph does not declare the %s binding %q", name, binding)
		}
	}
}

// TestConfigCapabilityShipsNoRuntimeSeam guards the single-mode contract: the
// generic config runtime seam is gone, so config is pure configuration and the
// long-running scheduler is wired only through the Loom cron provider.
func TestConfigCapabilityShipsNoRuntimeSeam(t *testing.T) {
	d := readDescriptor(t, "config")
	for _, file := range d.Files {
		if strings.HasPrefix(file.Path, "internal/app/") || strings.HasSuffix(file.Path, "runtime.go") {
			t.Errorf("config ships the removed runtime payload %q", file.Path)
		}
	}
	if _, err := fs.Stat(FS(), "capabilities/config/files/runtime.go.tmpl"); err == nil {
		t.Error("config still ships the removed runtime.go.tmpl seam")
	}
}

// TestLoomGraphIsConditionalOnHTTP guards the capability-aware graph: without
// http the graph declares commonModule only, so a short command builds nothing
// long-running; with http it adds appGraph, the server and InitApp. Both
// configurations render to gofmt-clean Go.
func TestLoomGraphIsConditionalOnHTTP(t *testing.T) {
	body, err := fs.ReadFile(FS(), "capabilities/loom/files/di.go.tmpl")
	if err != nil {
		t.Fatalf("read di template: %v", err)
	}
	render := func(caps commandTestCaps) string {
		t.Helper()
		tmpl, err := template.New("di.go").Option("missingkey=error").Parse(string(body))
		if err != nil {
			t.Fatalf("parse di template: %v", err)
		}
		var buf bytes.Buffer
		if err := tmpl.Execute(&buf, struct {
			Name    string
			Caps    commandTestCaps
			Modules []struct{ Name string }
		}{Name: "demo", Caps: caps}); err != nil {
			t.Fatalf("render di template: %v", err)
		}
		out := []byte(strings.ReplaceAll(buf.String(), "__module__", "example.com/demo"))
		if _, err := format.Source(out); err != nil {
			t.Fatalf("graph for caps %v does not render to valid Go: %v\n%s", caps, err, out)
		}
		return string(out)
	}

	cli := render(commandTestCaps{})
	if !strings.Contains(cli, "commonModule") {
		t.Error("the http-less graph does not declare commonModule")
	}
	for _, forbidden := range []string{"var appGraph = loom.Graph", "loom.Name(\"InitApp\")", "func NewServer(", `nethttp "net/http"`} {
		if strings.Contains(cli, forbidden) {
			t.Errorf("the http-less graph contains %q; the server graph must be conditional on http", forbidden)
		}
	}

	server := render(commandTestCaps{"http": true})
	for _, want := range []string{"var appGraph = loom.Graph", "loom.Name(\"InitApp\")", "func NewServer(", `nethttp "net/http"`} {
		if !strings.Contains(server, want) {
			t.Errorf("the http graph is missing %q", want)
		}
	}
}

// TestModuleTemplateIsWellFormed guards the per-name `weld add module` payload:
// every declared source exists, every target carries the __modname__ token so the
// per-module substitution produces a unique path, every Go payload renders to
// gofmt-clean Go for a sample module name, and the removed plain route seam is
// gone.
func TestModuleTemplateIsWellFormed(t *testing.T) {
	fsys := FS()
	raw, err := fs.ReadFile(fsys, "modules/module.json")
	if err != nil {
		t.Fatalf("read modules/module.json: %v", err)
	}
	var d struct {
		Version string `json:"version"`
		Files   []struct {
			Path   string `json:"path"`
			Source string `json:"source"`
		} `json:"files"`
	}
	if err := json.Unmarshal(raw, &d); err != nil {
		t.Fatalf("invalid modules/module.json: %v", err)
	}
	if d.Version == "" {
		t.Fatal("module.json needs a version")
	}
	if len(d.Files) == 0 {
		t.Fatal("module.json declares no files")
	}

	// The plain route seam was removed: the Loom server graph registers every
	// module from internal/di, so module.json must not declare it.
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatalf("invalid modules/module.json: %v", err)
	}
	for _, removed := range []string{"route", "routeSnippet"} {
		if _, ok := fields[removed]; ok {
			t.Errorf("modules/module.json still declares the removed %q seam", removed)
		}
	}

	// sources maps each declared payload to its target path. A target path must
	// carry the module-name token: that substitution is what makes a per-module
	// path unique.
	sources := map[string]string{}
	for _, file := range d.Files {
		if !strings.Contains(file.Path, "__modname__") {
			t.Errorf("module file path %q does not carry __modname__", file.Path)
		}
		sources[file.Source] = file.Path
	}
	for source := range sources {
		if _, err := fs.Stat(fsys, path.Join("modules", source)); err != nil {
			t.Errorf("module template payload %q missing: %v", source, err)
		}
	}

	// A Go payload must be gofmt-clean once the placeholders are substituted, so
	// a generated project builds without a formatting pass.
	replacer := strings.NewReplacer(
		"__name__", "demo",
		"__module__", "example.com/demo",
		"__version__", d.Version,
		"__modname__", "widget",
		"__ModName__", "Widget",
	)
	for source := range sources {
		if !strings.HasSuffix(source, ".go.tmpl") {
			continue
		}
		body, err := fs.ReadFile(fsys, path.Join("modules", source))
		if err != nil {
			t.Fatalf("read %s: %v", source, err)
		}
		rendered := []byte(replacer.Replace(string(body)))
		if _, err := format.Source(rendered); err != nil {
			t.Errorf("%s does not render to valid Go: %v\n%s", source, err, rendered)
		}
	}
}

// TestModuleShipsNoPlainRouteTemplate guards the single-mode module contract:
// the plain route template is gone (the Loom server graph registers
// module.NewService), while service.go keeps the always zero-argument
// NewDemoService fallback so a user who changes NewService's signature for the
// graph does not break the module's own tests.
func TestModuleShipsNoPlainRouteTemplate(t *testing.T) {
	fsys := FS()
	for _, removed := range []string{"modules/files/route.go.tmpl", "modules/files/routes.snippet"} {
		if _, err := fs.Stat(fsys, removed); err == nil {
			t.Errorf("modules still ships the removed route payload %q", removed)
		}
	}

	service, err := fs.ReadFile(fsys, "modules/files/service.go.tmpl")
	if err != nil {
		t.Fatalf("read service template: %v", err)
	}
	serviceText := string(service)
	if !strings.Contains(serviceText, "func NewService() Service {\n\treturn NewDemoService()\n}") {
		t.Errorf("NewService does not delegate to the NewDemoService fallback:\n%s", serviceText)
	}
	if !strings.Contains(serviceText, "func NewDemoService() Service {") {
		t.Errorf("the service template is missing the NewDemoService fallback:\n%s", serviceText)
	}

	// The Loom server graph, not a route seam, registers the module service.
	graph, err := fs.ReadFile(fsys, "capabilities/loom/files/di.go.tmpl")
	if err != nil {
		t.Fatalf("read loom graph: %v", err)
	}
	graphText := string(graph)
	if !strings.Contains(graphText, "loom.Provide({{.Name}}.NewService)") ||
		!strings.Contains(graphText, "{{.Name}}.Register(mux, {{.Name}}Service)") {
		t.Error("the Loom graph does not register the module service on the mux")
	}
}

// TestCommandTemplateIsWellFormed guards the per-name `weld add command` and
// `weld add module --command` payloads: every declared source exists, every
// target carries the __modname__ token so the per-command substitution produces
// a unique path, the two variants declare the same targets (a name is one
// command group, never two), and every Go payload renders to gofmt-clean Go.
// commandTestCaps is the capability set a command payload renders against.
type commandTestCaps map[string]bool

func (c commandTestCaps) Has(name string) bool { return c[name] }

// commandTestVars mirrors the weld CLI's CommandTemplateVars: the fields a
// command payload's text/template actions may reference.
type commandTestVars struct {
	Name     string
	Module   string
	Version  string
	Caps     commandTestCaps
	Mod      string
	ModTitle string
}

// renderCommandPayload expands a command payload as a text/template, substitutes
// the weld placeholder tokens, and formats a Go target, mirroring the CLI.
func renderCommandPayload(t *testing.T, fsys fs.FS, source, target string, vars commandTestVars) []byte {
	t.Helper()
	body, err := fs.ReadFile(fsys, path.Join("commands", source))
	if err != nil {
		t.Fatalf("read %s: %v", source, err)
	}
	tmpl, err := template.New(source).Option("missingkey=error").Parse(string(body))
	if err != nil {
		t.Fatalf("parse %s: %v", source, err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, vars); err != nil {
		t.Fatalf("render %s: %v", source, err)
	}
	rendered := []byte(replaceTokens(buf.String(), vars.Name, vars.Module, vars.Version))
	if !strings.HasSuffix(target, ".go") {
		return rendered
	}
	formatted, err := format.Source(rendered)
	if err != nil {
		t.Fatalf("%s (%s) does not render to valid Go: %v\n%s", source, target, err, rendered)
	}
	return formatted
}

// replaceTokens substitutes the weld placeholder tokens a command payload
// carries, exactly as the weld CLI does.
func replaceTokens(text, name, module, version string) string {
	return strings.NewReplacer(
		"__name__", name,
		"__module__", module,
		"__version__", version,
		"__modname__", "widget",
		"__ModName__", "Widget",
	).Replace(text)
}

// TestCommandTemplateIsWellFormed guards the per-name `weld add command` and
// `weld add module --command` payloads: every declared source exists, every
// target carries the __modname__ token so the per-command substitution produces
// a unique path, the two variants declare the same targets (a name is one
// command group, never two), the Loom graph is guarded on loom in both, and
// every Go payload renders to gofmt-clean Go for both a plain and a Loom
// project.
func TestCommandTemplateIsWellFormed(t *testing.T) {
	fsys := FS()
	raw, err := fs.ReadFile(fsys, "commands/command.json")
	if err != nil {
		t.Fatalf("read commands/command.json: %v", err)
	}
	var d struct {
		Version string `json:"version"`
		Generic struct {
			Files []struct {
				Path   string   `json:"path"`
				Source string   `json:"source"`
				When   []string `json:"when"`
			} `json:"files"`
		} `json:"generic"`
		Module struct {
			Files []struct {
				Path   string   `json:"path"`
				Source string   `json:"source"`
				When   []string `json:"when"`
			} `json:"files"`
		} `json:"module"`
	}
	if err := json.Unmarshal(raw, &d); err != nil {
		t.Fatalf("invalid commands/command.json: %v", err)
	}
	if d.Version == "" {
		t.Fatal("command.json needs a version")
	}
	if len(d.Generic.Files) == 0 || len(d.Module.Files) == 0 {
		t.Fatal("command.json needs both generic and module files")
	}

	const graphTarget = "internal/di/__modname___graph.go"
	targetsByVariant := map[string]map[string]bool{}
	capsets := map[string]commandTestCaps{
		"plain":         {},
		"loom":          {"loom": true},
		"loom-with-all": {"loom": true, "db": true, "redis": true, "mail": true, "storage": true},
	}
	for _, variant := range []struct {
		name  string
		files []struct {
			Path   string   `json:"path"`
			Source string   `json:"source"`
			When   []string `json:"when"`
		}
	}{{"generic", d.Generic.Files}, {"module", d.Module.Files}} {
		targets := map[string]bool{}
		targetsByVariant[variant.name] = targets
		var graphWhen []string
		for _, file := range variant.files {
			if !strings.Contains(file.Path, "__modname__") {
				t.Errorf("%s file path %q does not carry __modname__", variant.name, file.Path)
			}
			targets[file.Path] = true
			if file.Path == graphTarget {
				graphWhen = file.When
			}
			if _, err := fs.Stat(fsys, path.Join("commands", file.Source)); err != nil {
				t.Errorf("%s payload %q missing: %v", variant.name, file.Source, err)
				continue
			}
			if !strings.HasSuffix(file.Source, ".go.tmpl") && !strings.HasSuffix(file.Source, ".md.tmpl") {
				continue
			}
			for _, caps := range capsets {
				applies := true
				for _, name := range file.When {
					if !caps[name] {
						applies = false
					}
				}
				if !applies {
					continue
				}
				vars := commandTestVars{Name: "demo", Module: "example.com/demo", Version: d.Version, Caps: caps, Mod: "widget", ModTitle: "Widget"}
				renderCommandPayload(t, fsys, file.Source, file.Path, vars)
			}
		}
		if len(graphWhen) != 1 || graphWhen[0] != "loom" {
			t.Errorf("%s graph is not guarded by when: [loom]: %v", variant.name, graphWhen)
		}
	}
	// The two variants must write the same target set, so a name is a single
	// command group regardless of how it was created.
	for target := range targetsByVariant["generic"] {
		if !targetsByVariant["module"][target] {
			t.Errorf("module variant does not write %q", target)
		}
	}
	for target := range targetsByVariant["module"] {
		if !targetsByVariant["generic"][target] {
			t.Errorf("generic variant does not write %q", target)
		}
	}
}

// TestCommandGraphIsLoomAware guards the Loom command seam: the graph includes
// the shared commonModule and its own root, never snapshots the individual
// shared providers (so a capability installed later still reaches the command
// through the regenerated module), and never references the HTTP server or the
// scheduler. It also proves the rendered graph is capability independent, so a
// `weld add db/redis/mail/storage` after the command never has to rewrite the
// stable command graph. The generated command package never imports the Loom
// runtime.
func TestCommandGraphIsLoomAware(t *testing.T) {
	fsys := FS()
	caps := commandTestCaps{"loom": true, "db": true, "redis": true, "mail": true, "storage": true}
	vars := commandTestVars{Name: "demo", Module: "example.com/demo", Version: "0.2.0", Caps: caps, Mod: "widget", ModTitle: "Widget"}
	for _, source := range []string{"files/generic_graph.go.tmpl", "files/module_graph.go.tmpl"} {
		graph := string(renderCommandPayload(t, fsys, source, "internal/di/widget_graph.go", vars))
		for _, want := range []string{"loom.WithContext()", "commonModule"} {
			if !strings.Contains(graph, want) {
				t.Errorf("%s graph is missing %q:\n%s", source, want, graph)
			}
		}
		// The shared providers live in commonModule in di.go, not in the stable
		// command graph: snapshotting them here would make a capability installed
		// later unavailable to the command forever.
		for _, forbidden := range []string{
			"InitApp", "NewServer", "NewScheduler", "Scheduler",
			"NewConfigLoader", "NewConfig)", "NewLogger",
			"NewPool", "NewRepository", "data.Repository",
			"NewRedisClient", "NewMailSender", "NewObjectStore",
		} {
			if strings.Contains(graph, forbidden) {
				t.Errorf("%s graph snapshots the shared provider %q; it belongs to commonModule in di.go:\n%s", source, forbidden, graph)
			}
		}
	}
	// The two variants keep their own root, the only part that legitimately
	// differs.
	generic := string(renderCommandPayload(t, fsys, "files/generic_graph.go.tmpl", "internal/di/widget_graph.go", vars))
	if !strings.Contains(generic, "loom.Provide(widget.NewDeps)") {
		t.Errorf("the generic graph does not declare the command root:\n%s", generic)
	}
	module := string(renderCommandPayload(t, fsys, "files/module_graph.go.tmpl", "internal/di/widget_graph.go", vars))
	for _, want := range []string{"loom.Provide(module.NewService)", "loom.Provide(command.NewDeps)"} {
		if !strings.Contains(module, want) {
			t.Errorf("the module graph is missing %q:\n%s", want, module)
		}
	}

	// The graph must render identically whether or not the optional capabilities
	// are installed: that is what lets `weld add db/redis/mail/storage` propagate
	// through commonModule instead of rewriting the stable command graph.
	loomOnly := commandTestVars{Name: "demo", Module: "example.com/demo", Version: "0.2.0", Caps: commandTestCaps{"loom": true}, Mod: "widget", ModTitle: "Widget"}
	for _, source := range []string{"files/generic_graph.go.tmpl", "files/module_graph.go.tmpl"} {
		before := string(renderCommandPayload(t, fsys, source, "internal/di/widget_graph.go", loomOnly))
		after := string(renderCommandPayload(t, fsys, source, "internal/di/widget_graph.go", vars))
		if before != after {
			t.Errorf("%s depends on the installed capability set; a later `weld add` would have to rewrite the stable command graph:\n--- loom only ---\n%s\n--- with all ---\n%s", source, before, after)
		}
	}

	// The generated command package must not import the Loom runtime.
	for _, source := range []string{"files/generic_command.go.tmpl", "files/module_command.go.tmpl"} {
		body, err := fs.ReadFile(fsys, path.Join("commands", source))
		if err != nil {
			t.Fatalf("read %s: %v", source, err)
		}
		if strings.Contains(string(body), "github.com/Xwudao/loom") {
			t.Errorf("%s imports the Loom runtime; the command package must stay decoupled", source)
		}
	}
}
