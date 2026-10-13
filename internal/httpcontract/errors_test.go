package httpcontract

// These tests run inside the separately generated consumer module, so the
// runtime error API is exercised without importing the producer implementation.
const generatedErrorTests = `package sourceclient

import (
 "context"
 "encoding/json"
 "errors"
 "io"
 "net/http"
 "net/http/httptest"
 "strings"
 "testing"
)

func TestStructuredAPIErrorDetailsAndCorrelation(t *testing.T) {
 for _,tc:=range []struct{name,body,header,id string;code int}{
  {"details", "{\"code\":10001,\"msg\":\"invalid name\",\"details\":{\"field\":\"name\"},\"data\":{\"reason\":\"short\"},\"request_id\":\"body-id\"}","header-id","header-id",10001},
  {"data fallback", "{\"code\":400,\"msg\":\"invalid name\",\"data\":{\"field\":\"name\"},\"requestId\":\"body-id\"}","","body-id",400},
  {"zero code", "{\"code\":0,\"msg\":\"invalid name\",\"details\":{\"field\":\"name\"}}","","",0},
 } {t.Run(tc.name,func(t *testing.T){
  srv:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){w.Header().Set("X-Request-Id",tc.header);w.Header().Set("Retry-After","5");w.WriteHeader(400);_,_=io.WriteString(w,tc.body)}));defer srv.Close()
  client,_:=New(srv.URL)
  _,err:=client.Fetch(context.Background(),WidgetQuery{ID:"private-id",Tags:&[]string{"private-query"}})
  var api *APIError
  if !errors.As(err,&api) || api.Status!=400 || api.Code!=tc.code || api.Message!="invalid name" || api.RequestID!=tc.id || api.Operation!="fetch" || api.Method!="GET" || api.Route!="/api/items/v1/{id}" || api.Headers.Get("Retry-After")!="5"{t.Fatalf("error=%+v",api)}
  var details map[string]string;if err:=json.Unmarshal(api.Details,&details);err!=nil || details["field"]!="name"{t.Fatalf("details=%s %v",api.Details,err)}
  if strings.Contains(err.Error(),"private-id") || strings.Contains(err.Error(),"private-query") || strings.Contains(err.Error(),"short"){t.Fatal("sensitive request/details leaked into error string")}
 })}
}

func TestProtocolErrorsPreserveCauseAndMetadata(t *testing.T) {
 for _,tc:=range []struct{name,body,stage string;status int}{
  {"syntax","private-not-json","envelope",200},
  {"code mismatch","{\"code\":201,\"data\":{}}","envelope",200},
  {"null struct","{\"code\":200,\"data\":null}","data",200},
  {"bad data","{\"code\":200,\"data\":{\"created\":\"private-not-a-time\"}}","data",200},
  {"status","{\"code\":201,\"data\":{}}","status",201},
 } {t.Run(tc.name,func(t *testing.T){
  srv:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){w.Header().Set("X-Request-Id","request-1");w.WriteHeader(tc.status);_,_=io.WriteString(w,tc.body)}));defer srv.Close()
  client,_:=New(srv.URL);_,err:=client.Fetch(context.Background(),WidgetQuery{ID:"id"})
  var protocol *ProtocolError
  if !errors.As(err,&protocol) || protocol.Stage!=tc.stage || protocol.Status!=tc.status || protocol.ExpectedStatus!=200 || protocol.RequestID!="request-1" || protocol.Operation!="fetch"{t.Fatalf("protocol=%+v error=%v",protocol,err)}
  if strings.Contains(err.Error(),"private-"){t.Fatal("response/decoder contents leaked")}
  if tc.name=="syntax"{var syntax *json.SyntaxError;if !errors.As(err,&syntax){t.Fatal("decoder cause lost")}}
 })}
 // A pointer response is nullable; do not reject the producer's valid null.
 srv:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){w.WriteHeader(201);_,_=io.WriteString(w,"{\"code\":201,\"data\":null}")}));defer srv.Close()
 client,_:=New(srv.URL);value,err:=client.Update(context.Background(),WidgetBody{ID:"id"});if err!=nil || value!=nil{t.Fatalf("nullable response=%v %v",value,err)}
}

type errorRoundTripper func(*http.Request)(*http.Response,error)
func (f errorRoundTripper) RoundTrip(r *http.Request)(*http.Response,error){return f(r)}
type failingResponseBody struct{}
func (failingResponseBody) Read([]byte)(int,error){return 0,io.ErrUnexpectedEOF}
func (failingResponseBody) Close()error{return nil}
type deadlineBody struct{}
func (deadlineBody) Read([]byte)(int,error){return 0,context.DeadlineExceeded}
func (deadlineBody) Close()error{return nil}

func TestTransportErrorsAndAPIStatusAreNeverLost(t *testing.T){
 for _,status:=range []int{200,503}{
  client,_:=New("http://example.com",WithHTTPClient(&http.Client{Transport:errorRoundTripper(func(*http.Request)(*http.Response,error){return &http.Response{StatusCode:status,Header:http.Header{"X-Request-Id":[]string{"request-1"}},Body:failingResponseBody{}},nil})}))
  _,err:=client.Fetch(context.Background(),WidgetQuery{ID:"id"})
  var transport *TransportError
  if !errors.As(err,&transport) || !errors.Is(err,io.ErrUnexpectedEOF) || transport.Stage!="read" || transport.Status!=status || transport.RequestID!="request-1"{t.Fatalf("error=%v transport=%+v",err,transport)}
  var api *APIError;if (status==503)!=errors.As(err,&api){t.Fatalf("HTTP error classification=%v",err)}
  if api!=nil && (api.Status!=503 || api.RequestID!="request-1"){t.Fatal("API metadata lost")}
 }
 client,_:=New("http://example.com",WithHTTPClient(&http.Client{Transport:errorRoundTripper(func(r *http.Request)(*http.Response,error){return nil,context.DeadlineExceeded})}))
 _,err:=client.Fetch(context.Background(),WidgetQuery{ID:"id"})
 var transport *TransportError;if !errors.As(err,&transport) || !transport.Timeout() || !errors.Is(err,context.DeadlineExceeded){t.Fatalf("timeout=%v",err)}
 ctx,cancel:=context.WithCancel(context.Background());cancel()
 client,_=New("http://example.com")
 _,err=client.Fetch(ctx,WidgetQuery{ID:"id"})
 if !errors.As(err,&transport) || !errors.Is(err,context.Canceled) || transport.Timeout(){t.Fatalf("cancel=%v",err)}
 _,err=client.Fetch(context.Background(),WidgetQuery{})
 var local *RequestError;if !errors.As(err,&local) || local.Stage!="bind" || local.Operation!="fetch"{t.Fatalf("binding=%v",err)}
 // A context deadline during body reading must still be discoverable.
 client,_=New("http://example.com",WithHTTPClient(&http.Client{Transport:errorRoundTripper(func(*http.Request)(*http.Response,error){return &http.Response{StatusCode:200,Header:make(http.Header),Body:deadlineBody{}},nil})}))
 _,err=client.Fetch(context.Background(),WidgetQuery{ID:"id"})
 if !errors.As(err,&transport) || transport.Stage!="read" || !transport.Timeout() || !errors.Is(err,context.DeadlineExceeded){t.Fatalf("read timeout=%v",err)}
}

func TestAPIErrorsKeepStatusOnOversizedAndMalformedBodies(t *testing.T){
 for _,body:=range []string{"<html>private-body</html>","{\"msg\":\"private-body\"}",strings.Repeat("x",maxResponseBytes+1)}{
  srv:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){w.Header().Set("X-Request-Id","request-1");w.WriteHeader(502);_,_=io.WriteString(w,body)}))
  client,_:=New(srv.URL);_,err:=client.Fetch(context.Background(),WidgetQuery{ID:"id"})
  var api *APIError
  if !errors.As(err,&api) || api.Status!=502 || api.Message!="Bad Gateway" || api.RequestID!="request-1" || api.Err==nil{t.Fatalf("API error=%+v",api)}
  if strings.Contains(err.Error(),"private-body"){t.Fatal("raw error body leaked")}
  srv.Close()
 }
}
`
