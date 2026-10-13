package scaffold

import (
	"context"
	"go/format"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Xwudao/weld/internal/httpcontract"
)

// An actual generated server calls a client generated into a separate Go
// module. No server DTOs or internal implementation packages cross the boundary.
func TestHTTPGeneratedClientAcrossProjects(t *testing.T) {
	t.Parallel()
	loomToolAvailable(t)
	dir := create(t, t.TempDir())
	addModule(t, dir, "widget")
	servicePath := filepath.Join(dir, "internal/modules/widget/service.go")
	service := readFile(t, servicePath)
	service = strings.Replace(service, "func NewService() Service {\n\treturn NewDemoService()\n}", "func NewService() Service { panic(\"export must not initialize services\") }", 1)
	if err := os.WriteFile(servicePath, []byte(service), 0o644); err != nil {
		t.Fatal(err)
	}
	contract, err := httpcontract.Export(context.Background(), dir, httpcontract.ExportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	clientDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(clientDir, "go.mod"), []byte("module example.com/consumer\n\ngo 1.23\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	files, err := httpcontract.Generate(contract, "catalog")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(clientDir, "catalog"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(clientDir, "catalog", name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	runGo(t, dir, "mod", "edit", "-require=example.com/consumer@v0.0.0", "-replace=example.com/consumer="+clientDir)
	mod := strings.Fields(readFile(t, filepath.Join(dir, "go.mod")))[1]
	source := strings.ReplaceAll(actualServerClientTests, "__module__", mod)
	formatted, err := format.Source([]byte(source))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "internal/httpserver/client_integration_test.go"), formatted, 0o644); err != nil {
		t.Fatal(err)
	}
	if !goModTidy(t, dir) {
		t.Skip("generated dependencies unavailable")
	}
	runGo(t, dir, "test", "-race", "./internal/httpserver")
}

const actualServerClientTests = `package httpserver_test

import (
 "context"
 "errors"
 "net/http"
 "net/http/httptest"
 "strings"
 "testing"

 "example.com/consumer/catalog"
 "__module__/internal/httpserver"
 "__module__/internal/httpx"
 "__module__/internal/modules/widget"
)

func TestGeneratedClientCallsActualTypedHandlers(t *testing.T) {
 guard:=func(next http.Handler)http.Handler{return http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
  if r.Header.Get("Authorization")!="Bearer token" {httpx.Error(w,401,"authentication required");return}
  next.ServeHTTP(w,r)
 })}
 handler:=httpserver.NewHandler(nil,nil,[]httpserver.NamedPolicy{{Name:httpserver.PolicyAdmin,Middleware:guard}},func(router *httpx.Router){
  widget.Register(router.Group(httpserver.ModulePrefix(widget.Name)),widget.NewDemoService())
 })
 server:=httptest.NewServer(handler);defer server.Close()
 client,err:=catalog.New(server.URL,catalog.WithHTTPClient(server.Client()),catalog.WithBearerToken("token"));if err!=nil{t.Fatal(err)}
 response,err:=client.WidgetGreet(context.Background(),catalog.WidgetRequest{Name:"Ada"})
 if err!=nil || !strings.Contains(response.Message,"Ada") || response.Module!="widget" {t.Fatalf("greet=%+v error=%v",response,err)}
 item,err:=client.WidgetCreateItem(context.Background(),catalog.WidgetCreateRequest{Name:"widget"})
 if err!=nil || item.ID!="widget-1" || item.Name!="widget" {t.Fatalf("item=%+v error=%v",item,err)}
 if _,err:=client.WidgetIndex(context.Background());err!=nil{t.Fatal(err)}
 _,err=client.WidgetCreateItem(context.Background(),catalog.WidgetCreateRequest{Name:" "})
 var api *catalog.APIError
 if !errors.As(err,&api) || api.Status!=400 {t.Fatalf("validation error=%v",err)}
 anonymous,err:=catalog.New(server.URL);if err!=nil{t.Fatal(err)}
 _,err=anonymous.WidgetCreateItem(context.Background(),catalog.WidgetCreateRequest{Name:"widget"})
 if !errors.As(err,&api) || api.Status!=401 {t.Fatalf("authorization error=%v",err)}
 ctx,cancel:=context.WithCancel(context.Background());cancel()
 if _,err:=client.WidgetIndex(ctx);!errors.Is(err,context.Canceled){t.Fatalf("cancel error=%v",err)}
}
`
