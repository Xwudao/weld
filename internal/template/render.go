package template

import "strings"

// Vars are the substitution values for a generated project.
type Vars struct {
	Name    string
	Module  string
	Version string
}

// Render substitutes weld placeholder tokens with concrete project values.
//
// Payloads are plain text, so a literal token replace is used instead of
// text/template action syntax, which would collide with the braces found in
// JSON, JSX, CSS and shell payloads.
func Render(content []byte, vars Vars) []byte {
	replacer := strings.NewReplacer(
		"__name__", vars.Name,
		"__module__", vars.Module,
		"__version__", vars.Version,
	)
	return []byte(replacer.Replace(string(content)))
}
