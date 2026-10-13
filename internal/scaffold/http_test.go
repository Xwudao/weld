package scaffold

import (
	"go/format"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Keep this default-policy test in the generator's suite, not in generated
// projects: a project should be able to replace its policy without rewriting
// a generated test that assumes the default deny-all implementation.
func TestHTTPDefaultAdminPolicyFailsClosed(t *testing.T) {
	t.Parallel()
	loomToolAvailable(t)
	dir := create(t, t.TempDir())
	add(t, dir, "http")
	mod := strings.Fields(readFile(t, filepath.Join(dir, "go.mod")))[1]
	source := strings.ReplaceAll(`package di

import (
 "net/http"
 "net/http/httptest"
 "testing"

 "__module__/internal/httpserver"
 "__module__/internal/httpx"
)

func TestDefaultAdminPolicyDeniesEveryToken(t *testing.T) {
 policies, err := NewHTTPPolicies()
 if err != nil { t.Fatal(err) }
 handler := httpserver.NewHandler(nil, nil, policies, func(router *httpx.Router) {
  router.Get("/admin", func(http.ResponseWriter, *http.Request) {
   t.Error("unconfigured administrator policy allowed access")
  }, httpx.Policy(httpserver.PolicyAdmin))
 })
 for _, token := range []string{"", "Bearer fake", "Bearer test-token"} {
  req := httptest.NewRequest(http.MethodGet, "/admin", nil)
  req.Header.Set("Authorization", token)
  w := httptest.NewRecorder()
  handler.ServeHTTP(w, req)
  if w.Code != http.StatusForbidden { t.Fatalf("token %q: %d", token, w.Code) }
 }
}
`, "__module__", mod)
	formatted, err := format.Source([]byte(source))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "internal/di/default_policy_test.go"), formatted, 0o644); err != nil {
		t.Fatal(err)
	}
	if !goModTidy(t, dir) {
		t.Skip("generated dependencies unavailable")
	}
	runGo(t, dir, "test", "-race", "./internal/httpserver", "./internal/httpx", "./internal/di")
}
