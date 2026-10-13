package httpcontract

import (
	"context"
	"go/format"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestGeneratedClientBuildsAndExercisesTransport(t *testing.T) {
	t.Parallel()
	source := exportFixture(t, fixtureModule, fixtureMounts)
	c, err := Export(context.Background(), source, ExportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	body := Type{Kind: "ref", Name: "WidgetBody"}
	response := Type{Kind: "ref", Name: "WidgetResult"}
	c.Operations = append(c.Operations, Operation{Name: "pointer_update", Module: "widget", Method: "POST", Path: "/api/items/v2/{id}", Binding: "json", Request: &Type{Kind: "pointer", Elem: &body}, Response: &response, Status: 201})
	c.Operations = append(c.Operations, Operation{Name: "head", Method: "HEAD", Path: "/head", Binding: "none", Status: 200, NoContent: true})
	files, err := Generate(c, "sourceclient")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	writeFixture(t, dir, "go.mod", "module example.com/consumer\n\ngo 1.23\n")
	for name, data := range files {
		if strings.Contains(string(data), "example.com/source") || strings.Contains(string(data), "internal/httpx") {
			t.Fatal("client depends on the exporting server")
		}
		writeFixture(t, dir, name, string(data))
	}
	formatted, err := format.Source([]byte(generatedTransportTests))
	if err != nil {
		t.Fatal(err)
	}
	writeFixture(t, dir, "client_test.go", string(formatted))
	writeFixture(t, dir, "errors_test.go", generatedErrorTests)
	cmd := exec.Command("go", "test", "-race", "./...")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOWORK=off")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generated client test: %v\n%s\nfiles in %s", err, out, dir)
	}
	// Generation is deterministic and every output is already gofmt-formatted.
	second, err := Generate(c, "sourceclient")
	if err != nil {
		t.Fatal(err)
	}
	for name, data := range files {
		if string(second[name]) != string(data) {
			t.Fatal("nondeterministic client", name)
		}
		b, err := format.Source(data)
		if err != nil || string(b) != string(data) {
			t.Fatal("unformatted client", name)
		}
	}
}

func TestCheckedInClientExampleMatchesGenerator(t *testing.T) {
	file, err := os.Open("../../examples/http-client/weld-api.json")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	contract, err := Decode(file)
	if err != nil {
		t.Fatal(err)
	}
	files, err := Generate(contract, "producerclient")
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range files {
		got, err := os.ReadFile(filepath.Join("../../examples/http-client/producerclient", name))
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != string(want) {
			t.Fatalf("example %s is stale; regenerate with weld client add", name)
		}
	}
}

const generatedTransportTests = `package sourceclient

import (
 "context"
 "encoding/json"
 "errors"
 "net/http"
 "net/http/httptest"
 "strings"
 "sync/atomic"
 "testing"
 "time"
)

func TestFetchEncodesPathQueryAndHeaders(t *testing.T) {
 created:=time.Date(2026,1,2,3,4,5,0,time.UTC)
 srv:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
  if r.Method!="GET" || r.URL.EscapedPath()!="/api/items/v1/a%2Fb%20%3F%23%25" {t.Errorf("URL: %s %s",r.Method,r.URL.EscapedPath())}
  if r.URL.Query().Get("page")!="0" || strings.Join(r.URL.Query()["tag"],",")!="x,y" {t.Errorf("query: %s",r.URL.RawQuery)}
  if r.Header.Get("Authorization")!="Bearer secret" || r.Header.Get("X-Trace")!="trace" {t.Error("missing configured headers")}
  if r.Header.Get("Accept")!="application/json" || r.Header.Get("Content-Type")!="" {t.Error("incorrect content headers")}
  _ = json.NewEncoder(w).Encode(map[string]any{"code":200,"msg":"success","data":WidgetResult{Name:"Ada",Created:created,Attributes:map[string]string{"key":"value"}}})
 }))
 defer srv.Close()
 client,err:=New(srv.URL,WithHTTPClient(srv.Client()),WithBearerToken("secret"),WithHeader("X-Trace","trace"));if err!=nil{t.Fatal(err)}
 zero:=0;tags:=[]string{"x","y"}
 got,err:=client.Fetch(context.Background(),WidgetQuery{ID:"a/b ?#%",Page:&zero,Tags:&tags})
 if err!=nil || got.Name!="Ada" || !got.Created.Equal(created) || got.Attributes["key"]!="value" {t.Fatalf("response=%+v error=%v",got,err)}
}

func TestJSONPointerAndNoContent(t *testing.T) {
 srv:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
  if r.Method=="DELETE" {w.WriteHeader(204);return}
  if r.URL.EscapedPath()!="/api/items/v2/id%2F9" || r.Header.Get("Content-Type")!="application/json" {t.Errorf("request: %s %v",r.URL.EscapedPath(),r.Header)}
  var body map[string]any
  if err:=json.NewDecoder(r.Body).Decode(&body);err!=nil{t.Error(err)}
  if len(body)!=1 || body["name"]!="widget" {t.Errorf("JSON body=%v",body)}
  w.WriteHeader(201)
  _ = json.NewEncoder(w).Encode(map[string]any{"code":201,"msg":"success","data":WidgetResult{Name:"widget"}})
 }))
 defer srv.Close()
 client,err:=New(srv.URL);if err!=nil{t.Fatal(err)}
 got,err:=client.Update(context.Background(),WidgetBody{ID:"id/9",Name:"widget"})
 if err!=nil || got==nil || got.Name!="widget" {t.Fatalf("response=%+v error=%v",got,err)}
 if _,err:=client.PointerUpdate(context.Background(),&WidgetBody{ID:"id/9",Name:"widget"});err!=nil{t.Fatal(err)}
 if _,err:=client.PointerUpdate(context.Background(),nil);err==nil{t.Fatal("nil request accepted")}
 if err:=client.Clear(context.Background());err!=nil{t.Fatal(err)}
}

func TestErrorsAndResponseValidation(t *testing.T) {
 for _,tc:=range []struct{name string; status int; body string; api bool}{
  {"unauthorized",401,"{\"code\":401,\"msg\":\"authentication required\",\"data\":null}",true},
  {"HTML error",502,"<html>private upstream information</html>",true},
  {"invalid JSON",200,"broken",false},
  {"wrong envelope code",200,"{\"code\":201,\"data\":{}}",false},
  {"missing data",200,"{\"code\":200}",false},
  {"wrong data type",200,"{\"code\":200,\"data\":42}",false},
  {"wrong success status",201,"{\"code\":201,\"data\":{}}",false},
 } {
  t.Run(tc.name,func(t *testing.T){
   srv:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){w.WriteHeader(tc.status);_,_=w.Write([]byte(tc.body))}));defer srv.Close()
   client,err:=New(srv.URL);if err!=nil{t.Fatal(err)}
   _,err=client.Fetch(context.Background(),WidgetQuery{ID:"id"})
   var api *APIError
   if err==nil || errors.As(err,&api)!=tc.api {t.Fatalf("error=%v api=%v",err,api)}
   if tc.api && api.Status!=tc.status{t.Fatal(api)}
   if strings.Contains(err.Error(),"private upstream"){t.Fatal("HTML error body leaked")}
  })
 }
}

func TestCanceledContextMissingPathAndNoRetries(t *testing.T) {
 var calls atomic.Int32
 srv:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){calls.Add(1);w.WriteHeader(503);_,_=w.Write([]byte("unavailable"))}));defer srv.Close()
 client,err:=New(srv.URL);if err!=nil{t.Fatal(err)}
 if _,err:=client.Fetch(context.Background(),WidgetQuery{});err==nil{t.Fatal("missing path accepted")}
 ctx,cancel:=context.WithCancel(context.Background());cancel()
 if _,err:=client.Fetch(ctx,WidgetQuery{ID:"id"});!errors.Is(err,context.Canceled){t.Fatalf("cancel error=%v",err)}
 if calls.Load()!=0{t.Fatal("invalid request reached server")}
 _,err=client.Fetch(context.Background(),WidgetQuery{ID:"id"})
 var api *APIError
 if !errors.As(err,&api) || api.Status!=503 || calls.Load()!=1{t.Fatalf("error=%v requests=%d",err,calls.Load())}
}

func TestResponseLimit(t *testing.T) {
 srv:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){_,_=w.Write([]byte(strings.Repeat(" ",maxResponseBytes+1)))}));defer srv.Close()
 client,err:=New(srv.URL);if err!=nil{t.Fatal(err)}
 if _,err:=client.Fetch(context.Background(),WidgetQuery{ID:"id"});err==nil || !strings.Contains(err.Error(),"exceeds"){t.Fatalf("limit error=%v",err)}
}

func TestHeadAndRedirectPolicy(t *testing.T) {
 var calls atomic.Int32
 srv:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
  calls.Add(1)
  if r.Method=="HEAD" { w.WriteHeader(200);return }
  w.Header().Set("Location","/redirected")
  w.WriteHeader(307)
 }))
 defer srv.Close()
 client,err:=New(srv.URL);if err!=nil{t.Fatal(err)}
 if err:=client.Head(context.Background());err!=nil{t.Fatal(err)}
 _,err=client.Update(context.Background(),WidgetBody{ID:"id",Name:"widget"})
 var api *APIError
 if !errors.As(err,&api) || api.Status!=307 || calls.Load()!=2 {t.Fatalf("redirect error=%v calls=%d",err,calls.Load())}
}

func TestInvalidClientConfiguration(t *testing.T) {
 for _,url:=range []string{"localhost:8080","ftp://example.com","http://example.com?secret=x","http://example.com?","http://:8080","http://example.com/#fragment","http://user:password@example.com"} {
  if _,err:=New(url);err==nil{t.Fatalf("URL %q accepted",url)}
 }
 if _,err:=New("http://example.com",WithHTTPClient(nil));err==nil{t.Fatal("nil HTTP client accepted")}
 if _,err:=New("http://example.com",nil);err==nil{t.Fatal("nil option accepted")}
}
`
