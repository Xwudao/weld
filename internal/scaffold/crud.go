package scaffold

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Xwudao/weld/internal/db"
	"github.com/Xwudao/weld/internal/project"
	"github.com/Xwudao/weld/internal/template"
)

// sqlcOutputDir is where the project's single sqlc.yaml writes generated code.
const sqlcOutputDir = "internal/data/sqlc"

// crudColumnPattern matches a CRUD field name: a lower_snake_case column that is
// also the JSON and query name, with no empty segment.
var crudColumnPattern = regexp.MustCompile(`^[a-z][a-z0-9]*(_[a-z0-9]+)*$`)

// crudKinds maps a declared field type to its column and Go types. The set is
// deliberately small and NOT NULL only: each kind maps to one sqlc type with no
// pgtype wrapper beyond timestamptz, so the generated code stays readable.
var crudKinds = map[string]struct{ sqlType, goType string }{
	"string": {"text", "string"},
	"int":    {"bigint", "int64"},
	"bool":   {"boolean", "bool"},
	"float":  {"double precision", "float64"},
	"time":   {"timestamptz", "time.Time"},
}

// crudKindAliases accepts the spellings people reach for first.
var crudKindAliases = map[string]string{"text": "string", "int64": "int", "float64": "float", "timestamp": "time"}

// reservedCrudColumns are the columns every resource already has, or names
// that would collide with a generated method (a field Validate would shadow
// the DTO's Validate).
var reservedCrudColumns = map[string]string{
	"id":         "every resource has a generated id",
	"created_at": "every resource has a generated created_at",
	"updated_at": "every resource has a generated updated_at",
	"validate":   "it would collide with the request's Validate method",
}

// postgresReserved are PostgreSQL's reserved key words. A column with one of
// these names would need quoting in every statement, so it is rejected.
var postgresReserved = map[string]bool{}

func init() {
	for _, word := range strings.Fields(`all analyse analyze and any array as asc asymmetric both case cast check
		collate column constraint create current_catalog current_date current_role current_time
		current_timestamp current_user default deferrable desc distinct do else end except false
		fetch for foreign from grant group having in initially intersect into lateral leading limit
		localtime localtimestamp not null offset on only or order placing primary references
		returning select session_user some symmetric system_user table then to trailing true union
		unique user using variadic when where window with`) {
		postgresReserved[word] = true
	}
}

// reservedCrudNames are resource names whose table a capability already owns.
var reservedCrudNames = map[string]string{
	"item": "the db capability ships the items table",
	"user": "the user capability owns the users table",
}

// ParseCrudFields parses `name:type` field specs. No spec means one required
// name:string field, so `weld add crud widget` is a complete resource.
func ParseCrudFields(specs []string) ([]template.CrudField, error) {
	if len(specs) == 0 {
		specs = []string{"name:string"}
	}
	seen := map[string]bool{}
	fields := make([]template.CrudField, 0, len(specs))
	for _, spec := range specs {
		column, kind, ok := strings.Cut(spec, ":")
		if !ok {
			return nil, fmt.Errorf("invalid field %q: use name:type (types: string, int, bool, float, time)", spec)
		}
		if alias, ok := crudKindAliases[kind]; ok {
			kind = alias
		}
		types, ok := crudKinds[kind]
		if !ok {
			return nil, fmt.Errorf("invalid field %q: unknown type %q (types: string, int, bool, float, time)", spec, kind)
		}
		if !crudColumnPattern.MatchString(column) {
			return nil, fmt.Errorf("invalid field %q: the name must be lower_snake_case, starting with a letter", spec)
		}
		if reason, reserved := reservedCrudColumns[column]; reserved {
			return nil, fmt.Errorf("invalid field %q: %s", spec, reason)
		}
		if postgresReserved[column] {
			return nil, fmt.Errorf("invalid field %q: %s is a PostgreSQL reserved word", spec, column)
		}
		if seen[column] {
			return nil, fmt.Errorf("invalid field %q: %s is declared twice", spec, column)
		}
		seen[column] = true
		fields = append(fields, template.CrudField{
			Column:      column,
			GoName:      sqlcFieldName(column),
			Kind:        kind,
			SQLType:     types.sqlType,
			GoType:      types.goType,
			CreateParam: len(fields) + 1,
			UpdateParam: len(fields) + 2,
		})
	}
	return fields, nil
}

// sqlcFieldName spells a column the way sqlc names the generated struct field:
// each underscore-separated part is capitalized and "id" becomes "ID" (sqlc's
// default initialism), so owner_id is OwnerID and the hand-written domain
// types line up with the generated rows.
func sqlcFieldName(column string) string {
	var b strings.Builder
	for _, part := range strings.Split(column, "_") {
		if part == "id" {
			b.WriteString("ID")
			continue
		}
		b.WriteString(strings.ToUpper(part[:1]))
		b.WriteString(part[1:])
	}
	return b.String()
}

// pluralize returns the table name for a resource with the English rules sqlc's
// inflector reverses, so sqlc names the table's model after the resource again
// (widgets -> Widget, categories -> Category, boxes -> Box).
func pluralize(name string) string {
	switch {
	case strings.HasSuffix(name, "y") && len(name) > 1 && !strings.ContainsRune("aeiou", rune(name[len(name)-2])):
		return name[:len(name)-1] + "ies"
	case strings.HasSuffix(name, "s"), strings.HasSuffix(name, "x"), strings.HasSuffix(name, "z"),
		strings.HasSuffix(name, "ch"), strings.HasSuffix(name, "sh"):
		return name + "es"
	default:
		return name + "s"
	}
}

// AddCrud plans a database-backed CRUD resource named name: a goose migration,
// sqlc statements, a repository in internal/data and a business module with
// list/get/create/update/delete routes.
//
// It requires http and db (and what they require); a missing requirement is an
// error unless Request.WithDependencies is set. The resource is a business
// module like any other — recorded under module:<name>, mounted by the
// generated Loom graph and exportable with `weld http export` — whose files are
// written once and owned by the project.
//
// The repository calls sqlc code that does not exist until the generator runs,
// so the plan asks Apply to run the pinned sqlc generator before Loom and then
// `go build ./...`, rolling everything back when either fails. The plan itself
// writes and runs nothing.
func AddCrud(req Request, name string, fieldSpecs []string) (*Result, error) {
	if err := ValidateModuleName(name); err != nil {
		return nil, err
	}
	if reason, reserved := reservedCrudNames[name]; reserved {
		return nil, fmt.Errorf("invalid resource name %q: %s", name, reason)
	}
	fields, err := ParseCrudFields(fieldSpecs)
	if err != nil {
		return nil, err
	}
	manifest, err := project.Load(req.Dir)
	if err != nil {
		return nil, err
	}
	if manifest.HasModule(name) || manifest.HasCommand(name) {
		return nil, &project.ConflictError{
			Path:   "internal/modules/" + name,
			Reason: fmt.Sprintf("a module or command named %q is already installed; choose another resource name", name),
		}
	}
	crud, err := template.LoadCrud()
	if err != nil {
		return nil, err
	}

	var required []*template.Capability
	for _, capabilityName := range []string{"http", "db"} {
		capability, err := req.Catalog.Get(capabilityName)
		if err != nil {
			return nil, err
		}
		required = append(required, capability)
	}
	order, err := requiredCapabilities(req.Catalog, manifest, required...)
	if err != nil {
		return nil, err
	}
	if len(order) > 0 && !req.WithDependencies {
		return nil, &MissingRequirementsError{Subject: fmt.Sprintf("crud resource %q", name), Missing: capabilityNames(order)}
	}

	result := &Result{Dir: req.Dir}
	for _, capability := range order {
		result.Notes = append(result.Notes, fmt.Sprintf("installing required capability %q", capability.Name))
	}
	p := newPlanner(req, manifest, result)
	for _, capability := range order {
		if err := p.installCapability(capability); err != nil {
			return nil, err
		}
	}

	table := pluralize(name)
	vars := template.CrudVars{
		Name:     manifest.Name,
		Module:   manifest.Module,
		Version:  crud.Version,
		Resource: name,
		Title:    template.ExportName(name),
		Table:    table,
		Plural:   template.ExportName(table),
		Fields:   fields,
	}
	owner := "module:" + name
	for _, file := range crud.Files {
		target, err := crud.RenderPath(file, vars)
		if err != nil {
			return nil, err
		}
		content, err := crud.RenderFile(file, vars)
		if err != nil {
			return nil, err
		}
		if file.Versioned {
			if target, err = p.payloadPath(owner, template.File{Path: target, Versioned: true}); err != nil {
				return nil, err
			}
		}
		if err := p.planNewFile(target, owner, "", project.RoleSeam, content); err != nil {
			return nil, err
		}
	}
	manifest.AddModule(project.ModuleRef{Name: name, Version: crud.Version})
	if err := p.finish(); err != nil {
		return nil, err
	}

	result.GenerateSQLC = true
	result.SQLCOutputs = []string{sqlcOutputDir + "/" + table + ".sql.go"}
	result.Verify = []string{"build", "./..."}
	result.Notes = append(result.Notes,
		fmt.Sprintf("crud resource %q: table %s, routes under httpserver.ModulePrefix(%q) (/api/%s by default); writes require the admin policy", name, table, name, name),
		"apply the migration with `weld db migrate up` (or enable database.auto_migrate); serving the module now needs the database settings in config.yml",
		"sqlc regenerated internal/data/sqlc, so `weld list` reports its models.go as changed; that is the generator's output, not an edit")
	return result, nil
}

// runSQLC runs the project's pinned sqlc generator, the same command
// `weld gen sqlc` runs. Its output is captured and returned only on failure, so
// a successful apply prints nothing of the generator's.
func runSQLC(dir string) error {
	var output bytes.Buffer
	_, err := db.SQLC(db.SQLCRequest{
		Dir: dir,
		Runner: func(name, workDir string, env []string, args ...string) error {
			cmd := exec.Command(name, args...)
			cmd.Dir = workDir
			cmd.Env = append(os.Environ(), env...)
			cmd.Stdout = &output
			cmd.Stderr = &output
			return cmd.Run()
		},
	})
	if err != nil {
		return fmt.Errorf("%w\n%s", err, strings.TrimSpace(output.String()))
	}
	return nil
}

// filesIn lists the regular files directly inside dir (relative to root), as
// slash paths. A missing directory has no files.
func filesIn(root, dir string) ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(root, filepath.FromSlash(dir)))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var files []string
	for _, entry := range entries {
		if entry.Type().IsRegular() {
			files = append(files, path.Join(dir, entry.Name()))
		}
	}
	return files, nil
}
