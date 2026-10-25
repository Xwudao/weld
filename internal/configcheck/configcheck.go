// Package configcheck compares a generated project's config.yml with the
// committed config.example.yml it was created from.
//
// The generated loader decodes config.yml leniently: an unknown key is ignored,
// so a mistyped setting (rate_limit.burts) silently keeps its default. The
// committed example is the reference every capability extends when it is
// installed, so walking both documents side by side finds the keys the loader
// would ignore, the values it could not decode into the shipped type, and the
// settings left to their defaults or the environment.
//
// It never reports a value. A finding names the dotted key and the YAML kind
// only, because config.yml holds local credentials. It never runs the project
// either: a semantic rule (a port range, a minimum secret length) is still the
// project's own Validate method's to enforce at startup.
package configcheck

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Level is the severity of a finding.
type Level string

const (
	// LevelWarn is a setting the loader accepts but probably not as intended:
	// an unknown key, or a key left to its default.
	LevelWarn Level = "warn"
	// LevelFail is a value the loader cannot decode into the shipped type.
	LevelFail Level = "fail"
)

// Finding is one difference between config.yml and the reference.
type Finding struct {
	Level Level `json:"level"`
	// Key is the dotted path of the setting, for example rate_limit.burst or
	// cors.allowed_origins[1].
	Key     string `json:"key"`
	Message string `json:"message"`
}

// Check compares config with reference and returns the findings, sorted by
// key. An invalid document is an error that does not quote the input, since a
// YAML parser error can echo the offending line.
func Check(config, reference []byte) ([]Finding, error) {
	ref, err := parse(reference)
	if err != nil {
		return nil, fmt.Errorf("reference: %w", err)
	}
	got, err := parse(config)
	if err != nil {
		return nil, err
	}
	c := &checker{}
	c.compare("", ref, got)
	sort.SliceStable(c.findings, func(i, j int) bool { return c.findings[i].Key < c.findings[j].Key })
	return c.findings, nil
}

// parse reads one YAML document into its root node. An empty document is a nil
// node, which compares as an empty mapping.
func parse(data []byte) (*yaml.Node, error) {
	var document yaml.Node
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(&document); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, nil
		}
		return nil, errors.New("invalid YAML")
	}
	return resolve(&document), nil
}

// resolve unwraps a document node and follows aliases, so the comparison only
// sees mappings, sequences and scalars.
func resolve(node *yaml.Node) *yaml.Node {
	for node != nil {
		switch node.Kind {
		case yaml.DocumentNode:
			if len(node.Content) == 0 {
				return nil
			}
			node = node.Content[0]
		case yaml.AliasNode:
			node = node.Alias
		default:
			return node
		}
	}
	return nil
}

type checker struct {
	findings []Finding
}

func (c *checker) add(level Level, key, format string, args ...any) {
	if key == "" {
		key = "(document)"
	}
	c.findings = append(c.findings, Finding{Level: level, Key: key, Message: fmt.Sprintf(format, args...)})
}

// compare walks ref and got together. A null reference node accepts anything:
// the shipped file does not say what belongs there.
func (c *checker) compare(key string, ref, got *yaml.Node) {
	ref, got = resolve(ref), resolve(got)
	if ref == nil || isNull(ref) {
		return
	}
	if got == nil || isNull(got) {
		if ref.Kind == yaml.MappingNode && key == "" {
			// An empty config.yml: every top-level section is missing.
			c.compare(key, ref, &yaml.Node{Kind: yaml.MappingNode})
			return
		}
		c.add(LevelWarn, key, "is empty; the default or the environment applies")
		return
	}
	switch ref.Kind {
	case yaml.MappingNode:
		if got.Kind != yaml.MappingNode {
			c.add(LevelFail, key, "is %s, want a mapping", kind(got))
			return
		}
		c.mapping(key, ref, got)
	case yaml.SequenceNode:
		if got.Kind != yaml.SequenceNode {
			c.add(LevelFail, key, "is %s, want a list", kind(got))
			return
		}
		if len(ref.Content) == 0 {
			return
		}
		for i, element := range got.Content {
			c.compare(fmt.Sprintf("%s[%d]", key, i), ref.Content[0], element)
		}
	case yaml.ScalarNode:
		if got.Kind != yaml.ScalarNode {
			c.add(LevelFail, key, "is %s, want %s", kind(got), kind(ref))
			return
		}
		switch {
		case scalarFits(ref.ShortTag(), got.ShortTag()):
		case ref.ShortTag() == "!!int" && got.ShortTag() == "!!float":
			// The example cannot say whether 10 stands for an int or a float64
			// field, so a fraction is only suspicious, not a decode failure.
			c.add(LevelWarn, key, "is a number with a fraction, but config.example.yml has a whole number; the loader fails if the setting is an integer")
		default:
			c.add(LevelFail, key, "is %s, want %s", kind(got), kind(ref))
		}
	}
}

// mapping compares two mappings key by key. An empty reference mapping is
// free-form (a map field), so any key is accepted under it.
func (c *checker) mapping(key string, ref, got *yaml.Node) {
	refKeys := pairs(ref)
	gotKeys := pairs(got)
	seen := map[string]bool{}
	for i := 0; i+1 < len(got.Content); i += 2 {
		name := got.Content[i]
		if name.ShortTag() == "!!merge" {
			continue
		}
		if seen[name.Value] {
			c.add(LevelFail, join(key, name.Value), "is defined more than once; the loader refuses the file")
		}
		seen[name.Value] = true
	}
	if len(ref.Content) == 0 {
		return
	}
	for name, value := range gotKeys {
		refValue, known := refKeys[name]
		if !known {
			c.add(LevelWarn, join(key, name), "is not a known setting; the loader ignores it (a typo, or a setting missing from config.example.yml)")
			continue
		}
		c.compare(join(key, name), refValue, value)
	}
	for name := range refKeys {
		if _, present := gotKeys[name]; !present {
			c.add(LevelWarn, join(key, name), "is missing; the default or the environment applies")
		}
	}
}

// pairs indexes a mapping node by key. A merge key (<<) is expanded, so a
// mapping that reuses an anchor is compared with the keys it actually has.
func pairs(node *yaml.Node) map[string]*yaml.Node {
	out := map[string]*yaml.Node{}
	for i := 0; i+1 < len(node.Content); i += 2 {
		name, value := node.Content[i], node.Content[i+1]
		if name.ShortTag() == "!!merge" {
			if merged := resolve(value); merged != nil && merged.Kind == yaml.MappingNode {
				for k, v := range pairs(merged) {
					if _, exists := out[k]; !exists {
						out[k] = v
					}
				}
			}
			continue
		}
		out[name.Value] = value
	}
	return out
}

// scalarFits reports whether a scalar tagged got decodes into the field the
// reference scalar tagged ref stands for. A string field takes any scalar, a
// float field takes an integer, and the other kinds must match.
func scalarFits(ref, got string) bool {
	switch ref {
	case "!!str", "!!null":
		return true
	case "!!float":
		return got == "!!float" || got == "!!int"
	default:
		return ref == got
	}
}

func isNull(node *yaml.Node) bool {
	return node.Kind == yaml.ScalarNode && node.ShortTag() == "!!null"
}

// kind names a node's YAML kind for a message, never its value.
func kind(node *yaml.Node) string {
	switch node.Kind {
	case yaml.MappingNode:
		return "a mapping"
	case yaml.SequenceNode:
		return "a list"
	}
	switch node.ShortTag() {
	case "!!int":
		return "a whole number"
	case "!!float":
		return "a number"
	case "!!bool":
		return "a boolean"
	case "!!str":
		return "a string"
	case "!!null":
		return "empty"
	}
	return "a " + strings.TrimPrefix(node.ShortTag(), "!!")
}

func join(prefix, name string) string {
	if prefix == "" {
		return name
	}
	return prefix + "." + name
}
