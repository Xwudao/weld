package httpcontract

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Xwudao/weld/internal/genenum"
)

func enhancedDTOFixture(t *testing.T) string {
	t.Helper()
	source := strings.Replace(fixtureModule, `"time"`, `"time"
 "encoding/json"
 en "example.com/source/internal/enums"`, 1)
	source = strings.Replace(source, "type Result struct {", `type Base struct { Label string; Shadow string }
type Left struct { Conflict string }
type Right struct { Conflict string }
type Extra string
type Result struct {
 *Base
 Left
 Right
 Extra `+"`json:\",omitempty\"`"+`
 Shadow string
 State en.Status
 ByState map[en.Status]string
 ByID map[int]string
 Payload json.RawMessage
 Slots [2]int
 Ignored chan int `+"`json:\"-\"`", 1)
	source = strings.Replace(source, "type Body struct {", `type Body struct { State en.Status; Payload json.RawMessage; Slots [2]int; *Base; Extra`, 1)
	source = strings.Replace(source, "type Query struct {", `type Query struct { State en.Status `+"`query:\"state\"`"+`; States []en.Status `+"`query:\"states\"`", 1)
	dir := exportFixture(t, source, fixtureMounts)
	writeFixture(t, dir, "internal/enums/status.go", `package enums
func init(){panic("enum init executed")}
type Status string
const (
 StatusDraft Status = "draft"
 StatusLive Status = "live"
)
`)
	pkg, err := genenum.LoadDir(filepath.Join(dir, "internal/enums"))
	if err != nil {
		t.Fatal(err)
	}
	data, err := genenum.Render(pkg, pkg.Enums[0])
	if err != nil {
		t.Fatal(err)
	}
	writeFixture(t, dir, "internal/enums/status_gen.go", string(data))
	return dir
}

func TestEnhancedDTOExportAndIndependentClient(t *testing.T) {
	t.Parallel()
	dir := enhancedDTOFixture(t)
	c, err := Export(context.Background(), dir, ExportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if c.Version != 2 {
		t.Fatal("new shapes must use version 2")
	}
	var enum bool
	for _, d := range c.Types {
		if d.Name == "EnumsStatus" {
			enum = d.Type.Kind == "enum" && len(d.Type.Values) == 2
		}
	}
	if !enum {
		t.Fatal("enum contract missing")
	}
	data, err := c.JSON()
	if err != nil {
		t.Fatal(err)
	}
	c, err = Decode(strings.NewReader(string(data)))
	if err != nil {
		t.Fatal(err)
	}
	files, err := Generate(c, "sourceclient")
	if err != nil {
		t.Fatal(err)
	}
	consumer := t.TempDir()
	writeFixture(t, consumer, "go.mod", "module example.com/consumer\n\ngo 1.23\n")
	for name, data := range files {
		writeFixture(t, consumer, name, string(data))
	}
	writeFixture(t, consumer, "dto_test.go", generatedDTOTests)
	cmd := exec.Command("go", "test", "-race", "./...")
	cmd.Dir = consumer
	cmd.Env = append(os.Environ(), "GOWORK=off")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generated DTO client: %v\n%s", err, out)
	}
}

func TestModifiedEnumCodecIsRejected(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"modified text codec", "additional JSON codec"} {
		t.Run(mode, func(t *testing.T) {
			dir := enhancedDTOFixture(t)
			path := filepath.Join(dir, "internal/enums/status_gen.go")
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "modified text codec" {
				data = []byte(strings.Replace(string(data), "return []byte(string(v)), nil", "return []byte(\"changed\"), nil", 1))
			} else {
				writeFixture(t, dir, "internal/enums/status_json.go", "package enums\nfunc (v Status) MarshalJSON()([]byte,error){return []byte(`123`),nil}\n")
			}
			if err := os.WriteFile(path, data, 0o644); err != nil {
				t.Fatal(err)
			}
			_, err = Export(context.Background(), dir, ExportOptions{})
			if err == nil || !strings.Contains(err.Error(), "custom serialization") {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestExpandedContractValidation(t *testing.T) {
	for _, shape := range []Type{
		{Kind: "map", Key: &Type{Kind: "basic", Name: "float64"}, Elem: &Type{Kind: "basic", Name: "string"}},
		{Kind: "array", Len: -1, Elem: &Type{Kind: "basic", Name: "string"}},
		{Kind: "array", Len: 1 << 21, Elem: &Type{Kind: "basic", Name: "string"}},
		{Kind: "array", Len: 1},
		{Kind: "enum"},
		{Kind: "enum", Values: []EnumValue{{Name: "A", Value: "x"}, {Name: "B", Value: "x"}}},
		{Kind: "struct", Fields: []Field{{Name: "Bad", Embedded: true, Type: Type{Kind: "basic", Name: "string"}}}},
	} {
		c := simpleContract()
		c.Types = []Definition{{Name: "Shape", Type: shape}}
		if err := c.Validate(); err == nil {
			t.Fatalf("invalid shape accepted: %+v", shape)
		}
	}
	c := simpleContract()
	c.Types = []Definition{{Name: "State", Type: Type{Kind: "enum", Values: []EnumValue{{Name: "New", Value: "x"}}}}}
	if err := c.Validate(); err == nil {
		t.Fatal("enum symbol collision accepted")
	}
	c = simpleContract()
	c.Version = 1
	data, _ := json.Marshal(c)
	if _, err := Decode(strings.NewReader(string(data))); err != nil {
		t.Fatal("old snapshot no longer accepted", err)
	}
}

const generatedDTOTests = `package sourceclient
import (
 "context"
 "encoding/json"
 "errors"
 "net/http"
 "net/http/httptest"
 "strings"
 "sync/atomic"
 "testing"
)

func TestDTOJSONShapeAndEnumCodec(t *testing.T){
 value:=WidgetResult{WidgetBase:&WidgetBase{Label:"label",Shadow:"hidden"},WidgetLeft:WidgetLeft{Conflict:"left"},WidgetRight:WidgetRight{Conflict:"right"},WidgetExtra:"extra",Shadow:"visible",State:EnumsStatusLive,ByState:map[EnumsStatus]string{EnumsStatusDraft:"draft value"},ByID:map[int]string{42:"answer"},Payload:json.RawMessage("{\"nested\":true}"),Slots:[2]int{3,4}}
 data,err:=json.Marshal(value);if err!=nil{t.Fatal(err)}
 var shape map[string]json.RawMessage;if err:=json.Unmarshal(data,&shape);err!=nil{t.Fatal(err)}
 if string(shape["Label"])!="\"label\"" || string(shape["Shadow"])!="\"visible\"" || string(shape["Extra"])!="\"extra\"" || string(shape["State"])!="\"live\"" || string(shape["Payload"])!="{\"nested\":true}" || string(shape["Slots"])!="[3,4]"{t.Fatalf("wire shape=%s",data)}
 if _,ok:=shape["Conflict"];ok{t.Fatal("ambiguous JSON field should be omitted")}
 if _,ok:=shape["Ignored"];ok{t.Fatal("ignored channel was copied")}
 var decoded WidgetResult
 if err:=json.Unmarshal(data,&decoded);err!=nil || decoded.WidgetBase==nil || decoded.Label!="label" || decoded.WidgetExtra!="extra" || decoded.State!=EnumsStatusLive || decoded.Slots[1]!=4 || decoded.ByID[42]!="answer" || decoded.ByState[EnumsStatusDraft]!="draft value"{t.Fatalf("round trip=%+v %v",decoded,err)}
 if len(EnumsStatusValues())!=2 || !EnumsStatusLive.IsValid(){t.Fatal("enum helpers missing")}
 if _,err:=ParseEnumsStatus("unknown");err==nil{t.Fatal("unknown enum accepted")}
 if err:=json.Unmarshal([]byte("\"unknown\""),new(EnumsStatus));err==nil{t.Fatal("unknown enum JSON accepted")}
}

func TestExtendedDTOTransportAndClassification(t *testing.T){
 var calls atomic.Int32
 srv:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
  calls.Add(1)
  if r.Method=="GET"{if r.URL.Query().Get("state")!="live" || strings.Join(r.URL.Query()["states"],",")!="draft,live"{t.Error(r.URL.RawQuery)}}else{
   var body WidgetBody;if err:=json.NewDecoder(r.Body).Decode(&body);err!=nil || body.State!=EnumsStatusLive || body.Slots[1]!=4 || body.Label!="label"{t.Errorf("body=%+v %v",body,err)}
   w.WriteHeader(201)
  }
  code:=200;if r.Method=="POST"{code=201}
  _=json.NewEncoder(w).Encode(map[string]any{"code":code,"data":WidgetResult{State:EnumsStatusLive,Payload:json.RawMessage("[1,2]"),Slots:[2]int{3,4},WidgetBase:&WidgetBase{Label:"label"}}})
 }));defer srv.Close()
 client,_:=New(srv.URL)
 result,err:=client.Fetch(context.Background(),WidgetQuery{ID:"id",State:EnumsStatusLive,States:[]EnumsStatus{EnumsStatusDraft,EnumsStatusLive}})
 if err!=nil || result.State!=EnumsStatusLive || result.Label!="label" || string(result.Payload)!="[1,2]"{t.Fatalf("response=%+v %v",result,err)}
 _,err=client.Update(context.Background(),WidgetBody{ID:"id",State:EnumsStatusLive,Payload:json.RawMessage("{}"),Slots:[2]int{3,4},WidgetBase:&WidgetBase{Label:"label"}});if err!=nil{t.Fatal(err)}
 _,err=client.Update(context.Background(),WidgetBody{ID:"id",Payload:json.RawMessage("invalid")})
 var local *RequestError;if !errors.As(err,&local) || local.Stage!="encode" || calls.Load()!=2{t.Fatalf("encoding error=%v calls=%d",err,calls.Load())}
}
`
