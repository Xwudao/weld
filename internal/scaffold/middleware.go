package scaffold

import (
	"fmt"
	"go/token"
	"path/filepath"

	"github.com/Xwudao/weld/internal/project"
	"github.com/Xwudao/weld/internal/template"
)

// middlewareDir is the package `weld add middleware` writes into, relative to
// the project root. It is a package of its own rather than internal/httpserver
// so a generated constructor can never collide with an identifier the http
// capability (or a later capability) ships there.
const middlewareDir = "internal/middleware"

// ValidateMiddlewareName reports why a middleware name is unusable. The name is
// both the <name>.go file and the exported <Name> constructor, so it must be a
// valid, lower-case Go identifier.
func ValidateMiddlewareName(name string) error {
	if !moduleNamePattern.MatchString(name) {
		return fmt.Errorf("invalid middleware name %q: use lowercase letters and digits starting with a letter (a valid Go identifier)", name)
	}
	if token.IsKeyword(name) {
		return fmt.Errorf("invalid middleware name %q: it is a Go keyword", name)
	}
	return nil
}

// AddMiddleware plans a new HTTP middleware named name: the
// internal/middleware/<name>.go constructor and a hermetic test beside it.
//
// The files are project-owned seams, written once and never rewritten. The
// middleware is deliberately not wired: the stack lives in the project-owned
// internal/httpserver/middleware.go, which carries no marker region weld may
// patch, so the plan notes the one line to add there instead of editing it.
//
// The http capability (and its loom/config chain) must be installed first; a
// missing requirement is an error unless Request.WithDependencies is set.
func AddMiddleware(req Request, name string) (*Result, error) {
	if err := ValidateMiddlewareName(name); err != nil {
		return nil, err
	}
	manifest, err := project.Load(req.Dir)
	if err != nil {
		return nil, err
	}

	path := filepath.ToSlash(filepath.Join(middlewareDir, name+".go"))
	testPath := filepath.ToSlash(filepath.Join(middlewareDir, name+"_test.go"))
	result := &Result{Dir: req.Dir}
	if manifest.Owns(path) {
		result.Notes = []string{fmt.Sprintf("middleware %s is already installed", name)}
		return result, nil
	}

	p := newPlanner(req, manifest, result)
	if err := p.requireCapability("http", fmt.Sprintf("middleware %q", name)); err != nil {
		return nil, err
	}

	owner := "middleware:" + name
	vars := template.Vars{Name: manifest.Name, Module: manifest.Module}
	if err := p.planNewFile(path, owner, "", project.RoleSeam, template.Render(middlewareFile(name), vars)); err != nil {
		return nil, err
	}
	if err := p.planNewFile(testPath, owner, "", project.RoleSeam, middlewareTestFile(name)); err != nil {
		return nil, err
	}
	if err := p.finish(); err != nil {
		return nil, err
	}
	result.Notes = append(result.Notes, fmt.Sprintf(
		"middleware %s is not wired yet: add middleware.%s() to the list middlewareChain returns in internal/httpserver/middleware.go (import %q)",
		name, template.ExportName(name), manifest.Module+"/"+middlewareDir))
	return result, nil
}

// middlewareFile renders the <name>.go constructor. Like a seeder it is built in
// code: it has no capability-dependent shape, only the identifiers change.
func middlewareFile(name string) []byte {
	exported := template.ExportName(name)
	return []byte(fmt.Sprintf(`package middleware

import (
	"net/http"

	"__module__/internal/httpx"
)

// %s returns the %s HTTP middleware. It is project-owned: weld writes it once
// and never rewrites it.
//
// Wire it by adding middleware.%s() to the list middlewareChain returns in
// internal/httpserver/middleware.go; the list is applied outer-to-inner, so its
// position decides what it sees. A route-specific check belongs in a named
// policy (internal/di/http_policies.go) instead. Take the dependencies it needs
// (configuration, a logger, a service) as parameters here and pass them from
// middlewareChain.
func %s() httpx.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r)
		})
	}
}
`, exported, name, exported, exported))
}

// middlewareTestFile renders the hermetic test for the <name>.go constructor.
func middlewareTestFile(name string) []byte {
	exported := template.ExportName(name)
	return []byte(fmt.Sprintf(`package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// Test%sCallsNext proves the middleware passes the request on and keeps the
// inner handler's response. Extend it when the middleware gains behavior.
func Test%sCallsNext(t *testing.T) {
	called := false
	handler := %s()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	}))

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))

	if !called {
		t.Fatal("the inner handler was not called")
	}
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %%d, want %%d", recorder.Code, http.StatusNoContent)
	}
}
`, exported, exported, exported))
}
