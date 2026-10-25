package scaffold

import (
	"fmt"
	"strings"

	"github.com/Xwudao/weld/internal/project"
	"github.com/Xwudao/weld/internal/template"
)

// MissingRequirementsError reports that a scaffold request needs capabilities
// that are not installed. weld returns it by default; Request.WithDependencies
// opts into installing the requirements instead. Capabilities are one-way
// additive and a requirement is a real dependency, so the caller is told
// exactly what is missing rather than having it installed silently.
type MissingRequirementsError struct {
	// Subject names what was requested, for example `capability "web"`.
	Subject string
	// Missing lists the capabilities that would be installed, dependencies first.
	Missing []string
}

func (e *MissingRequirementsError) Error() string {
	verb, pronoun := "is", "it"
	if len(e.Missing) != 1 {
		verb, pronoun = "are", "them"
	}
	return fmt.Sprintf("%s requires %s, which %s not installed; add %s first, or re-run with --with-dependencies", e.Subject, quoteNames(e.Missing), verb, pronoun)
}

// quoteNames renders capability names as a quoted, comma-separated list with a
// final "and".
func quoteNames(names []string) string {
	quoted := make([]string, len(names))
	for i, name := range names {
		quoted[i] = fmt.Sprintf("%q", name)
	}
	switch len(quoted) {
	case 0:
		return ""
	case 1:
		return quoted[0]
	case 2:
		return quoted[0] + " and " + quoted[1]
	default:
		return strings.Join(quoted[:len(quoted)-1], ", ") + " and " + quoted[len(quoted)-1]
	}
}

// capabilityNames returns the names of order in order.
func capabilityNames(order []*template.Capability) []string {
	names := make([]string, len(order))
	for i, capability := range order {
		names[i] = capability.Name
	}
	return names
}

// capabilityNamesExcept returns the names of order that are not in excluded, in
// order.
func capabilityNamesExcept(order []*template.Capability, excluded ...*template.Capability) []string {
	skip := make(map[string]bool, len(excluded))
	for _, capability := range excluded {
		skip[capability.Name] = true
	}
	var names []string
	for _, capability := range order {
		if !skip[capability.Name] {
			names = append(names, capability.Name)
		}
	}
	return names
}

// InstallOrder returns the names of the capabilities `weld add <names...>` would
// install into the project manifest describes, dependencies first. A requested
// capability that is already installed contributes nothing, so an empty result
// means the add would install nothing. It is the read-only view of the order Add
// plans, for commands that explain a capability without planning any writes.
func InstallOrder(catalog *template.Catalog, manifest *project.Manifest, names ...string) ([]string, error) {
	requested, err := requestedCapabilities(catalog, names)
	if err != nil {
		return nil, err
	}
	var pending []*template.Capability
	for _, capability := range requested {
		if !manifest.HasCapability(capability.Name) {
			pending = append(pending, capability)
		}
	}
	if len(pending) == 0 {
		return nil, nil
	}
	order, err := requiredCapabilities(catalog, manifest, pending...)
	if err != nil {
		return nil, err
	}
	return capabilityNames(order), nil
}

// requiredCapabilities returns the capabilities an Add plan installs for the
// requested capabilities: their transitive requirements first, then each
// requested capability itself, plus Loom (and the config Loom requires) when
// Loom is not already accounted for. All requests share one install order, so a
// requirement two of them need is planned once. Capabilities already recorded in
// the manifest are excluded, so the result is exactly what the plan would write.
func requiredCapabilities(catalog *template.Catalog, manifest *project.Manifest, requested ...*template.Capability) ([]*template.Capability, error) {
	selected := map[string]bool{}
	var order []*template.Capability
	for _, capability := range requested {
		installed, err := installOrder(catalog, manifest, capability, selected)
		if err != nil {
			return nil, err
		}
		order = append(order, installed...)
	}
	// Every capability except the bare base CLI is developed on Loom, so adding
	// anything plans Loom (and the config it requires) unless it is already
	// installed. http and the capabilities that require it pull Loom through the
	// dependency graph; this covers config, the one leaf Loom itself requires.
	if !manifest.HasCapability("loom") && !containsCapability(order, "loom") {
		loomCapability, err := catalog.Get("loom")
		if err != nil {
			return nil, err
		}
		loomOrder, err := installOrder(catalog, manifest, loomCapability, selected)
		if err != nil {
			return nil, err
		}
		order = append(order, loomOrder...)
	}
	return order, nil
}
