package template

import "strings"

// Vars are the substitution values for a generated project.
type Vars struct {
	Name    string
	Module  string
	Version string
	// Mod is a business module's package name and URL segment, used only by the
	// per-module payloads (`weld add module <name>`).
	Mod string
	// ModTitle is Mod with its first letter upper-cased, for exported
	// identifiers such as install<ModTitle>Route.
	ModTitle string
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
		"__modname__", vars.Mod,
		"__ModName__", vars.ModTitle,
	)
	return []byte(replacer.Replace(string(content)))
}
