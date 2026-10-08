# weld

`weld` is a progressive Go scaffold. Start with a minimal Go CLI, then add
capabilities to the *same* project over time — `weld add web` modifies your
existing project to gain a web frontend instead of generating a second
template.

```
weld new demo          # minimal Go CLI: version + help, no dependencies
cd demo
weld add web           # installs http, then the React + TS + Vite frontend
weld add api           # JSON API + OpenAPI 3.1 (installs http too; no web needed)
weld add db            # PostgreSQL: SQL migrations + sqlc, pool and repository
make build             # builds the frontend, embeds it, builds the app
make run ARGS=serve    # serves the frontend and /api on http://localhost:8080
```

## Two tightly coupled repos

| repo | owns |
| --- | --- |
| [`../weld-template`](../weld-template) | declarative capability payloads, exposed as an embedded `io/fs.FS` |
| `weld` (this repo) | CLI: catalog loading, rendering, manifest, planning, safe apply |

`weld` imports `github.com/Xwudao/weld-template` and reads payloads through
`weldtemplate.FS()`. The embedded FS means a built `weld` binary carries its
whole scaffold — no checkout, network access, or environment variable at run
time. During development `go.mod` uses:

```
replace github.com/Xwudao/weld-template => ../weld-template
```

To release, publish `weld-template` and pin a real version, then drop the
`replace`. That module is the single, versioned seam between the two repos.

## Commands

```
weld new <name> [--module path] [--dir dir] [--dry-run]
weld add <capability> [--dir dir] [--dry-run]
weld list [--dir dir]
weld version
weld help
```

Capabilities:

- `base` (scaffold) — minimal, dependency-free Go CLI.
- `http` (add) — HTTP server lifecycle, a composable handler builder, and the
  single `serve` command.
- `web` (add) — React + TypeScript + Vite frontend, served by `http`.
- `api` (add) — JSON HTTP API: typed DTOs, `go-validate` rules and an OpenAPI
  3.1 document built from the same contract, served by `http`.
- `db` (add) — PostgreSQL persistence: SQL migrations and queries, sqlc-generated
  code, an injectable connection pool and a repository. Requires only `base`.

`web` and `api` are independent and both require `http`, so `weld add web` and
`weld add api` can be run in either order and both are served by the one `serve`
command. `db` requires only `base`: it is independent of the HTTP capabilities,
so it can be added to a CLI-only project and composes with `http`/`web`/`api` in
any order.

## Staged architecture

```
new ──▶ base project ──▶ add capability ──▶ add capability ──▶ …
         (go.mod, main.go,   (http: internal/httpserver,   (web: internal/web + internal/httpserver/web_route.go,
          internal/app,       internal/app/serve.go)        api: internal/api + internal/httpserver/api_route.go)
          Makefile)                                             db: db/migrations, db/query, db/tools,
                                                                  internal/data + generated internal/data/sqlc)
```

Every file `weld` writes is recorded in `weld.json` with the capability and
version that produced it. Capabilities are **one-way additive**: later
capabilities extend the project; nothing removes a capability.

Capabilities are also **independent and dependency-resolved**. `weld new`
produces a CLI that builds and runs with no HTTP server, no database and no
frontend. `web` and `api` each require `http`; `weld add web` or `weld add api`
installs `http` first automatically, so the user never has to know the order.
`web` and `api` can be added in either order and share the one serve command.
`db` requires only `base`, so it never pulls in an HTTP server and can be added
to a CLI-only project.

## How capabilities integrate (owned extension points, not replacement)

No capability performs arbitrary text substitution. There are three owned
extension points:

1. **Go command registry** — `internal/app` exposes `RegisterCommand(Command)`.
   `http` adds `internal/app/serve.go`, whose `init` registers the `serve`
   command. Commands are the only init-time registration point; HTTP routes are
   not.
2. **Go route composition** — `internal/httpserver/http.go` exposes
   `NewHandler(routes ...Route)` plus a `weld:routes` marker region that
   `Handler()` reads. `web` adds `internal/httpserver/web_route.go` (same
   package) and appends `installWebRoute` to that region; `api` adds
   `internal/httpserver/api_route.go` and appends `installAPIRoute`. Composition
   is explicit: no global route registry and no init-time route registration.
3. **File markers** — `Makefile` and `.gitignore` regions for the frontend build
   and ignores, a `Makefile` `weld:db` region for the sqlc and migration targets,
   and a `go.mod` `weld:deps` region where a capability declares the Go module
   dependencies it needs. Markers use the host file's comment syntax and a
   per-capability sentinel (`weld:<capability>:installed`), so several
   capabilities can extend one region in any order and a repeat `weld add` is a
   no-op.

`internal/web` serves the SPA on `/` and reserves `/api/`: unknown API paths
return 404 instead of the HTML shell, and only browser navigations fall back to
`index.html`. It keeps that exclusion rather than relying on mux precedence, so
the frontend never masquerades as an API that is not installed.

## Safety model

- **Plan before write.** A plan is fully built and conflict-checked before any
  byte is written, which is what makes `--dry-run` truthful.
- **Dependency resolution.** Missing requirements are installed first, in
  dependency order, as part of the same atomic plan.
- **Conflict detection.** Refuses to clobber unmanaged files, to patch a file
  `weld` does not manage, to patch a malformed extension point, or to act on a
  non-project directory.
- **Idempotent.** Re-running `weld add web` is a no-op that reports
  `already installed` and flags any managed files that have drifted.
- **Best-effort rollback.** `Apply` captures each file's previous content and
  restores it if a later write fails. The rollback is best effort: it can itself
  fail (for example on a full disk), so a project is not guaranteed to be
  all-or-nothing, only to attempt recovery.
- **Preserves user files.** Existing files are only rewritten through a verified
  marker region; every byte outside the region is left untouched, and all other
  writes are additive.

## `weld add api` (stage 2)

`weld add api` is implemented as a stage-2 capability: HTTP contract,
`go-validate` rule metadata, and an OpenAPI 3.1 document built from the same
DTO `Spec`s. It requires `http`, appends its route to the `weld:routes` region,
owns the `/api/` namespace, and does not require `web`.

Its one unfinished edge is a dependency that is not published yet:
`api` requires a `go-validate` version with the `Spec`/`Constraint` API, which
currently exists only as a local working copy (the newest tag is v0.1.1). The
generated `go.mod` pins a placeholder (`v0.2.0`) and a local build needs a
temporary `replace`/`go.work`. Releasing this capability requires publishing
go-validate and pinning the real version, then removing the placeholder.

## `weld add db` (stage 3)

`weld add db` is an opt-in persistence capability that requires only `base`. It
keeps SQL authoritative: `db/migrations` holds the schema, `db/query` the named
queries, and `sqlc.yaml` reads both to generate `internal/data/sqlc`. The
hand-written `internal/data/data.go` wraps the generated queries in a small
`Repository` interface, converts generated rows to a domain `Item`, exposes
`NewPool` and a narrow `WithTx`.

What lands in the project:

- `db/migrations/000001_create_items.sql`, one goose migration (`-- +goose
  Up` / `-- +goose Down`), plus `db/query/items.sql` and `sqlc.yaml`;
- `db/tools/`, a nested Go module that pins the generator and the goose
  migration tool with `tool` directives
  (`tool github.com/sqlc-dev/sqlc/cmd/sqlc` and
  `tool github.com/pressly/goose/v3/cmd/goose`), so neither tool's large
  dependency graph ever enters the app `go.mod` or `go test ./...`;
- `internal/data/sqlc/`, the generated code, shipped so the project compiles and
  tests immediately with no tool installed;
- `internal/data/data.go`, the `Repository`/`Item` wrapper, `NewPool` and
  `WithTx`, plus a unit test and a `PG_TEST_DSN`-gated integration test;
- `internal/data/README.md`, and the `go.mod` dependency and `Makefile` `db`
  regions patched.

The generator is reproducible: `make sqlc` (or `cd db/tools && go tool sqlc
generate -f ../../sqlc.yaml`) regenerates `internal/data/sqlc`, and a
regeneration must reproduce the committed files byte for byte. Migrations run
through the pinned goose tool: `make migrate-up` applies pending changes
idempotently, `make migrate-status` reports versions, and `make migrate-down`
reverts exactly one change only with `CONFIRM=1`. Every migration target
requires `DATABASE_URL` explicitly, so none can silently fall back to a local
database. `pgx/v5` is pinned in the
`weld:deps` region at the newest release that keeps the project's Go toolchain
floor; no local path or credential is committed.

`db` is independent of `http`, `web` and `api`. The HTTP DTOs stay in
`internal/api` and are never derived from sqlc rows, and `internal/data` never
imports `net/http` or `internal/api`, so the capability can be added to a
CLI-only project and composes with the HTTP capabilities in any order.

## Tests

```sh
go test ./... -count=1
go vet ./...
```

Test conventions:

- No test opens a network socket. HTTP behavior is asserted with
  `net/http/httptest` against handlers built from injected routes/fixtures
  (`httpserver.NewHandler`, and `web.Handler` over an `fstest.MapFS`).
- The generated `serve` command is exercised through flag parsing and the
  command registry (`internal/app/serve_test.go`) rather than by listening.
- The scaffold tests generate a real project, run `gofmt`, `go build`, `go vet`
  and the generated `go test ./...`, so the composed files are compiled rather
  than only string-matched.
- The generated API tests use `net/http/httptest` with an injected fake
  `Service`, so the handler is exercised with no socket and no database, and the
  OpenAPI 3.1 document is validated with a standards parser (`kin-openapi`).
- The generated `db` unit tests run with no PostgreSQL: they cover the
  row-to-domain conversion and dsn validation, and the gated integration test
  skips when `PG_TEST_DSN` is unset. `PG_TEST_DSN` must name a dedicated,
  disposable test database; there is no fallback to the application dsn, so the
  suite never targets a live database by accident. The integration test also
  isolates itself: it creates a unique throwaway schema, points the pool's
  `search_path` at it, applies the migration there, and drops only that schema
  with `CASCADE`, so it never drops or mutates the database's real tables.
- The scaffold tests for `db` generate a real project and run `gofmt`, `go
  build`, `go vet`, the generated `go test ./...`, and a sqlc regeneration that
  must produce no diff. Combination tests cover `base`→`db`, `web`→`db` and
  `api`→`db` in either order.
- The API integration tests are **skipped with a reason** when the sibling
  `go-validate` working copy (or the test dependencies) is unavailable, because
  the `Spec`/`Constraint` API is not published yet (newest tag v0.1.1). The
  release gate stays blocked until `go-validate` is published and pinned and a
  standalone generated project builds without the sibling. Set
  `WELD_GO_VALIDATE_DIR` to point at a specific working copy.
- Coverage includes create, dependency resolution, add, repeat (idempotency),
  unmanaged-file conflicts, dry-run, no unintended edits, and the extension
  point format (two capabilities into one region in either order, duplicate
  inserts, malformed markers, and preserved user text).

## Layout

```
cmd/weld/            CLI entry point
internal/cli/        argument parsing and output
internal/template/   capability catalog + placeholder rendering
internal/project/    manifest, marker patching, best-effort rollback apply
internal/scaffold/   create/add plans and dependency resolution
```
