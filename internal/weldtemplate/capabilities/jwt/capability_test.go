// Package jwt holds the descriptor contract test for the jwt capability. The
// payloads under files/ are Go templates for a generated project, so their tests
// are the templates themselves; this test guards the capability descriptor, the
// configuration extension, and the promise that installing jwt guards no route.
package jwt

import (
	"encoding/json"
	"io/fs"
	"path"
	"strings"
	"testing"

	weldtemplate "github.com/Xwudao/weld/internal/weldtemplate"
)

type descriptor struct {
	Name     string   `json:"name"`
	Version  string   `json:"version"`
	Kind     string   `json:"kind"`
	Summary  string   `json:"summary"`
	Requires []string `json:"requires"`
	Files    []struct {
		Path   string   `json:"path"`
		Source string   `json:"source"`
		Role   string   `json:"role"`
		When   []string `json:"when"`
	} `json:"files"`
	Patches []struct {
		Path      string `json:"path"`
		Marker    string `json:"marker"`
		Source    string `json:"source"`
		Bootstrap string `json:"bootstrap"`
	} `json:"patches"`
}

func read(t *testing.T) descriptor {
	t.Helper()
	raw, err := fs.ReadFile(weldtemplate.FS(), "capabilities/jwt/capability.json")
	if err != nil {
		t.Fatalf("read descriptor: %v", err)
	}
	var d descriptor
	if err := json.Unmarshal(raw, &d); err != nil {
		t.Fatalf("invalid descriptor: %v", err)
	}
	return d
}

// TestJWTCapabilityRequiresHTTPWithoutIssuingTokens guards the capability
// contract: it needs http (for the shared envelope and policy seam), ships the
// manager and middleware, and writes no route that would issue a token on its
// own.
func TestJWTCapabilityRequiresHTTPWithoutIssuingTokens(t *testing.T) {
	d := read(t)
	if d.Kind != "add" {
		t.Fatalf("kind = %q, want add", d.Kind)
	}
	if d.Version == "" || d.Summary == "" {
		t.Fatal("jwt needs a version and a summary")
	}
	if strings.Join(d.Requires, ",") != "http" {
		t.Fatalf("requires = %v, want [http]", d.Requires)
	}

	paths := map[string]string{}
	for _, file := range d.Files {
		paths[file.Path] = file.Source
		if strings.Contains(file.Path, "login") || strings.Contains(file.Path, "route") {
			t.Errorf("jwt ships an issuing payload %q; installing it must not issue tokens", file.Path)
		}
	}
	for _, want := range []string{
		"internal/config/jwt.go",
		"internal/config/jwt_test.go",
		"internal/jwtauth/jwtauth.go",
		"internal/jwtauth/jwtauth_test.go",
		"internal/jwtauth/README.md",
		"internal/httpserver/jwt.go",
		"internal/di/jwt_provider.go",
	} {
		if _, ok := paths[want]; !ok {
			t.Errorf("jwt does not ship %s", want)
		}
	}
	var providerSeam bool
	for _, file := range d.Files {
		if file.Path == "internal/di/jwt_provider.go" {
			providerSeam = file.Role == "seam" && len(file.When) == 1 && file.When[0] == "loom"
		}
	}
	if !providerSeam {
		t.Errorf("the Loom provider seam is not a seam guarded by when: [loom]: %+v", d.Files)
	}
}

// TestJWTExtendsConfigurationThroughMarkers proves the typed config section is
// added through the config capability's extension points, that both the local
// file and the committed example receive it, and that the dependency is pinned
// in the go.mod region.
func TestJWTExtendsConfigurationThroughMarkers(t *testing.T) {
	d := read(t)
	markers := map[string]bool{}
	for _, patch := range d.Patches {
		markers[patch.Path+"|"+patch.Marker] = true
		if patch.Path == "config.yml" && patch.Marker == "config" && patch.Bootstrap != "config.example.yml" {
			t.Errorf("config.yml bootstrap = %q, want config.example.yml", patch.Bootstrap)
		}
	}
	for _, want := range []string{
		"go.mod|deps",
		"config.yml|config",
		"config.example.yml|config",
		"internal/config/config.go|configfields",
		"internal/config/config.go|configenv",
		"internal/config/config.go|configdefaults",
	} {
		if !markers[want] {
			t.Errorf("jwt does not patch %s", want)
		}
	}

	deps, err := fs.ReadFile(weldtemplate.FS(), "capabilities/jwt/files/go.mod.snippet")
	if err != nil {
		t.Fatalf("read go.mod snippet: %v", err)
	}
	if !strings.Contains(string(deps), "github.com/golang-jwt/jwt/v5 v5.3.0") {
		t.Errorf("go.mod snippet does not pin golang-jwt:\n%s", deps)
	}

	snippet, err := fs.ReadFile(weldtemplate.FS(), "capabilities/jwt/files/config.snippet")
	if err != nil {
		t.Fatalf("read config snippet: %v", err)
	}
	for _, want := range []string{"jwt:", `secret: ""`, "issuer:", "ttl_seconds:", "leeway_seconds:"} {
		if !strings.Contains(string(snippet), want) {
			t.Errorf("config snippet is missing %q:\n%s", want, snippet)
		}
	}
}

// TestJWTAuthFailsClosed guards the verifier's security rules in the payload:
// one accepted algorithm, an issuer check, an expiry requirement, a secret
// length floor and an envelope-backed 401.
func TestJWTAuthFailsClosed(t *testing.T) {
	auth, err := fs.ReadFile(weldtemplate.FS(), "capabilities/jwt/files/jwtauth.go.tmpl")
	if err != nil {
		t.Fatalf("read jwtauth template: %v", err)
	}
	body := string(auth)
	for _, want := range []string{
		"jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()})",
		"jwt.WithIssuer(m.issuer)",
		"jwt.WithExpirationRequired()",
		"jwt.WithAudience(m.audience)",
		"httpx.Error(w, status, message)",
		"WWW-Authenticate",
		"ErrInvalidToken",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("jwtauth is missing %q", want)
		}
	}
	if strings.Contains(body, "jwt.SigningMethodNone") {
		t.Error("jwtauth accepts the none algorithm")
	}
	if strings.Contains(body, "fmt.Errorf(\"jwtauth: invalid token: %w\"") {
		t.Error("jwtauth exposes the verification failure to the caller")
	}

	cfg, err := fs.ReadFile(weldtemplate.FS(), "capabilities/jwt/files/jwt_config.go.tmpl")
	if err != nil {
		t.Fatalf("read jwt config template: %v", err)
	}
	for _, want := range []string{"MinJWTSecretBytes", "jwt.secret is required", "len(secret) < MinJWTSecretBytes", "func (c *Config) ValidateJWT() error"} {
		if !strings.Contains(string(cfg), want) {
			t.Errorf("jwt config is missing %q", want)
		}
	}

	provider, err := fs.ReadFile(weldtemplate.FS(), "capabilities/jwt/files/jwt_provider.go.tmpl")
	if err != nil {
		t.Fatalf("read provider template: %v", err)
	}
	for _, want := range []string{"func NewJWTAuth(cfg *config.Config) (*jwtauth.Manager, error)", "NewHTTPPolicies"} {
		if !strings.Contains(string(provider), want) {
			t.Errorf("jwt provider is missing %q", want)
		}
	}
	if strings.Contains(string(provider), "jwtauth.New(cfg), nil") {
		t.Error("jwt provider ignores the manager construction error")
	}

	graph, err := fs.ReadFile(weldtemplate.FS(), "capabilities/loom/files/di.go.tmpl")
	if err != nil {
		t.Fatalf("read loom graph: %v", err)
	}
	if !strings.Contains(string(graph), `{{- if .Caps.Has "jwt"}}`) ||
		!strings.Contains(string(graph), "loom.Provide(NewJWTAuth)") {
		t.Error("the Loom graph does not declare the jwt manager binding")
	}
	if strings.Contains(string(graph), "func NewJWTAuth") {
		t.Error("loom defines NewJWTAuth; the provider must live in jwt_provider.go")
	}
}

// TestJWTShipsNoDefaultSecretOrToken proves the capability never invents a
// credential or a pre-signed token: the only secret is an empty YAML value that
// the project must fill in or override from the environment.
func TestJWTShipsNoDefaultSecretOrToken(t *testing.T) {
	for _, source := range []string{
		"capabilities/jwt/files/config.snippet",
		"capabilities/jwt/files/jwt_config.go.tmpl",
		"capabilities/jwt/files/jwt_provider.go.tmpl",
		"capabilities/jwt/files/jwtauth.go.tmpl",
	} {
		raw, err := fs.ReadFile(weldtemplate.FS(), source)
		if err != nil {
			t.Fatalf("read %s: %v", source, err)
		}
		for _, forbidden := range []string{"eyJhbGciOi", "changeme", "secret123"} {
			if strings.Contains(strings.ToLower(string(raw)), forbidden) {
				t.Errorf("%s contains a default credential or token", path.Base(source))
			}
		}
	}
}
