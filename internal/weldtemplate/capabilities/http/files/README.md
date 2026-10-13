# HTTP layer

`internal/httpserver` owns application composition and the server runtime;
`internal/httpx` provides the reusable net/http toolkit. DI supplies dependencies
and hooks only. No global route registry or default mux is used.

## Where to edit

| File | Responsibility |
| --- | --- |
| `httpserver/middleware.go` | Global middleware order and configuration-driven CORS |
| `httpserver/mounts.go` | API and per-module mount prefixes |
| `httpserver/policy.go` | Policy names, without business service imports |
| `di/http_policies.go` | Inject authentication services and build named policies |
| `httpserver/errors.go` | Map domain errors and log unexpected failures |

These files are project-owned seams and survive later `weld add` calls.
Change DI provider signatures, then run `make generate`.

**The default administrator guard denies every request**, including requests
with a bearer token. Replace it with real authentication **and** administrator
authorization before enabling protected endpoints. The demo module's test guard
is a test fake, not production authentication.

## The same handler for production and tests

`NewHandler(cfg, logger, policies, routes...)` creates one mux/router, defines
policies, installs error mapping, registers routes, logs the development surface,
and applies the global middleware. It opens no socket. Nil cfg/logger and nil
policies work for tests that need no configuration, logs or named guards.

For a module with an injected service and guard:

```go
handler := httpserver.NewHandler(cfg, logger, []httpserver.NamedPolicy{
	{Name: httpserver.PolicyAdmin, Middleware: auth.AdminGuard()},
}, func(router *httpx.Router) {
	widget.Register(router.Group(httpserver.ModulePrefix(widget.Name)), service)
})
recorder := httptest.NewRecorder()
handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/widget", nil))
```

Inject a fake service and a test-only guard in module tests. Production's Loom
graph calls this same builder with the real dependencies.

## Route and binding conventions

```go
type ListRequest struct {
	ID     string   `path:"id"`
	Limit  *int     `query:"limit"`
	Labels []string `query:"label"`
}

// A pointer receiver works even when the handler accepts ListRequest by value.
func (req *ListRequest) Validate() error {
	if req.Limit != nil && *req.Limit < 1 {
		return errors.New("limit must be positive")
	}
	return nil
}

httpx.Get(router, "/items/{id}", handleList)
httpx.Post(router, "/items", handleCreate, httpx.WithStatus(http.StatusCreated))
router.Get("/download", handleDownload) // raw net/http response
```

- Typed GET/DELETE bind path and query; POST/PUT/PATCH bind JSON and overlay path
  parameters. Request DTOs may be structs or pointers to structs.
- Missing optional values stay nil; `?limit=` is invalid, while `?name=` is a
  valid empty string. Repeated string parameters bind to `[]string` or `*[]string`.
- JSON rejects non-JSON content types, oversized bodies, unknown fields and
  trailing values. `WithMaxBytes` changes the default 1 MiB limit.
- Validation runs after binding, for value and pointer receivers.
- Typed successes use `{code,msg,data}`; raw routes use `RawJSON`, `RawText`,
  `Bytes`, or `NoContent` when that envelope is inappropriate.
- `Group(...).Require("admin")` guards all routes on that mount. `Open()` removes
  only the baseline; explicitly added `Policy(...)` still runs.
- Missing policy names, invalid policy definitions and route conflicts fail at
  registration, before the server starts.
- `Routes()` returns an independent copy. `LogRoutes` and `PrintRoutes` show the
  registered surface and its named policies.

## Errors and middleware

Keep services transport-independent. Map domain errors with `errors.Is/As` in
`httpserver/errors.go`, then write a client-safe HTTP error. Unmapped endpoint
and response-encoding failures are logged with the request ID and become a
redacted 500. Standalone routers can use `WithErrorHandler`; groups inherit it.
Raw handlers own their error responses.

Middleware composes outermost first. The default order is CORS, RequestID,
AccessLog, Recover. CORS preflights intentionally bypass access logging.
Recover independently tracks writes, flushes and hijacks; a panic after response
commitment aborts rather than appending an error envelope. ResponseController
works through wrappers exposing `Unwrap`; access logging also records aborted
responses.

## Typed clients between projects (preview)

In the producer, give typed routes stable unique IDs:

```go
httpx.Post(router, "/items", handleCreate,
	httpx.Operation("widget_create_item"),
	httpx.WithStatus(http.StatusCreated),
)
```

Then export and generate in the consumer:

```sh
weld http export --out weld-api.json
weld client add producer --dir ../consumer --from ../producer/weld-api.json
```

The consumer gets `internal/clients/producer/client_gen.go` and
`types_gen.go`, depending only on the standard library:

```go
client, err := producer.New("http://localhost:8080",
	producer.WithHTTPClient(httpClient),
	producer.WithBearerToken(token),
)
if err != nil {
	return err
}
item, err := client.WidgetCreateItem(ctx, producer.WidgetCreateRequest{
	Name: "widget",
})
```

The client binds path/query tags, encodes JSON, unwraps the success envelope,
and returns a typed `*producer.APIError` for a non-success response.
Context cancellation is preserved. It never retries requests automatically.
Defaults are a 30-second HTTP timeout, an 8 MiB response limit and no redirects.
An injected HTTP client controls its own redirect policy.
Authentication is configured explicitly; a policy name does not prove which
credential mechanism it uses. The default administrator guard still denies
requests until wired.

Export uses static Go type analysis: constructors, package init functions and
DI graphs are never executed. Source must type-check; run `go mod tidy` first
if dependencies are missing. V1 supports direct typed registrations in
straight-line module `Register` functions, static Group/Require derivations,
constant paths/options and simple constant/if/switch ModulePrefix mounts.
Use `--module widget --prefix /external/widget` for an explicit mount override.
Without Operation, named handlers receive a fallback ID based on module and
handler names; anonymous handlers require Operation.

Supported DTOs include scalars, structs, pointers, slices (including byte
slices), string-keyed maps, recursive pointer DTOs and time.Time. Embedded
fields, interface/generic DTOs, custom serialization (including custom-codec
enums), dynamic registration helpers/control flow and wildcard paths are
rejected explicitly. Raw routes are listed as skipped, not guessed.
Only installed HTTP business modules are exported, not arbitrary DI callbacks
or custom middleware transformations of success responses.

Re-export a changed contract with `weld http export --force`, then repeat
`weld client add` to refresh the generated files. Generated clients are
tool-owned and rewritten; keep custom wrappers in separate files. Hand-owned
files are refused unless `--force` is supplied. Both commands support
`--dry-run`. This is a transport snapshot, not validation rules, service
discovery or an API compatibility checker.

## Runtime without Loom

```go
server := httpserver.NewServer("127.0.0.1:0", handler, logger)
if err := server.Start(ctx); err != nil {
	return err
}
fmt.Println(httpserver.BrowseURL(server.Addr()))
// Wait for application cancellation or a result from server.Fail(), then:
shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
defer cancel()
return server.Stop(shutdownCtx)
```

Configure `server.HTTP` before Start if the project needs different HTTP
timeouts. Defaults bound headers and idle connections, not streaming response
duration. Start is synchronous and one-shot; a failed bind may be retried.
Stop honors the caller's context and force-closes active connections when
draining fails. Hijacked connections (WebSockets) must be closed by their owner.
