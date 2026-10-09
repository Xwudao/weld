package template

import (
	"bytes"
	"fmt"
	"go/format"
	"text/template"
)

// CapabilitySet is the set of capabilities installed in a project. It is the
// data a capability-aware payload is rendered against.
type CapabilitySet map[string]bool

// Has reports whether name is installed.
func (s CapabilitySet) Has(name string) bool { return s[name] }

// DITemplateVars is the context used to render a capability's DI sources.
type DITemplateVars struct {
	Name    string
	Module  string
	Version string
	Caps    CapabilitySet
	// Modules lists the installed business modules, in install order, so a
	// capability's graph can bind and route them without a runtime registry.
	Modules []DIModule
}

// RenderDIGraph renders a capability's DI graph source for the installed
// capability set and formats it as Go.
func (c *Capability) RenderDIGraph(vars DITemplateVars) ([]byte, error) {
	return c.renderGo(c.DI.Source, vars)
}

// RenderDITest renders a capability's DI test source, or nil when the
// capability declares no test.
func (c *Capability) RenderDITest(vars DITemplateVars) ([]byte, error) {
	if c.DI == nil || c.DI.Test == "" {
		return nil, nil
	}
	return c.renderGo(c.DI.Test, vars)
}

// renderGo expands the payload as a text/template, substitutes the weld
// placeholder tokens, then formats the result as Go so a capability-aware
// payload is guaranteed to be gofmt-clean whatever its conditionals look like.
func (c *Capability) renderGo(source string, vars DITemplateVars) ([]byte, error) {
	raw, err := c.read(source)
	if err != nil {
		return nil, err
	}
	tmpl, err := template.New(c.Name).Option("missingkey=error").Parse(string(raw))
	if err != nil {
		return nil, fmt.Errorf("capability %q: parse template %s: %w", c.Name, source, err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, vars); err != nil {
		return nil, fmt.Errorf("capability %q: render template %s: %w", c.Name, source, err)
	}
	rendered := Render(buf.Bytes(), Vars{Name: vars.Name, Module: vars.Module, Version: vars.Version})
	formatted, err := format.Source(rendered)
	if err != nil {
		return nil, fmt.Errorf("capability %q: rendered %s is not valid Go: %w", c.Name, source, err)
	}
	return formatted, nil
}
