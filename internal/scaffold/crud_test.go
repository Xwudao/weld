package scaffold

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Xwudao/weld/internal/project"
)

// TestParseCrudFields pins the field grammar: the default field, the type
// aliases, sqlc's field spelling and the placeholder positions.
func TestParseCrudFields(t *testing.T) {
	t.Parallel()
	fields, err := ParseCrudFields(nil)
	if err != nil || len(fields) != 1 || fields[0].Column != "name" || fields[0].Kind != "string" {
		t.Fatalf("default fields = %+v, %v; want name:string", fields, err)
	}

	fields, err = ParseCrudFields([]string{"title:text", "owner_id:int64", "price:float", "seen_at:timestamp", "active:bool"})
	if err != nil {
		t.Fatalf("ParseCrudFields: %v", err)
	}
	want := []struct{ column, goName, kind, sqlType, goType string }{
		{"title", "Title", "string", "text", "string"},
		{"owner_id", "OwnerID", "int", "bigint", "int64"},
		{"price", "Price", "float", "double precision", "float64"},
		{"seen_at", "SeenAt", "time", "timestamptz", "time.Time"},
		{"active", "Active", "bool", "boolean", "bool"},
	}
	for i, w := range want {
		got := fields[i]
		if got.Column != w.column || got.GoName != w.goName || got.Kind != w.kind || got.SQLType != w.sqlType || got.GoType != w.goType {
			t.Errorf("field %d = %+v, want %+v", i, got, w)
		}
		if got.CreateParam != i+1 || got.UpdateParam != i+2 {
			t.Errorf("field %d params = $%d/$%d, want $%d/$%d", i, got.CreateParam, got.UpdateParam, i+1, i+2)
		}
	}

	for spec, wantErr := range map[string]string{
		"name":            "use name:type",
		"name:uuid":       `unknown type "uuid"`,
		"Name:string":     "lower_snake_case",
		"first__name:int": "lower_snake_case",
		"id:int":          "generated id",
		"created_at:time": "generated created_at",
		"validate:bool":   "Validate method",
		"order:int":       "PostgreSQL reserved word",
	} {
		if _, err := ParseCrudFields([]string{spec}); err == nil || !strings.Contains(err.Error(), wantErr) {
			t.Errorf("ParseCrudFields(%q) = %v, want an error containing %q", spec, err, wantErr)
		}
	}
	if _, err := ParseCrudFields([]string{"a:int", "a:bool"}); err == nil || !strings.Contains(err.Error(), "declared twice") {
		t.Errorf("duplicate field = %v, want a duplicate error", err)
	}
}

// TestPluralize pins the table names whose singular sqlc maps back to the
// resource, which is what the hand-written repository refers to.
func TestPluralize(t *testing.T) {
	t.Parallel()
	for name, want := range map[string]string{
		"widget": "widgets", "category": "categories", "key": "keys", "box": "boxes",
		"status": "statuses", "match": "matches", "wish": "wishes", "quiz": "quizes",
	} {
		if got := pluralize(name); got != want {
			t.Errorf("pluralize(%q) = %q, want %q", name, got, want)
		}
	}
}

// TestAddCrudRequiresHTTPAndDB proves the requirements are never installed
// silently, and that --with-dependencies plans the whole chain once.
func TestAddCrudRequiresHTTPAndDB(t *testing.T) {
	t.Parallel()
	dir := create(t, t.TempDir())
	var missing *MissingRequirementsError
	if _, err := AddCrud(Request{Dir: dir, Catalog: newCatalog()}, "widget", nil); !errors.As(err, &missing) {
		t.Fatalf("AddCrud without requirements = %v, want MissingRequirementsError", err)
	}
	if got := strings.Join(missing.Missing, ","); got != "config,loom,http,db" {
		t.Fatalf("missing = %q, want config,loom,http,db", got)
	}

	result, err := AddCrud(Request{Dir: dir, Catalog: newCatalog(), WithDependencies: true}, "widget", nil)
	if err != nil {
		t.Fatalf("AddCrud --with-dependencies: %v", err)
	}
	if got := strings.Join(result.Installed, ","); got != "config,loom,http,db" {
		t.Fatalf("installed = %q, want config,loom,http,db", got)
	}
}

// TestAddCrudPlanWritesNothing proves planning is pure — the truthful dry run —
// and that the plan asks Apply for sqlc, Loom and a build check.
func TestAddCrudPlanWritesNothing(t *testing.T) {
	t.Parallel()
	dir := create(t, t.TempDir())
	add(t, dir, "http", "db")
	before := snapshotTree(t, dir)

	result, err := AddCrud(Request{Dir: dir, Catalog: newCatalog()}, "widget", []string{"title:string", "stock:int"})
	if err != nil {
		t.Fatalf("AddCrud: %v", err)
	}
	if !result.GenerateSQLC || !result.GenerateLoom || strings.Join(result.Verify, " ") != "build ./..." {
		t.Fatalf("result = %+v, want sqlc, Loom and a build check", result)
	}
	if strings.Join(result.SQLCOutputs, ",") != "internal/data/sqlc/widgets.sql.go" {
		t.Fatalf("sqlc outputs = %v", result.SQLCOutputs)
	}
	planned := map[string]bool{}
	for _, operation := range result.Operations {
		planned[migrationVersion.ReplaceAllString(operation.Path, "V_")] = true
	}
	for _, path := range []string{
		"db/migrations/V_create_widgets.sql",
		"db/query/widgets.sql",
		"internal/data/widget_repo.go",
		"internal/modules/widget/module.go",
		"internal/modules/widget/memory.go",
		"internal/di/di.go",
		project.ManifestName,
	} {
		if !planned[path] {
			t.Errorf("plan is missing %s", path)
		}
	}
	after := snapshotTree(t, dir)
	if len(after) != len(before) {
		t.Fatalf("planning changed the file set: %d -> %d files", len(before), len(after))
	}
	for path, content := range before {
		if after[path] != content {
			t.Errorf("planning changed %s", path)
		}
	}
}

// TestAddCrudRejectsConflicts proves a resource never takes over a name or a
// table something else owns.
func TestAddCrudRejectsConflicts(t *testing.T) {
	t.Parallel()
	dir := create(t, t.TempDir())
	add(t, dir, "http", "db")
	moduleResult, err := AddModule(Request{Dir: dir, Catalog: newCatalog()}, "widget")
	if err != nil {
		t.Fatalf("AddModule: %v", err)
	}
	if err := moduleResult.Apply(); err != nil {
		t.Fatalf("Apply module: %v", err)
	}

	for name, wantErr := range map[string]string{
		"widget":  "already installed",
		"item":    "items table",
		"user":    "users table",
		"Widget":  "invalid module name",
		"package": "Go keyword",
	} {
		if _, err := AddCrud(Request{Dir: dir, Catalog: newCatalog()}, name, nil); err == nil || !strings.Contains(err.Error(), wantErr) {
			t.Errorf("AddCrud(%q) = %v, want an error containing %q", name, err, wantErr)
		}
	}

	// A table that already has a query file is the project's, not weld's.
	if err := os.WriteFile(filepath.Join(dir, "db", "query", "gadgets.sql"), []byte("-- mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var conflict *project.ConflictError
	if _, err := AddCrud(Request{Dir: dir, Catalog: newCatalog()}, "gadget", nil); !errors.As(err, &conflict) || conflict.Path != "db/query/gadgets.sql" {
		t.Fatalf("AddCrud over an existing query file = %v, want a conflict on it", err)
	}
}

// TestAddCrudGeneratesAWorkingResource is the end-to-end contract: the files,
// the sqlc output, the regenerated graph and the manifest record, and a project
// that is gofmt-clean, builds and passes its own (database-free) tests.
func TestAddCrudGeneratesAWorkingResource(t *testing.T) {
	t.Parallel()
	dir := create(t, t.TempDir())
	add(t, dir, "http", "db")
	if !runDBTool(t, dir, "go", "tool", "sqlc", "version") {
		t.Skip("cannot run the pinned sqlc tool (network/module cache unavailable)")
	}

	result, err := AddCrud(Request{Dir: dir, Catalog: newCatalog()}, "product",
		[]string{"title:string", "price:float", "stock:int", "active:bool", "released_at:time", "owner_id:int"})
	if err != nil {
		t.Fatalf("AddCrud: %v", err)
	}
	if err := result.Apply(); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	models := readFile(t, filepath.Join(dir, "internal", "data", "sqlc", "models.go"))
	if !strings.Contains(models, "type Product struct") || !strings.Contains(models, "OwnerID") {
		t.Errorf("sqlc did not generate the Product model:\n%s", models)
	}
	if _, err := os.Stat(filepath.Join(dir, "internal", "data", "sqlc", "products.sql.go")); err != nil {
		t.Errorf("sqlc did not generate products.sql.go: %v", err)
	}
	if di := readFile(t, filepath.Join(dir, "internal", "di", "di.go")); !strings.Contains(di, "loom.Provide(product.NewService)") {
		t.Errorf("di.go does not bind the module:\n%s", di)
	}
	migration := readFile(t, migrationFile(t, dir, "create_products"))
	for _, want := range []string{"CREATE TABLE products", "released_at timestamptz NOT NULL", "DROP TABLE products"} {
		if !strings.Contains(migration, want) {
			t.Errorf("migration is missing %q:\n%s", want, migration)
		}
	}

	manifest := mustLoad(t, dir)
	if !manifest.HasModule("product") {
		t.Fatal("the resource is not recorded as a module")
	}
	// The generator's own output is the only drift: every file weld wrote still
	// matches its record.
	for _, item := range manifest.Drift(dir) {
		if !strings.HasPrefix(item.Path, sqlcOutputDir+"/") {
			t.Errorf("unexpected drift after add crud: %+v", item)
		}
	}

	if !buildAndTestGeneratedProject(t, dir) {
		t.Skip("cannot resolve the generated project's dependencies (network/module cache unavailable)")
	}
}

// TestAddCrudSingleFieldResource covers the one-parameter INSERT, for which sqlc
// generates a plain argument instead of a Params struct.
func TestAddCrudSingleFieldResource(t *testing.T) {
	t.Parallel()
	dir := create(t, t.TempDir())
	add(t, dir, "http", "db")
	if !runDBTool(t, dir, "go", "tool", "sqlc", "version") {
		t.Skip("cannot run the pinned sqlc tool (network/module cache unavailable)")
	}
	result, err := AddCrud(Request{Dir: dir, Catalog: newCatalog()}, "event", []string{"happened_at:time"})
	if err != nil {
		t.Fatalf("AddCrud: %v", err)
	}
	if err := result.Apply(); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	goTest(t, dir, "./internal/...")
}

// TestAddCrudRollsBackWhenGenerationFails proves a failed sqlc run undoes the
// whole plan: no resource file survives and every rewritten file is restored.
func TestAddCrudRollsBackWhenGenerationFails(t *testing.T) {
	t.Parallel()
	dir := create(t, t.TempDir())
	add(t, dir, "http", "db")
	before := snapshotTree(t, dir)

	result, err := AddCrud(Request{Dir: dir, Catalog: newCatalog()}, "widget", nil)
	if err != nil {
		t.Fatalf("AddCrud: %v", err)
	}
	for i, operation := range result.Operations {
		if operation.Path == "db/query/widgets.sql" {
			result.Operations[i].Content = []byte("-- name: Broken :one\nSELECT FROM WHERE;\n")
		}
	}
	if err := result.Apply(); err == nil || !strings.Contains(err.Error(), "rolled back") {
		t.Fatalf("Apply with broken SQL = %v, want a rolled-back failure", err)
	}

	after := snapshotTree(t, dir)
	for path, content := range before {
		if after[path] != content {
			t.Errorf("%s was not restored", path)
		}
	}
	for path := range after {
		if _, ok := before[path]; !ok {
			t.Errorf("rollback left %s behind", path)
		}
	}
}
