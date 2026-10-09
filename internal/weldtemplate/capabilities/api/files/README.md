# internal/api

The JSON HTTP contract of this project. Request and response DTOs carry JSON
serialization tags only; each DTO's `Spec()` declares its go-validate rules
once, and the same `Spec` produces the runtime validation and the OpenAPI 3.1
constraints. `operations()` is the single route table behind both the mux
registration (`Register`) and the document (`Document`, served at
`GET /api/openapi.json`).

## Business modules in the document

The document merges two sources: the built-in `operations()` table and the
operations every linked business module registered with the shared
`internal/openapi` package. A module registers the same route table its
`Register` installs, so a module route cannot exist undocumented and a
documented operation cannot exist unrouted. This package never imports a module
and never infers a route, a schema or an authorization rule from a handler: it
states only what a module explicitly declared.

A duplicate `operationId`, method or path between the two sources is an error,
never a silent overwrite, so `weld add module` and `weld add api` compose in
either order without one hiding the other. A module that predates this package
simply contributes no documented operation; it never breaks the build.

## The document switch (fail-closed)

`GET /api/openapi.json` is **off by default**. It is served only when the
deployment names itself a development or local environment *and* explicitly
enables the switch:

- `environment` in `config.yml`, overridden by the `APP_ENV` environment
  variable, must be `development` or `local`;
- `openapi.enabled`, overridden by the `OPENAPI_ENABLED` environment variable,
  must be `true`.

An unset, unknown or production environment never serves the document, even if
the switch is enabled. The decision is resolved once from the typed
configuration (`config.Config.OpenAPIDocumentEnabled`) and passed to
`api.Register` as a plain bool, so no `os.Getenv` bypass can disagree with the
process configuration. A refused request is a `404` with no document, not a
hidden endpoint that still returns the document. The built-in items API and the
business module routes are unaffected by the switch.

## Request and response format

Request bodies are the DTOs themselves. Responses are wrapped in the shared
`{code, msg, data}` envelope from `internal/httpx`:

- `code` repeats the HTTP transport status (200, 201, 400, 404, 500) rather than
  inventing a separate business code, so a client may read either;
- `msg` is `success` for a success response and a short, client-safe message for
  an error;
- `data` is the payload, or `null` on every error.

The handler binds and validates with `httpx.DecodeJSON`, passing
`Spec().Validate` as the code-first validator callback, so `internal/httpx`
stays free of the go-validate dependency while this package keeps it.
`GET /api/openapi.json` is the one exception: it is served raw with
`httpx.RawJSON`, because a spec generator must receive the OpenAPI document
itself. The document's response schemas describe the same envelope.

> **Development demo:** the default `Service` is `NewService()`, an in-memory
> service. Items live in the process only and are lost on restart, and serving
> the API needs no database. Installing the `db` capability does **not** switch
> this service to PostgreSQL; wire persistence in yourself (see
> `internal/data/README.md`).

The handler is a plain `http.Handler` built with an injected `Service`, so it is
tested with `net/http/httptest` and a fake service: no listener and no database.

## Wiring a persistent Service

`Service` is a small interface, so a repository-backed implementation replaces
the demo without touching the HTTP layer. The `serve` command builds the
application from `internal/di`; edit `internal/di/api_provider.go` — a stable
file weld writes once and never regenerates — to return your service and, to
persist it, to take `data.Repository` in `NewAPIService`. Do not edit
`internal/di/di.go`: it is regenerated, and an edit there is erased by the next
`weld add`.

See `internal/data/README.md` for the pool and repository, and
`internal/di/README.md` for the Loom graph.

## go-validate dependency

This package uses the published `github.com/Xwudao/go-validate v0.2.0` for
its `Spec`/`Constraint`/`Schema` API. No local replacement is needed. Run
`go mod tidy` before `go test ./...` to resolve the test-only
`github.com/getkin/kin-openapi` dependency that validates the OpenAPI document.
