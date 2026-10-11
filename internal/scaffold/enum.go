package scaffold

import (
	"bytes"
	"errors"
	"fmt"
	"go/format"
	"go/token"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"

	"github.com/Xwudao/weld/internal/genenum"
	"github.com/Xwudao/weld/internal/project"
)

// enumDir is the package directory enum definitions live in, relative to the
// project root. It is deliberately not capability-specific: an enum is a leaf
// type any package may import, so the generator needs nothing installed.
const enumDir = "internal/enums"

// enumValuePattern matches a canonical enum value: lower_snake_case, which is
// both a stable wire string and a constant-name fragment.
var enumValuePattern = regexp.MustCompile(`^[a-z][a-z0-9]*(_[a-z0-9]+)*$`)

// ValidateEnumName reports why an enum name is unusable. The name is the source
// file's base name and, camel-cased, the exported type name (config_key ->
// ConfigKey), so it must be lower_snake_case and not a Go keyword.
func ValidateEnumName(name string) error {
	if !enumValuePattern.MatchString(name) {
		return fmt.Errorf("invalid enum name %q: use lower_snake_case (lowercase letters, digits and single underscores, starting with a letter)", name)
	}
	if token.IsKeyword(name) {
		return fmt.Errorf("invalid enum name %q: it is a Go keyword", name)
	}
	return nil
}

// ValidateEnumValue reports why an enum value is unusable. The value is the
// wire string and a fragment of the generated constant name, so it must be
// lower_snake_case and not a Go keyword.
func ValidateEnumValue(value string) error {
	if !enumValuePattern.MatchString(value) {
		return fmt.Errorf("invalid enum value %q: use lower_snake_case (lowercase letters, digits and single underscores, starting with a letter)", value)
	}
	if token.IsKeyword(value) {
		return fmt.Errorf("invalid enum value %q: it is a Go keyword", value)
	}
	return nil
}

// enumConst is one value together with the constant identifier it produces.
type enumConst struct {
	Name string // StatusInProgress
	Raw  string // in_progress
}

// enumConsts validates values and derives each constant identifier. A repeated
// value or a pair of values that collapse to the same identifier (in_progress
// and inprogress both become InProgress) is an error, so the generated map can
// never carry a duplicate key or constant.
func enumConsts(typeName string, values []string) ([]enumConst, error) {
	consts := make([]enumConst, 0, len(values))
	seenValue := map[string]bool{}
	seenConst := map[string]string{}
	for _, value := range values {
		if err := ValidateEnumValue(value); err != nil {
			return nil, err
		}
		if seenValue[value] {
			return nil, fmt.Errorf("enum value %q is repeated", value)
		}
		seenValue[value] = true
		name := enumConstName(typeName, value)
		if previous, ok := seenConst[name]; ok {
			return nil, fmt.Errorf("enum values %q and %q both map to constant %s", previous, value, name)
		}
		seenConst[name] = value
		consts = append(consts, enumConst{Name: name, Raw: value})
	}
	return consts, nil
}

// enumConstName derives a constant identifier from a type and a value:
// Status + in_progress -> StatusInProgress.
func enumConstName(typeName, value string) string {
	return typeName + camel(value)
}

// enumTypeName derives the exported type name from an enum name:
// config_key -> ConfigKey.
func enumTypeName(name string) string {
	return camel(name)
}

// camel upper-cases the first letter of each underscore-separated word and
// drops the underscores: in_progress -> InProgress, config_key -> ConfigKey.
func camel(value string) string {
	var b strings.Builder
	upper := true
	for _, r := range value {
		if r == '_' {
			upper = true
			continue
		}
		if upper {
			b.WriteRune(unicode.ToUpper(r))
			upper = false
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// renderEnumSource renders the hand-owned file `weld add enum` writes once: a
// package clause, the exported type and its string constants. The methods are
// generated into a sibling *_gen.go, never into this file.
func renderEnumSource(typeName string, consts []enumConst) ([]byte, error) {
	generated := genenum.FileName(genenum.Enum{Name: typeName})
	var b strings.Builder
	b.WriteString("package enums\n\n")
	fmt.Fprintf(&b, "// %s is a typed string enumeration.\n//\n// Its values are declared below; the methods the code uses (String, %sValues,\n// Parse%s, the JSON/YAML text codecs and the SQL codecs) live in the generated\n// %s, which `weld gen enum` rewrites. Add or change a value here and run\n// `weld gen enum`, or add one with `weld add enum`. weld writes this file once\n// and never rewrites it.\ntype %s string\n\n", typeName, typeName, typeName, generated, typeName)
	b.WriteString("const (\n")
	for _, c := range consts {
		fmt.Fprintf(&b, "\t%s %s = %q\n", c.Name, typeName, c.Raw)
	}
	b.WriteString(")\n")
	formatted, err := format.Source([]byte(b.String()))
	if err != nil {
		return nil, fmt.Errorf("format %s.go: %w", typeName, err)
	}
	return formatted, nil
}

// AddEnum plans `weld add enum <name> <value>...`: it writes the hand-owned
// type/constant file once and immediately generates its methods into a sibling
// *_gen.go through the same renderer `weld gen enum` uses.
//
// It requires no capability. The definition file is recorded under enum:<name>
// and never rewritten, so a repeated add is a no-op; the generated file is
// tool-owned and may be refreshed at any time.
func AddEnum(req Request, name string, values []string) (*Result, error) {
	if err := ValidateEnumName(name); err != nil {
		return nil, err
	}
	if len(values) == 0 {
		return nil, fmt.Errorf("enum %q needs at least one value", name)
	}
	typeName := enumTypeName(name)
	consts, err := enumConsts(typeName, values)
	if err != nil {
		return nil, err
	}

	manifest, err := project.Load(req.Dir)
	if err != nil {
		return nil, err
	}
	humanPath := path.Join(enumDir, name+".go")
	result := &Result{Dir: req.Dir}
	if manifest.Owns(humanPath) {
		result.Notes = alreadyInstalledEnumNotes(req.Dir, manifest, name)
		return result, nil
	}

	p := newPlanner(req, manifest, result)
	human, err := renderEnumSource(typeName, consts)
	if err != nil {
		return nil, err
	}
	if err := p.planNewFile(humanPath, "enum:"+name, human); err != nil {
		return nil, err
	}

	pkg, err := detectEnumPackage(req.Dir, p.planned)
	if err != nil {
		return nil, err
	}
	target, ok := findEnum(pkg, typeName)
	if !ok {
		return nil, fmt.Errorf("enum %q was not detected after planning", typeName)
	}
	generated, err := genenum.Render(pkg, target)
	if err != nil {
		return nil, err
	}
	if err := p.planGeneratedFile(path.Join(enumDir, genenum.FileName(target)), "enum:"+name, generated); err != nil {
		return nil, err
	}
	if err := p.writeManifest(); err != nil {
		return nil, err
	}
	return result, nil
}

// GenEnums plans regenerating every enum's *_gen.go from the package's source.
// A generated file that already matches the rendered content is not rewritten,
// so a repeated `weld gen enum` is a no-op. With prune, a *_gen.go whose enum
// type no longer exists is deleted (only when it carries the generated header).
func GenEnums(req Request, prune bool) (*Result, error) {
	manifest, err := project.Load(req.Dir)
	if err != nil {
		return nil, err
	}
	result := &Result{Dir: req.Dir}
	pkg, err := genenum.LoadDir(filepath.Join(req.Dir, enumDir))
	if err != nil {
		return nil, err
	}

	p := newPlanner(req, manifest, result)
	if pkg != nil {
		for _, e := range pkg.Enums {
			generated, err := genenum.Render(pkg, e)
			if err != nil {
				return nil, err
			}
			owner := "enum:" + strings.TrimSuffix(genenum.FileName(e), "_gen.go")
			if err := p.planGeneratedFile(path.Join(enumDir, genenum.FileName(e)), owner, generated); err != nil {
				return nil, err
			}
		}
	}
	if prune {
		if err := p.pruneEnumFiles(pkg); err != nil {
			return nil, err
		}
	}
	if err := p.writeManifest(); err != nil {
		return nil, err
	}
	if len(result.Operations) == 0 {
		result.Notes = append(result.Notes, "generated enum files are up to date")
	}
	return result, nil
}

// CheckEnums reports the generated enum files that are missing, stale or
// orphaned, without writing anything. An empty result means `weld gen enum`
// would change nothing.
func CheckEnums(req Request) ([]string, error) {
	dir := filepath.Join(req.Dir, enumDir)
	pkg, err := genenum.LoadDir(dir)
	if err != nil {
		return nil, err
	}

	var stale []string
	wanted := map[string]bool{}
	if pkg != nil {
		for _, e := range pkg.Enums {
			file := genenum.FileName(e)
			wanted[file] = true
			content, err := genenum.Render(pkg, e)
			if err != nil {
				return nil, err
			}
			target := path.Join(enumDir, file)
			existing, err := os.ReadFile(filepath.Join(req.Dir, target))
			switch {
			case errors.Is(err, os.ErrNotExist):
				stale = append(stale, target+" (missing)")
			case err != nil:
				return nil, err
			case !bytes.Equal(existing, content):
				stale = append(stale, target+" (stale)")
			}
		}
	}

	entries, err := os.ReadDir(dir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, "_gen.go") || wanted[name] {
			continue
		}
		stale = append(stale, path.Join(enumDir, name)+" (orphan)")
	}
	return stale, nil
}

// detectEnumPackage parses the enum package as it will be once this plan is
// applied, overlaying the files this plan wrote in memory so a newly added enum
// is detected before it reaches disk.
func detectEnumPackage(dir string, planned map[string][]byte) (*genenum.Package, error) {
	overlay := map[string][]byte{}
	for target, content := range planned {
		if path.Dir(target) != enumDir {
			continue
		}
		base := path.Base(target)
		if !strings.HasSuffix(base, ".go") || strings.HasSuffix(base, "_gen.go") {
			continue
		}
		overlay[base] = content
	}
	return genenum.LoadDirOverlay(filepath.Join(dir, enumDir), overlay)
}

// findEnum returns the named enum from a detected package.
func findEnum(pkg *genenum.Package, name string) (genenum.Enum, bool) {
	if pkg == nil {
		return genenum.Enum{}, false
	}
	for _, e := range pkg.Enums {
		if e.Name == name {
			return e, true
		}
	}
	return genenum.Enum{}, false
}

// planGeneratedFile plans a tool-owned generated file. It refuses to overwrite
// a file that exists without the generated header, so a user file that happens
// to share the name is never clobbered. An identical file plans nothing.
func (p *planner) planGeneratedFile(target, owner string, content []byte) error {
	existed := false
	if _, planned := p.planned[target]; !planned {
		existing, err := os.ReadFile(filepath.Join(p.req.Dir, target))
		switch {
		case err == nil:
			existed = true
			if !genenum.IsGenerated(existing) {
				return &project.ConflictError{Path: target, Reason: "exists but is not a weld-generated enum file (missing the DO NOT EDIT header)"}
			}
			if bytes.Equal(existing, content) {
				p.manifest.SetFile(target, owner, content)
				return nil
			}
		case errors.Is(err, os.ErrNotExist):
		default:
			return err
		}
	}
	p.planned[target] = content
	p.upsert(project.Operation{Path: target, Content: content, Overwrite: existed})
	p.manifest.SetFile(target, owner, content)
	return nil
}

// pruneEnumFiles plans deleting every weld-generated *_gen.go whose enum type no
// longer exists. A file without the generated header is left alone.
func (p *planner) pruneEnumFiles(pkg *genenum.Package) error {
	dir := filepath.Join(p.req.Dir, enumDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	wanted := map[string]bool{}
	if pkg != nil {
		for _, e := range pkg.Enums {
			wanted[genenum.FileName(e)] = true
		}
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, "_gen.go") || wanted[name] {
			continue
		}
		target := path.Join(enumDir, name)
		if _, planned := p.planned[target]; planned {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return err
		}
		if !genenum.IsGenerated(raw) {
			continue
		}
		p.upsert(project.Operation{Path: target, Delete: true})
		p.manifest.RemoveFile(target)
	}
	return nil
}

// alreadyInstalledEnumNotes explains a repeated `weld add enum` and reports any
// managed file the user changed since it was generated.
func alreadyInstalledEnumNotes(root string, manifest *project.Manifest, name string) []string {
	notes := []string{fmt.Sprintf("enum %s is already installed", name)}
	drift := manifest.Drift(root)
	if len(drift) == 0 {
		return notes
	}
	notes = append(notes, fmt.Sprintf("%d managed file(s) changed since install:", len(drift)))
	for _, item := range drift {
		notes = append(notes, fmt.Sprintf("  %s (%s)", item.Path, item.Reason))
	}
	return notes
}
