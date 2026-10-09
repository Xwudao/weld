# `internal/api`

`weld add api` generated this package: the JSON HTTP contract of the project.
weld records every file in `weld.json` and never regenerates them, so your edits
survive later `weld add` commands.

## What it is

- `dto.go` — the typed request and response DTOs plus each request's `Validate`.
- `service.go` — the `Service` interface and the default in-memory
  implementation.
- `handler.go` — `Handler` and `Register`, which declare every route once.
- `api_test.go` — an in-process `httptest` suite with an injected Service.

The HTTP surface is a plain `http.Handler`, so a test exercises it with
`net/http/httptest`; it opens no socket and needs no database.

## Routes

`Register` declares each route in one statement: the method, the relative path,
the request binding and the handler are together, so there is no separate route
table or documentation file to keep in sync. The caller owns the mount prefix
(see `internal/httpserver.APIPrefix` in the generated server graph), so the same
routes can serve `/api/v1` and `/api/v2` without a change here.

```go
func Register(router *httpx.Router, service Service) {
	httpx.Get(router, "/items", func(ctx context.Context, query ListItemsQuery) ([]ItemResponse, error) {
		...
	})
	httpx.Post(router, "/items", func(ctx context.Context, request CreateItemRequest) (ItemResponse, error) {
		...
	}, httpx.WithStatus(nethttp.StatusCreated), httpx.WithMaxBytes(maxBodyBytes))
	httpx.Get(router, "/items/{id}", func(ctx context.Context, request GetItemRequest) (ItemResponse, error) {
		...
	})
}
```

`Handler(service)` mounts the same routes at the default `/api` prefix; it is
the in-process seam a test uses.

## Binding and validation

The typed helpers in `internal/httpx` bind the request before calling the
handler:

- `httpx.Get` and `httpx.Delete` bind the URL query and the path parameters.
- `httpx.Post`, `httpx.Put` and `httpx.Patch` bind the JSON body and the path
  parameters.
- `httpx.NoInput` binds nothing.
- A DTO field is bound from a `query:"name"` or `path:"name"` tag; a field
  without one keeps its zero value.
- `httpx.DecodeJSON` enforces the content type, the body limit, a single JSON
  value and unknown-field rejection, so `CreateItemRequest` does not repeat
  those checks.
- A request type that implements `Validate() error` is validated automatically
  after binding. `CreateItemRequest` builds a go-validate `Spec`, so the rules
  live next to the fields they describe; a failure is a `400` envelope.

## Responses and errors

Every handler writes through the shared `{code,msg,data}` envelope:

- a success response is `httpx.JSON` with `code` equal to the HTTP status and
  `msg` equal to `httpx.SuccessMsg` (`"success"`);
- an error is `httpx.Error`, or a handler returns an `*httpx.HTTPError` such as
  `httpx.NotFound("item not found")` and the helper maps it to the status and
  message;
- any other error becomes a fixed `500 internal error` envelope, so an
  unexpected error never leaks its message.

`code` always repeats the HTTP transport status; the API does not invent a
separate business-code space. For a response that must stay unwrapped — a file
download, a stream, SSE — register a raw handler with
`router.Get("/export", ...)` (or `router.Raw` for another method) and write it
with `httpx.RawText`, `httpx.Bytes` or `httpx.RawJSON`.

## Replaceable example, not persistence

`NewService` returns an in-memory development service: items are lost on
restart and no database is required. Installing `db` does not switch it to
PostgreSQL. To persist the API, change `NewService` (or the `NewAPIService`
provider in `internal/di/api_provider.go`) to consume `data.Repository`; the
Loom graph follows the signature.
