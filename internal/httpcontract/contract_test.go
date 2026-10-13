package httpcontract

import (
	"bytes"
	"go/format"
	"strings"
	"testing"
)

func simpleContract() *Contract {
	response := Type{Kind: "basic", Name: "string"}
	return &Contract{Version: Version, Service: "service", Operations: []Operation{{Name: "health", Method: "GET", Path: "/health", Binding: "none", Response: &response, Status: 200}}}
}

func TestContractRoundTripAndValidation(t *testing.T) {
	c := simpleContract()
	data, err := c.JSON()
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	other, err := decoded.JSON()
	if err != nil || !bytes.Equal(data, other) {
		t.Fatalf("round trip: %s %v", other, err)
	}
	for _, tc := range []struct {
		name   string
		mutate func(*Contract)
	}{
		{"version", func(c *Contract) { c.Version = 99 }},
		{"operation collision", func(c *Contract) { op := c.Operations[0]; op.Name = "Health"; c.Operations = append(c.Operations, op) }},
		{"DTO function collision", func(c *Contract) { c.Types = []Definition{{Name: "New", Type: Type{Kind: "basic", Name: "string"}}} }},
		{"unknown DTO", func(c *Contract) { c.Operations[0].Response = &Type{Kind: "ref", Name: "Missing"} }},
		{"recursive value", func(c *Contract) { c.Types = []Definition{{Name: "Loop", Type: Type{Kind: "ref", Name: "Loop"}}} }},
		{"invalid path", func(c *Contract) { c.Operations[0].Path = "/path?token=x" }},
		{"unsupported type", func(c *Contract) { c.Operations[0].Response = &Type{Kind: "basic", Name: "complex64"} }},
		{"missing bodyless marker", func(c *Contract) { c.Operations[0].Status = 204 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := simpleContract()
			tc.mutate(c)
			if err := c.Validate(); err == nil {
				t.Fatal("invalid contract accepted")
			}
		})
	}
	for _, source := range []string{string(data) + "{}", strings.Replace(string(data), `"service": "service"`, `"unknown": 1, "service": "service"`, 1)} {
		if _, err := Decode(strings.NewReader(source)); err == nil {
			t.Fatal("invalid JSON contract accepted")
		}
	}
	if _, err := Decode(strings.NewReader(strings.Repeat(" ", MaxContractBytes+1))); err == nil {
		t.Fatal("oversized contract accepted")
	}
}

func TestBindingContractValidation(t *testing.T) {
	for _, tc := range []struct {
		name    string
		request Type
		path    string
	}{
		{"missing path tag", Type{Kind: "struct"}, "/{id}"},
		{"wildcard", Type{Kind: "struct", Fields: []Field{{Name: "ID", Type: Type{Kind: "basic", Name: "string"}, Tag: `path:"id"`}}}, "/{id...}"},
		{"non-struct", Type{Kind: "basic", Name: "int"}, "/"},
		{"double pointer", Type{Kind: "pointer", Elem: &Type{Kind: "pointer", Elem: &Type{Kind: "struct"}}}, "/"},
		{"duplicate query tag", Type{Kind: "struct", Fields: []Field{{Name: "A", Type: Type{Kind: "basic", Name: "string"}, Tag: `query:"q"`}, {Name: "B", Type: Type{Kind: "basic", Name: "string"}, Tag: `query:"q"`}}}, "/"},
		{"query map", Type{Kind: "struct", Fields: []Field{{Name: "Values", Type: Type{Kind: "map", Elem: &Type{Kind: "basic", Name: "string"}}, Tag: `query:"q"`}}}, "/"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := simpleContract()
			c.Operations[0].Binding = "query"
			c.Operations[0].Request = &tc.request
			c.Operations[0].Path = tc.path
			if err := c.Validate(); err == nil {
				t.Fatal("unsupported binding accepted")
			}
		})
	}
}

func TestGeneratorRejectsInvalidPackageAndKeepsRuntimeTemplateFormatted(t *testing.T) {
	for _, name := range []string{"../escape", "package", "main", "", "_"} {
		if _, err := Generate(simpleContract(), name); err == nil {
			t.Fatalf("package %q accepted", name)
		}
	}
	// The checked-in runtime is itself Go source and should not rely on the
	// final generator formatting pass to fix its indentation.
	formatted, err := format.Source([]byte(clientRuntime))
	if err != nil || string(formatted) != clientRuntime {
		t.Fatal("unformatted runtime template", err)
	}
	files, err := Generate(simpleContract(), "client")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(files["client_gen.go"]), "func (c *Client) Health(ctx context.Context) (string, error)") {
		t.Fatal("missing typed method")
	}
}
