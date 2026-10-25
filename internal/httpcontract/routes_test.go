package httpcontract

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Xwudao/weld/internal/project"
)

// fixtureRouteHTTPX extends the export fixture's toolkit with the raw router
// methods a route table reads.
const fixtureRouteHTTPX = fixtureHTTPX + `
func (r *Router) Post(string,http.HandlerFunc,...Option){}
func (r *Router) Raw(string,string,http.HandlerFunc,...Option){}
func (r *Router) Handle(string,http.Handler,...Option){}
func (r *Router) HandleFunc(string,http.HandlerFunc,...Option){}
`

// fixtureRouteModule mixes typed routes, raw routes in every form, a dynamic
// raw pattern the reader must report instead of guessing, and an anonymous
// typed handler that Export would refuse.
const fixtureRouteModule = "package widget\n" + `
import (
 "context"
 "net/http"
 hx "example.com/source/internal/httpx"
)
const Name = "widget"
func init(){panic("route listing executed package initialization")}
type Service struct{}
type Out struct{ Name string ` + "`json:\"name\"`" + ` }
func handleList(Service) hx.Handler[struct{},[]Out] { return func(context.Context,struct{})([]Out,error){return nil,nil} }
var dynamic = func() string { return "/dyn" }
func Register(router *hx.Router, service Service) {
 admin := router.Group("/admin").Require("admin")
 hx.Get(router, "", handleList(service), hx.Operation("widget_list"))
 hx.Get(admin, "/stats", func(context.Context,struct{})(Out,error){return Out{},nil})
 router.Get("/health", func(http.ResponseWriter,*http.Request){}, hx.Open())
 admin.Post("/reindex", func(http.ResponseWriter,*http.Request){}, hx.Policy("ops"))
 router.Raw(http.MethodOptions, "/raw", func(http.ResponseWriter,*http.Request){})
 router.Handle("PUT /blob/{id}", http.NotFoundHandler())
 router.HandleFunc("/any", func(http.ResponseWriter,*http.Request){})
 router.Get(dynamic(), func(http.ResponseWriter,*http.Request){})
}
`

func routeFixture(t *testing.T, capabilities ...string) string {
	t.Helper()
	dir := t.TempDir()
	writeFixture(t, dir, "go.mod", "module example.com/source\n\ngo 1.23\n")
	m := project.NewManifest("source", "example.com/source", "test")
	for _, name := range capabilities {
		m.AddCapability(project.CapabilityRef{Name: name, Version: "test"})
	}
	m.Modules = []project.ModuleRef{{Name: "widget", Version: "test"}}
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	writeFixture(t, dir, "weld.json", string(data))
	writeFixture(t, dir, "internal/httpx/httpx.go", fixtureRouteHTTPX)
	writeFixture(t, dir, "internal/httpserver/mounts.go", fixtureMounts)
	writeFixture(t, dir, "internal/modules/widget/module.go", fixtureRouteModule)
	return dir
}

func TestListRoutesReadsTypedAndRawRoutesWithoutExecuting(t *testing.T) {
	t.Parallel()
	table, err := ListRoutes(context.Background(), routeFixture(t, "http", "web"))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, route := range table.Routes {
		got = append(got, strings.Join([]string{route.Method, route.Path, route.Kind, route.Operation, strings.Join(route.Policies, "+")}, " "))
	}
	want := []string{
		"GET / mount  ",
		"GET /api/items typed widget_list ",
		"POST /api/items/admin/reindex raw  admin+ops",
		"GET /api/items/admin/stats typed  admin",
		"* /api/items/any raw  ",
		"PUT /api/items/blob/{id} raw  ",
		"GET /api/items/health raw  ",
		"OPTIONS /api/items/raw raw  ",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("routes:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if len(table.Unread) != 1 || !strings.Contains(table.Unread[0], "raw Router.Get") {
		t.Fatalf("unread = %q, want the dynamic raw route", table.Unread)
	}
}

func TestListRoutesRequiresHTTP(t *testing.T) {
	t.Parallel()
	if _, err := ListRoutes(context.Background(), routeFixture(t)); err == nil || !strings.Contains(err.Error(), "http capability is not installed") {
		t.Fatalf("ListRoutes without http = %v", err)
	}
}

func TestListRoutesReportsAnUnreadableModule(t *testing.T) {
	t.Parallel()
	dir := routeFixture(t, "http")
	writeFixture(t, dir, "internal/modules/widget/module.go", "package widget\n"+`
import hx "example.com/source/internal/httpx"
const Name = "widget"
func Register(router *hx.Router) {
 for _, p := range []string{"/a"} { router.Get(p, nil) }
}
`)
	table, err := ListRoutes(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(table.Routes) != 0 || len(table.Unread) != 1 || !strings.Contains(table.Unread[0], "widget:") {
		t.Fatalf("table = %+v, want the module reported as unread", table)
	}
}
