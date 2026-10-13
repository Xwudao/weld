// Package httpcontract exports a small typed-JSON contract and generates
// standalone Go clients. Contracts describe transport shapes, not validation
// rules, business services, or a general-purpose API schema.
package httpcontract

import (
	"encoding/json"
	"fmt"
	"go/token"
	"io"
	"reflect"
	"strings"
	"unicode"
)

const Version = 2
const MaxContractBytes = 8 << 20

// Contract is a portable snapshot, independent of the exporting Go module.
type Contract struct {
	Version    int          `json:"version"`
	Service    string       `json:"service"`
	Operations []Operation  `json:"operations"`
	Types      []Definition `json:"types,omitempty"`
	Skipped    []string     `json:"skipped,omitempty"`
}

type Operation struct {
	Name      string   `json:"name"`
	Module    string   `json:"module"`
	Method    string   `json:"method"`
	Path      string   `json:"path"`
	Binding   string   `json:"binding"` // none, query, json
	Request   *Type    `json:"request,omitempty"`
	Response  *Type    `json:"response,omitempty"`
	Status    int      `json:"status"`
	NoContent bool     `json:"noContent,omitempty"`
	Policies  []string `json:"policies,omitempty"`
}

type Definition struct {
	Name string `json:"name"`
	Type Type   `json:"type"`
}

// Type is intentionally limited to ordinary JSON-compatible Go data types.
type Type struct {
	Kind   string      `json:"kind"` // basic, ref, pointer, slice, array, map, struct, time, raw, enum
	Name   string      `json:"name,omitempty"`
	Elem   *Type       `json:"elem,omitempty"`
	Key    *Type       `json:"key,omitempty"`
	Fields []Field     `json:"fields,omitempty"`
	Len    int64       `json:"len,omitempty"`
	Values []EnumValue `json:"values,omitempty"`
}

type EnumValue struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type Field struct {
	Name     string `json:"name"`
	Type     Type   `json:"type"`
	Tag      string `json:"tag,omitempty"`
	Embedded bool   `json:"embedded,omitempty"`
}

var basicTypes = map[string]bool{
	"string": true, "bool": true, "int": true, "int8": true, "int16": true, "int32": true, "int64": true,
	"uint": true, "uint8": true, "uint16": true, "uint32": true, "uint64": true, "float32": true, "float64": true,
}

var mapKeyTypes = map[string]bool{"string": true, "int": true, "int8": true, "int16": true, "int32": true, "int64": true, "uint": true, "uint8": true, "uint16": true, "uint32": true, "uint64": true}

var reservedTypes = map[string]bool{"Client": true, "Option": true, "APIError": true, "New": true, "WithHTTPClient": true, "WithHeader": true, "WithBearerToken": true, "RequestError": true, "TransportError": true, "ProtocolError": true}

func exported(name string) bool {
	return token.IsIdentifier(name) && unicode.IsUpper([]rune(name)[0])
}

// GoName converts a stable operation ID (e.g. widget_create_item) to a method.
func GoName(id string) string {
	var b strings.Builder
	upper := true
	for _, r := range id {
		if r == '_' || r == '-' || r == '.' {
			upper = true
			continue
		}
		if upper {
			r = unicode.ToUpper(r)
			upper = false
		}
		b.WriteRune(r)
	}
	return b.String()
}

func Decode(r io.Reader) (*Contract, error) {
	data, err := io.ReadAll(io.LimitReader(r, MaxContractBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > MaxContractBytes {
		return nil, fmt.Errorf("HTTP contract exceeds %d bytes", MaxContractBytes)
	}
	var c Contract
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("decode HTTP contract: %w", err)
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return nil, fmt.Errorf("HTTP contract must contain one JSON document")
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

func (c *Contract) JSON() ([]byte, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	return append(b, '\n'), err
}

func (c *Contract) Validate() error {
	if c.Version != Version && c.Version != 1 {
		return fmt.Errorf("unsupported HTTP contract version %d", c.Version)
	}
	if c.Service == "" || len(c.Operations) == 0 {
		return fmt.Errorf("HTTP contract needs a service and typed operations")
	}
	defs := map[string]Type{}
	for _, d := range c.Types {
		if !exported(d.Name) || reservedTypes[d.Name] {
			return fmt.Errorf("invalid or reserved DTO name %q", d.Name)
		}
		if _, ok := defs[d.Name]; ok {
			return fmt.Errorf("duplicate DTO %q", d.Name)
		}
		defs[d.Name] = d.Type
	}
	symbols := map[string]bool{}
	for name := range reservedTypes {
		symbols[name] = true
	}
	for name := range defs {
		symbols[name] = true
	}
	for _, d := range c.Types {
		if d.Type.Kind == "enum" {
			for _, name := range enumSymbols(d) {
				if !exported(name) || symbols[name] {
					return fmt.Errorf("duplicate or invalid enum symbol %q", name)
				}
				symbols[name] = true
			}
		}
		if err := validateType(d.Type, defs, 0); err != nil {
			return fmt.Errorf("DTO %s: %w", d.Name, err)
		}
	}
	sizeState := map[string]uint8{}
	for name := range defs {
		if err := validateSize(Type{Kind: "ref", Name: name}, defs, sizeState); err != nil {
			return fmt.Errorf("DTO %s: %w", name, err)
		}
	}
	methods := map[string]bool{}
	for _, op := range c.Operations {
		method := GoName(op.Name)
		if !exported(method) || methods[method] {
			return fmt.Errorf("invalid or duplicate operation %q (%s)", op.Name, method)
		}
		methods[method] = true
		if op.Status < 200 || op.Status > 599 || (op.Status == 204 || op.Status == 304 || op.Method == "HEAD") && !op.NoContent {
			return fmt.Errorf("%s: invalid response status", op.Name)
		}
		if !strings.HasPrefix(op.Path, "/") || strings.ContainsAny(op.Path, "?#\r\n ") {
			return fmt.Errorf("%s: invalid route path %q", op.Name, op.Path)
		}
		switch op.Method {
		case "GET", "DELETE", "POST", "PUT", "PATCH", "HEAD", "OPTIONS":
		default:
			return fmt.Errorf("%s: unsupported HTTP method %q", op.Name, op.Method)
		}
		switch op.Binding {
		case "none":
			if op.Request != nil {
				return fmt.Errorf("%s: no-input operation has a request", op.Name)
			}
		case "query", "json":
			if op.Request == nil {
				return fmt.Errorf("%s: missing request", op.Name)
			}
			if err := validateType(*op.Request, defs, 0); err != nil {
				return fmt.Errorf("%s request: %w", op.Name, err)
			}
			if err := validateBinding(*op.Request, defs, op.Path, op.Binding); err != nil {
				return fmt.Errorf("%s: %w", op.Name, err)
			}
		default:
			return fmt.Errorf("%s: invalid binding %q", op.Name, op.Binding)
		}
		if op.Binding == "none" && strings.ContainsAny(op.Path, "{}") {
			return fmt.Errorf("%s: no-input route cannot bind a path parameter", op.Name)
		}
		if !op.NoContent {
			if op.Response == nil {
				return fmt.Errorf("%s: missing response", op.Name)
			}
			if err := validateType(*op.Response, defs, 0); err != nil {
				return fmt.Errorf("%s response: %w", op.Name, err)
			}
		}
	}
	return nil
}

func validateType(t Type, defs map[string]Type, depth int) error {
	if depth > 64 {
		return fmt.Errorf("DTO nesting exceeds 64 levels")
	}
	switch t.Kind {
	case "basic":
		if !basicTypes[t.Name] {
			return fmt.Errorf("unsupported basic type %q", t.Name)
		}
	case "ref":
		if _, ok := defs[t.Name]; !ok {
			return fmt.Errorf("unknown DTO %q", t.Name)
		}
	case "time", "raw":
	case "enum":
		if len(t.Values) == 0 {
			return fmt.Errorf("enum needs declared values")
		}
		seen := map[string]bool{}
		for _, v := range t.Values {
			if !exported(v.Name) || seen[v.Value] {
				return fmt.Errorf("invalid enum constant %q or duplicate wire value", v.Name)
			}
			seen[v.Value] = true
		}
	case "array":
		if t.Len < 0 || t.Len > 1<<20 {
			return fmt.Errorf("array length must be between 0 and 1048576")
		}
		if t.Elem == nil {
			return fmt.Errorf("array needs an element type")
		}
		return validateType(*t.Elem, defs, depth+1)
	case "pointer", "slice", "map":
		if t.Kind == "map" && t.Key != nil {
			if err := validateType(*t.Key, defs, depth+1); err != nil {
				return fmt.Errorf("map key: %w", err)
			}
			key := *t.Key
			for i := 0; key.Kind == "ref" && i < 64; i++ {
				key = defs[key.Name]
			}
			if key.Kind != "enum" && key.Kind != "time" && (key.Kind != "basic" || !mapKeyTypes[key.Name]) {
				return fmt.Errorf("map keys must be strings, integers, weld enums or time.Time")
			}
		}
		if t.Elem == nil {
			return fmt.Errorf("%s needs an element type", t.Kind)
		}
		return validateType(*t.Elem, defs, depth+1)
	case "struct":
		seen := map[string]bool{}
		for _, f := range t.Fields {
			if !exported(f.Name) || seen[f.Name] {
				return fmt.Errorf("invalid or duplicate field %q", f.Name)
			}
			seen[f.Name] = true
			if f.Embedded {
				ft := f.Type
				if ft.Kind == "pointer" && ft.Elem != nil {
					ft = *ft.Elem
				}
				if ft.Kind != "ref" || ft.Name != f.Name {
					return fmt.Errorf("embedded field %s must reference its named DTO", f.Name)
				}
			}
			if err := validateType(f.Type, defs, depth+1); err != nil {
				return fmt.Errorf("field %s: %w", f.Name, err)
			}
		}
	default:
		return fmt.Errorf("unsupported type kind %q", t.Kind)
	}
	return nil
}

// References through pointers/slices/maps are valid recursive Go DTOs;
// direct alias or by-value struct recursion is not.
func validateSize(t Type, defs map[string]Type, state map[string]uint8) error {
	switch t.Kind {
	case "ref":
		if state[t.Name] == 1 {
			return fmt.Errorf("invalid direct recursive type %s", t.Name)
		}
		if state[t.Name] == 2 {
			return nil
		}
		state[t.Name] = 1
		if err := validateSize(defs[t.Name], defs, state); err != nil {
			return err
		}
		state[t.Name] = 2
	case "array":
		return validateSize(*t.Elem, defs, state)
	case "struct":
		for _, f := range t.Fields {
			if err := validateSize(f.Type, defs, state); err != nil {
				return err
			}
		}
	}
	return nil
}

func resolve(t Type, defs map[string]Type) (Type, error) {
	for i := 0; i < 64; i++ {
		if t.Kind == "ref" {
			t = defs[t.Name]
			continue
		}
		if t.Kind == "pointer" && t.Elem != nil {
			t = *t.Elem
			continue
		}
		return t, nil
	}
	return Type{}, fmt.Errorf("cyclic or excessively nested binding type")
}

func validateBinding(t Type, defs map[string]Type, pattern, binding string) error {
	pointers := 0
	for i := 0; i < 64; i++ {
		if t.Kind == "ref" {
			t = defs[t.Name]
			continue
		}
		if t.Kind == "pointer" {
			pointers++
			t = *t.Elem
			continue
		}
		break
	}
	if pointers > 1 || t.Kind != "struct" {
		return fmt.Errorf("request DTO must be a struct or *struct")
	}
	paths := map[string]bool{}
	seen := map[string]bool{}
	for _, f := range t.Fields {
		for _, tag := range []string{"path", "query"} {
			name := strings.Split(reflect.StructTag(f.Tag).Get(tag), ",")[0]
			if name == "" || name == "-" || tag == "query" && binding != "query" {
				continue
			}
			if seen[tag+":"+name] {
				return fmt.Errorf("duplicate %s tag %q", tag, name)
			}
			seen[tag+":"+name] = true
			ft, err := resolve(f.Type, defs)
			if err != nil {
				return err
			}
			if ft.Kind == "slice" && tag == "query" && ft.Elem != nil {
				ft, err = resolve(*ft.Elem, defs)
				if err != nil {
					return err
				}
				if (ft.Kind != "basic" || ft.Name != "string") && ft.Kind != "enum" {
					return fmt.Errorf("field %s: query slices must contain strings", f.Name)
				}
			} else if ft.Kind != "basic" && ft.Kind != "enum" {
				return fmt.Errorf("field %s: %s needs a scalar", f.Name, tag)
			}
			if tag == "path" {
				paths[name] = true
			}
		}
	}
	for _, segment := range strings.Split(pattern, "/") {
		if !strings.ContainsAny(segment, "{}") {
			continue
		}
		if !strings.HasPrefix(segment, "{") || !strings.HasSuffix(segment, "}") {
			return fmt.Errorf("unsupported path pattern %q", segment)
		}
		name := strings.TrimSuffix(strings.TrimPrefix(segment, "{"), "}")
		if !token.IsIdentifier(name) || !paths[name] {
			return fmt.Errorf("path parameter %q needs a matching scalar path tag (wildcards are unsupported)", name)
		}
		delete(paths, name)
	}
	if len(paths) > 0 {
		return fmt.Errorf("path tag has no matching route parameter")
	}
	return nil
}
