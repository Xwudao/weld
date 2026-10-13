package httpcontract

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Xwudao/weld/internal/project"
)

const fixtureHTTPX = `package httpx
import (
 "context"
 "net/http"
)
type Router struct{}
func (r *Router) Group(string, ...func(http.Handler)http.Handler)*Router{return r}
func (r *Router) Require(...string)*Router{return r}
func (r *Router) Get(string,http.HandlerFunc,...Option){}
type Option func()
func Operation(string)Option{return nil}
func WithStatus(int)Option{return nil}
func WithNoContent()Option{return nil}
func WithMaxBytes(int64)Option{return nil}
func Policy(...string)Option{return nil}
func Open()Option{return nil}
type Handler[Req,Resp any] func(context.Context,Req)(Resp,error)
func Get[Req,Resp any](*Router,string,Handler[Req,Resp],...Option){}
func Post[Req,Resp any](*Router,string,Handler[Req,Resp],...Option){}
func NoInput[Resp any](*Router,string,string,func(context.Context)(Resp,error),...Option){}
`

const fixtureModule = "package widget\n" + `
import (
 "context"
 "net/http"
 "time"
 hx "example.com/source/internal/httpx"
)
const Name = "widget"
// Neither init nor constructors are permitted to run during export.
func init(){panic("export executed package initialization")}
type Service struct{}
func NewService()Service{panic("export executed constructor")}
type Query struct {
 ID string ` + "`path:\"id\"`" + `
 Page *int ` + "`query:\"page\"`" + `
 Tags *[]string ` + "`query:\"tag\"`" + `
}
type Body struct {
 ID string ` + "`path:\"id\" json:\"-\"`" + `
 Name string ` + "`json:\"name\"`" + `
}
type Result struct {
 Name string ` + "`json:\"name\"`" + `
 Created time.Time ` + "`json:\"created\"`" + `
 Children []*Result ` + "`json:\"children,omitempty\"`" + `
 Attributes map[string]string ` + "`json:\"attributes\"`" + `
}
func handleFetch(Service) hx.Handler[Query,Result] { return func(context.Context,Query)(Result,error){return Result{},nil} }
func handleUpdate(Service) hx.Handler[Body,*Result] { return func(context.Context,Body)(*Result,error){return nil,nil} }
func Register(router *hx.Router, service Service) {
 first := router.Group("/v1").Require("admin")
 router = router.Group("/v2")
 hx.Get(first, "/{id}", handleFetch(service), hx.Operation("fetch"))
 hx.Post(router, "/{id}", handleUpdate(service), hx.Operation("update"), hx.WithStatus(http.StatusCreated))
 hx.NoInput(router, http.MethodDelete, "", func(context.Context)(struct{},error){return struct{}{},nil},hx.Operation("clear"),hx.WithNoContent())
 router.Get("/raw",func(http.ResponseWriter,*http.Request){})
}
`

func writeFixture(t *testing.T, dir, path, source string) {
	t.Helper()
	absolute := filepath.Join(dir, path)
	if err := os.MkdirAll(filepath.Dir(absolute), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(absolute, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
}

func exportFixture(t *testing.T, source, prefix string) string {
	t.Helper()
	dir := t.TempDir()
	writeFixture(t, dir, "go.mod", "module example.com/source\n\ngo 1.23\n")
	m := project.NewManifest("source", "example.com/source", "test")
	m.Modules = []project.ModuleRef{{Name: "widget", Version: "test"}}
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	writeFixture(t, dir, "weld.json", string(data))
	writeFixture(t, dir, "internal/httpx/httpx.go", fixtureHTTPX)
	writeFixture(t, dir, "internal/httpserver/mounts.go", prefix)
	writeFixture(t, dir, "internal/modules/widget/module.go", source)
	return dir
}

const fixtureMounts = `package httpserver
const APIPrefix="/api"
func ModulePrefix(name string)string {
 switch name {
 case "widget":return APIPrefix+"/items"
 default:return APIPrefix+"/"+name
 }
}
`

func TestExportStaticRoutesAndTypesWithoutExecutingApplication(t *testing.T) {
	t.Parallel()
	dir := exportFixture(t, fixtureModule, fixtureMounts)
	c, err := Export(context.Background(), dir, ExportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Operations) != 3 || len(c.Types) != 3 || len(c.Skipped) != 1 {
		t.Fatalf("contract: %+v", c)
	}
	ops := map[string]Operation{}
	for _, op := range c.Operations {
		ops[op.Name] = op
	}
	if ops["fetch"].Path != "/api/items/v1/{id}" || strings.Join(ops["fetch"].Policies, ",") != "admin" {
		t.Fatalf("fetch: %+v", ops["fetch"])
	}
	if ops["update"].Path != "/api/items/v2/{id}" || ops["update"].Status != 201 || ops["update"].Response.Kind != "pointer" {
		t.Fatalf("update: %+v", ops["update"])
	}
	if ops["clear"].Status != 204 || !ops["clear"].NoContent || ops["clear"].Request != nil {
		t.Fatalf("clear: %+v", ops["clear"])
	}
	data, err := c.JSON()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), dir) {
		t.Fatal("portable contract leaks the local absolute source path")
	}
	second, err := Export(context.Background(), dir, ExportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	other, _ := second.JSON()
	if string(data) != string(other) {
		t.Fatal("export is nondeterministic")
	}
	if _, err := Generate(c, "sourceclient"); err != nil {
		t.Fatal(err)
	}
}

func TestExportRejectsUnsupportedSource(t *testing.T) {
	cases := []struct{ name, source, want string }{
		{"dynamic routes", strings.Replace(fixtureModule, `hx.Get(first, "/{id}", handleFetch(service), hx.Operation("fetch"))`, `if time.Now().Unix()>0 { hx.Get(first, "/{id}", handleFetch(service), hx.Operation("fetch")) }`, 1), "dynamic Register control flow"},
		{"custom serialization", strings.Replace(fixtureModule, "func handleFetch", `func (r Result) MarshalJSON()([]byte,error){return nil,nil}
func handleFetch`, 1), "custom serialization"},
		{"anonymous embedded DTO", strings.Replace(fixtureModule, "Name string `json:\"name\"`\n Created", "Body\n Name string `json:\"name\"`\n Created", 1), "embedded DTO field"},
		{"unknown operation expression", strings.Replace(fixtureModule, `hx.Operation("fetch")`, `hx.Operation(time.Now().String())`, 1), "not statically evaluable"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := exportFixture(t, tc.source, fixtureMounts)
			_, err := Export(context.Background(), dir, ExportOptions{})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error=%v want %q", err, tc.want)
			}
		})
	}
}

func TestExportPrefixOverrideAndOperationFallback(t *testing.T) {
	t.Parallel()
	source := strings.Replace(fixtureModule, `, hx.Operation("fetch")`, "", 1)
	dir := exportFixture(t, source, `package httpserver
import "os"
func ModulePrefix(name string)string{return os.Getenv("PREFIX")+name}
`)
	if _, err := Export(context.Background(), dir, ExportOptions{}); err == nil || !strings.Contains(err.Error(), "--prefix") {
		t.Fatalf("dynamic mount error=%v", err)
	}
	prefix := "/external"
	c, err := Export(context.Background(), dir, ExportOptions{Module: "widget", Prefix: &prefix})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, op := range c.Operations {
		if op.Name == "widget_Fetch" {
			found = true
			if op.Path != "/external/v1/{id}" {
				t.Fatal(op.Path)
			}
		}
	}
	if !found {
		t.Fatal("named handler did not receive fallback operation ID")
	}
}
