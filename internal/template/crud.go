package template

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/format"
	"io/fs"
	"path"
	"strings"
	"text/template"

	weldtemplate "github.com/Xwudao/weld/internal/weldtemplate"
)

// CrudFile is one payload of the per-resource CRUD template. Its Path is itself
// a text/template over CrudVars, so a resource's table name can appear in it.
type CrudFile struct {
	Path   string `json:"path"`
	Source string `json:"source"`
	// Versioned marks the goose migration: it is written with a version chosen
	// when the resource is added, exactly like a capability's versioned file.
	Versioned bool `json:"versioned,omitempty"`
}

// CrudTemplate is the payload `weld add crud <name>` writes.
//
// Unlike a module payload, which is literal token replacement, every CRUD
// payload is a text/template: the field list drives the SQL, the sqlc calls and
// the DTOs, so the payloads need loops. Go payloads are gofmt'ed after
// rendering, so their conditionals never leave a formatting artifact.
type CrudTemplate struct {
	Version string     `json:"version"`
	Files   []CrudFile `json:"files"`

	fsys fs.FS
}

// CrudField is one user-declared column of a CRUD resource.
type CrudField struct {
	// Column is the lower_snake_case column, JSON and query name.
	Column string
	// GoName is the exported Go field name, spelled the way sqlc spells a
	// column ("owner_id" -> "OwnerID") so the generated rows and the
	// hand-written domain types agree.
	GoName string
	// Kind is the declared type: string, int, bool, float or time.
	Kind string
	// SQLType is the PostgreSQL column type.
	SQLType string
	// GoType is the domain and DTO Go type.
	GoType string
	// CreateParam and UpdateParam are the field's $n placeholders in the
	// INSERT and the UPDATE (whose $1 is the id).
	CreateParam int
	UpdateParam int
}

// IsTime reports whether the field needs the pgtype.Timestamptz conversion.
func (f CrudField) IsTime() bool { return f.Kind == "time" }

// IsString reports whether the field is validated as non-blank.
func (f CrudField) IsString() bool { return f.Kind == "string" }

// CrudVars is the data every CRUD payload is rendered against.
type CrudVars struct {
	// Name is the project name and Module its Go module path.
	Name   string
	Module string
	// Version is the CRUD template version recorded for the resource.
	Version string
	// Resource is the module package name and URL segment ("widget"); Title is
	// its exported form ("Widget").
	Resource string
	Title    string
	// Table is the plural table name ("widgets") and Plural its exported form
	// ("Widgets"), used in query names such as ListWidgets.
	Table  string
	Plural string
	Fields []CrudField
}

// HasTime reports whether any field is a timestamp, which decides the time
// and pgtype imports.
func (v CrudVars) HasTime() bool {
	for _, field := range v.Fields {
		if field.IsTime() {
			return true
		}
	}
	return false
}

// HasString reports whether any field is a string, which decides the strings
// import of the validating DTOs.
func (v CrudVars) HasString() bool {
	for _, field := range v.Fields {
		if field.IsString() {
			return true
		}
	}
	return false
}

// sampleValues are the JSON literals the generated tests send for each kind.
var sampleValues = map[string]string{
	"string": `"sample"`,
	"int":    `7`,
	"bool":   `true`,
	"float":  `1.5`,
	"time":   `"2024-01-02T03:04:05Z"`,
}

// SampleJSON is a valid create/update body for the generated tests.
func (v CrudVars) SampleJSON() string {
	return v.sampleJSON("")
}

// HasRequired reports whether any field has a validation rule (a non-blank
// string or a set timestamp), so the generated tests can exercise a 400.
func (v CrudVars) HasRequired() bool { return v.HasString() || v.HasTime() }

// InvalidJSON is a body that fails validation: the first required field is
// blanked (a string) or omitted (a timestamp). It is only meaningful when
// HasRequired is true.
func (v CrudVars) InvalidJSON() string {
	for _, field := range v.Fields {
		if field.IsString() || field.IsTime() {
			return v.sampleJSON(field.Column)
		}
	}
	return v.SampleJSON()
}

// InvalidMessage is the validation message InvalidJSON provokes.
func (v CrudVars) InvalidMessage() string {
	for _, field := range v.Fields {
		switch {
		case field.IsString():
			return field.Column + " must not be empty"
		case field.IsTime():
			return field.Column + " must be set"
		}
	}
	return ""
}

// sampleJSON renders the sample body, blanking a string column or omitting a
// time column named invalid.
func (v CrudVars) sampleJSON(invalid string) string {
	var parts []string
	for _, field := range v.Fields {
		value := sampleValues[field.Kind]
		if field.Column == invalid {
			if field.IsTime() {
				continue
			}
			value = `"  "`
		}
		parts = append(parts, fmt.Sprintf("%q:%s", field.Column, value))
	}
	return "{" + strings.Join(parts, ",") + "}"
}

// SingleField reports whether the INSERT takes exactly one parameter. sqlc then
// generates a plain argument instead of a Params struct, so the repository has
// to call it differently.
func (v CrudVars) SingleField() bool { return len(v.Fields) == 1 }

// LoadCrud returns the CRUD payload embedded in the weldtemplate package.
func LoadCrud() (*CrudTemplate, error) {
	root := weldtemplate.FS()
	raw, err := fs.ReadFile(root, "crud/crud.json")
	if err != nil {
		return nil, fmt.Errorf("crud template: read descriptor: %w", err)
	}
	crud := &CrudTemplate{}
	if err := json.Unmarshal(raw, crud); err != nil {
		return nil, fmt.Errorf("crud template: invalid descriptor: %w", err)
	}
	if crud.Version == "" {
		return nil, fmt.Errorf("crud template: missing version")
	}
	if len(crud.Files) == 0 {
		return nil, fmt.Errorf("crud template: no files")
	}
	sub, err := fs.Sub(root, "crud")
	if err != nil {
		return nil, fmt.Errorf("crud template: %w", err)
	}
	crud.fsys = sub
	for _, file := range crud.Files {
		if file.Path == "" || file.Source == "" {
			return nil, fmt.Errorf("crud template: file entry needs path and source")
		}
		if _, err := fs.Stat(sub, file.Source); err != nil {
			return nil, fmt.Errorf("crud template: payload %q: %w", file.Source, err)
		}
	}
	return crud, nil
}

// RenderPath renders a file's project path for vars.
func (c *CrudTemplate) RenderPath(file CrudFile, vars CrudVars) (string, error) {
	rendered, err := execute("path "+file.Path, file.Path, vars)
	if err != nil {
		return "", err
	}
	return string(rendered), nil
}

// RenderFile renders a file's content for vars. A Go payload is formatted, so
// the result is gofmt-clean or an error names the payload.
func (c *CrudTemplate) RenderFile(file CrudFile, vars CrudVars) ([]byte, error) {
	raw, err := fs.ReadFile(c.fsys, file.Source)
	if err != nil {
		return nil, fmt.Errorf("crud template: read %s: %w", file.Source, err)
	}
	rendered, err := execute(file.Source, string(raw), vars)
	if err != nil {
		return nil, err
	}
	rendered = Render(rendered, Vars{Name: vars.Name, Module: vars.Module, Version: vars.Version, Mod: vars.Resource, ModTitle: vars.Title})
	if !strings.HasSuffix(strings.TrimSuffix(file.Source, ".tmpl"), ".go") {
		return rendered, nil
	}
	formatted, err := format.Source(rendered)
	if err != nil {
		return nil, fmt.Errorf("crud template: rendered %s is not valid Go: %w", path.Base(file.Source), err)
	}
	return formatted, nil
}

// execute runs one text/template with missing keys as errors.
func execute(name, text string, vars CrudVars) ([]byte, error) {
	tmpl, err := template.New(name).Option("missingkey=error").Parse(text)
	if err != nil {
		return nil, fmt.Errorf("crud template: parse %s: %w", name, err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, vars); err != nil {
		return nil, fmt.Errorf("crud template: render %s: %w", name, err)
	}
	return buf.Bytes(), nil
}
