# Embedded weld templates

This directory contains the declarative payloads for the `weld` scaffold.
The CLI reads them from `weldtemplate.FS()`, an embedded `io/fs.FS` in this
same Go module. A built `weld` binary therefore carries every capability; no
separate template module, checkout or network access is needed at runtime.

## Staged architecture

Capabilities are one-way additive. A project is created from exactly one
`base` capability and then grows by adding capabilities, each of which records
its version in the project manifest (`weld.json`).

```
capabilities/
  base/            kind: base   -> scaffolds a minimal, dependency-free CLI
    capability.json
    files/         main.go (builds the logger, logs a failed command),
                   internal/app (dispatch + command registry),
                   internal/logging (log/slog factory, injectable writer),
                   Makefile, README, .gitignore
  config/          kind: add    -> typed YAML configuration, requires base
    capability.json
    files/         internal/config (typed structs, injectable loader, Secret),
                   config.yml (local, git-ignored), config.example.yml, snippets
  http/            kind: add    -> HTTP lifecycle + shared toolkit + handler builder,
                   requires loom
    capability.json
    files/         internal/httpserver (server, injectable handler builder,
                   project-owned middleware configuration in middleware.go),
                   internal/httpx (the {code,msg,data} JSON envelope and raw
                   writers, typed JSON binding with a body limit, composable
                   net/http middleware), internal/app/serve.go (the one serve
                   command), config.yml / config.example.yml http section (snippet)
  web/             kind: add    -> frontend, requires http
    capability.json
    files/         web/ (React + TS + Vite), internal/web (SPA handler),
                   registered by the Loom server graph
  api/             kind: add    -> JSON API, requires http
    capability.json
    files/         internal/api (DTOs with go-validate validation, typed routes
                   registered with internal/httpx, httptest-driven tests),
                   internal/di/api_provider.go (stable Loom seam)
  db/              kind: add    -> PostgreSQL, requires loom
    capability.json
    files/         db/migrations (goose), db/query, sqlc.yaml, db/tools (nested
                   module pinning sqlc and goose), internal/data (Repository +
                   pool), generated internal/data/sqlc, snippets
  redis/           kind: add    -> opt-in Redis client, requires loom
    capability.json
    files/         internal/redisclient (lazy go-redis client, caller owns the
                   lifecycle), internal/config/redis.go (typed connection
                   settings), internal/di/redis_provider.go (stable Loom seam),
                   snippets that extend the shared config, README
  loom/            kind: add    -> Loom DI graph, requires config
    capability.json
    files/         internal/di/di.go.tmpl (capability-aware graph source),
                   tools/loom (nested module pinning the Loom generator),
                   README; the stable provider seams are contributed by the
                   capabilities that need them (api, redis, mail, storage, cron)
  cron/            kind: add    -> in-process scheduler, requires http
    capability.json
    files/         internal/cron (registry + stable register.go job seam),
                   internal/config/cron.go,
                   internal/di/cron_provider.go (stable Loom seam), snippets, README
  mail/            kind: add    -> opt-in SMTP sender, requires loom
    capability.json
    files/         internal/mailsender (per-Send SMTP client),
                   internal/config/mail.go,
                   internal/di/mail_provider.go (stable Loom seam), README
  storage/         kind: add    -> opt-in S3 client, requires loom
    capability.json
    files/         internal/objectstore (streaming S3-compatible client),
                   internal/config/storage.go,
                   internal/di/storage_provider.go (stable Loom seam), README

modules/           per-name    -> `weld add module <name>` payload (not a capability)
  module.json      files: internal/modules/<name> (typed DTOs, Service seam,
                   Go handler, httptest test, README); the module is registered
                   on the Loom server graph and no route file is written;
                   rendered once per module name

commands/          per-name    -> `weld add command <name>` / `--command` payload
  command.json     generic: an independent internal/commands/<name> group plus
                   its internal/app/<name>_command.go registration; module: the
                   same target paths backed by the module's Service. Both
                   variants declare one target set and the Loom command graph
                   internal/di/<name>_graph.go, rendered once per name.
                   Payloads are capability-aware text/templates: the command
                   package never imports the Loom runtime, and the graph
                   includes the shared commonModule but never the HTTP server
                   or the cron scheduler — so it is capability independent and a
                   later `weld add` never has to rewrite it.
```

`base` is pure CLI: it never imports `net/http` and ships no configuration. It
ships `internal/logging`, a small factory over `log/slog` (`New(w io.Writer,
format Format, options *slog.HandlerOptions)`), so the process has one logging
protocol with an injectable sink and no custom logger interface.

`config` is the shared configuration capability, required by `loom` and
installed automatically with any added capability. It ships `internal/config`:
typed Go structs, a loader
with injectable `readFile`/`lookupEnv` dependencies, and a redacting `Secret`
type. Configuration is `config.yml`, read from the working directory by default
(`--config` overrides the path), overridden by the environment and then by a
command-line flag, so the precedence is flag, environment, file, defaults. A
missing or invalid file is an error rather than a silent fallback to defaults.
`config.yml` is generated locally and git-ignored; `config.example.yml` is
committed. The file carries a marker region that each capability appends to, so
adding `db` later merges its `database` section without clobbering the user's
edits — including a local database password. Because `config.yml` is git-ignored,
a fresh clone has no local file; adding a capability restores it from the
tracked `config.example.yml` (its declared `bootstrap`) before appending, and a
missing or corrupt example is an actionable error rather than a silent default.
See [Configuration](#configuration) below.

`http` requires `loom` and owns the single `serve` command. Its `Serve` takes the
injected `*slog.Logger`, and `serve` loads the listen address through the shared
`config` loader and runs the Loom graph. `web` and `api` each require `http` and
register their handler on the Loom server graph, the only composition point, so
either can be added first and both are served by the same `serve` path.

`db` requires `loom` (and its `config`) but not `http`. It can be added to a
CLI-only project, and it is independent of `http`, `web` and `api`: the
repository speaks domain types,
not sqlc rows, and the JSON DTOs stay in `internal/api`. It can therefore be
added in any order relative to the HTTP capabilities, and adding it appends its
`database` section to the local `config.yml`.

`redis` is likewise independent: it requires `loom` but not `http`, so it can
be added to a CLI-only project and composed with `http`/`db`/`api` in any order.
It installs an opt-in client, never a connection: `internal/redisclient` builds a
lazy `go-redis` client from the typed `redis` configuration section (added to
`config.yml` through the same extension point as `database`), and the caller owns
its lifecycle. Nothing in the generated application imports the client, so an
ordinary build, test or serve needs no Redis server; `weld add redis` never wires
Redis into a service.

`loom` requires `config` and is the foundation: every capability except the bare
`base` CLI requires it directly or through `http`, so `weld add` installs it and
a project is always either the base CLI or a Loom project. It renders
`internal/di/di.go` against the installed capability set and generates
`internal/di/loom_gen.go` with the real pinned generator, so the graph follows
whatever of `http`, `db`, `api`, `redis`, `mail`, `storage` and `cron` is
installed. It is the only capability whose payload is capability aware; every
other capability stays Loom-free. The
graph also provides the `*slog.Logger` (via `NewLogger`) that the managed HTTP
server uses to record startup, a serve failure and a graceful shutdown, so the
logger is injected rather than reached for through a global. The `db`, `redis`,
`mail` and `storage` bindings are declared but pruned until a provider depends
on them; `cron` is different — the graph root consumes the scheduler, so the
scheduler is constructed and its lifecycle hooks run with the server (the socket
is bound before it starts and it stops before the server).

`cron` requires `http` (and therefore `loom`). It ships an in-process
scheduler (`internal/cron`) with a stable, user-editable registration file
(`internal/cron/register.go`) that declares no jobs, so installing the capability
schedules nothing. The stable `internal/di/cron_provider.go` seam builds the
scheduler, and because the Loom server graph root consumes it the scheduler is
constructed and its start/stop hooks run with `serve` — after the socket is bound
and stopped before the server; `help`, `version` and every short command never
reach it. The scheduler takes no database or Redis lock.

`mail` and `storage` likewise require `loom` but not `http`. `mail` ships an
SMTP sender (`internal/mailsender`) with typed, secret-redacted configuration and
an explicit TLS policy; `storage` ships a streaming S3-compatible client
(`internal/objectstore`) that never creates the bucket. Neither is imported by
the generated application, and each declares a stable Loom provider seam
(`internal/di/mail_provider.go`, `internal/di/storage_provider.go`) that the
graph declares but prunes until a provider depends on it. Each capability writes
its own seam once, so the install order does not matter.

## Configuration

`config` is a first-class, shared capability: `loom` requires it and every added
capability requires `loom`, so any `weld add` installs it and the user never adds
it by hand. `base` stays a minimal CLI with no configuration and no `config.yml`.

`internal/config` holds typed Go structs (`Config`, `HTTP`, `Database`) with
`yaml` tags for names only — there are no validation tags. `Loader.Load` reads
`config.yml` from the working directory by default (`--config` overrides the
path), then applies environment overrides and finally the explicit code
defaults, so precedence is flag, environment, file, defaults. The loader takes
its `readFile` and `lookupEnv` as dependencies (`NewLoader`), which is what makes
the loader testable without the filesystem or the process environment; `serve`
runs the Loom graph, which builds the process loader with `NewOsLoader`, so there
is one configuration path.

The typed struct is closed but extensible: `config.go` carries three marker
regions (`weld:configfields`, `weld:configenv`, `weld:configdefaults`) where a
later capability appends its own field and registers its environment overrides
and defaults. A capability that needs a new section adds the field, the redacting
type and the accessors in a same-package file — `redis` adds `internal/config/redis.go`
and the `redis` section — so the base loader never changes. A project whose
`config.go` was generated before these regions existed has no such marker, so
`weld add redis` fails with the missing extension point instead of rewriting the
file.

A missing file and an invalid file are distinct errors, and neither is a silent
fallback to defaults: an absent `config.yml`, a malformed one, or an explicitly
set-but-blank `HTTP_ADDR` fails loudly. A parse error never quotes the file
content, so a malformed secret cannot reach a log record.

`Secret` is a string that redacts itself in `fmt`, `log/slog`, JSON and YAML
output; `Value()` is the one accessor that reveals it, and `Config.DSN()` is the
only place the database password is read. The database section is validated only
where the database is actually used (`ValidateDatabase`), so an http-only project
never needs a database setting, and no credential is ever defaulted: host and
port have defaults, but the user, password and name do not.

`config.yml` is generated locally and listed in `.gitignore`;
`config.example.yml` is committed and documents the shape. The file carries a
`weld:config` marker region, and each capability appends only the section it
needs (`http` appends `http`, `db` appends `database`, `redis` appends `redis`),
so a db-only project does not carry a gratuitous http section. Because the
append is marker-based and sentinel-guarded, adding a capability later preserves
every byte outside the region and every value inside it — including a database
or Redis password edited by the user — and a repeat add is a no-op.

A patch that targets `config.yml` declares `config.example.yml` as its
`bootstrap`. On a fresh clone the git-ignored local file is absent while the
manifest still records it and the committed example remains, so adding a
capability restores `config.yml` from the example before appending its
section, rather than failing on the missing target. The restore only runs when the local file is
absent, so it never overwrites a local value or comment, and the written file is
recorded in the manifest with its hash. If the example is absent or no longer
carries the marker region, the add fails with the exact restore instruction
(for example `git checkout config.example.yml`) instead of falling back to
defaults; the loader still errors on a missing `config.yml` until the file
exists.

## Database: SQL source of truth and generated code

`db` keeps the SQL authoritative. `db/migrations` holds the schema (one goose
migration file per change, with `-- +goose Up` / `-- +goose Down` sections),
`db/query` holds the named sqlc queries, and `sqlc.yaml` reads both and
generates `internal/data/sqlc`. The
hand-written `internal/data/data.go` wraps the generated queries behind a small
`Repository` interface, converts generated rows to a domain `Item`, exposes
`NewPool` for the connection pool and `WithTx` for a narrow transaction.

The generated package is shipped, so `weld add db` produces a project that
compiles and tests immediately with no generator installed and no PostgreSQL
running. Regeneration is reproducible: the sqlc version is pinned by a `tool`
directive in the nested `db/tools/go.mod` module
(`tool github.com/sqlc-dev/sqlc/cmd/sqlc`), alongside the goose migration tool
(`tool github.com/pressly/goose/v3/cmd/goose`). That module is separate on
purpose, so neither tool's large dependency graph enters the application
`go.mod` or its `go test ./...`:

```sh
make sqlc
make migrate-up          # idempotent; requires DATABASE_URL
make migrate-status
CONFIRM=1 make migrate-down   # reverts exactly one migration
# or, from the nested module:
cd db/tools && go tool sqlc generate -f ../../sqlc.yaml
```

The migration targets never fall back to a local database: they refuse to run
when `DATABASE_URL` is unset, and `migrate-down` requires `CONFIRM=1` so it can
never roll back the whole schema by accident.

`pgx/v5` is pinned in the `weld:deps` region of the generated `go.mod` at the
newest release that keeps the project's Go toolchain floor. Generated sqlc code
imports `pgx`; it carries no placeholder tokens and no local path, so nothing
machine-specific is committed into a scaffolded project.

A capability descriptor is JSON:

```json
{
  "name": "web",
  "version": "0.1.0",
  "kind": "add",
  "summary": "...",
  "requires": ["http"],
  "files":   [ { "path": "web/package.json", "source": "files/package.json" } ],
  "patches": [ { "path": "Makefile", "marker": "web", "source": "files/Makefile.snippet" } ]
}
```

- `files` are new files written into the project. Payloads are plain text; the
  CLI substitutes `__name__`, `__module__` and `__version__` (a literal token
  replace, so JSX/JSON braces stay untouched).
- `patches` insert a snippet at an **owned extension point**: a marker pair such
  as `# weld:web:begin` / `# weld:web:end` that the owning capability places in
  a file. `weld` never performs arbitrary text replacement.
- `requires` lists capabilities that must be installed first. `weld add`
  resolves them automatically, so the order in which a user adds capabilities
  does not matter.

Two optional fields let a payload vary with the installed set:

- a `file` or `patch` may carry `when: [cap]` / `whenAbsent: [cap]`, so it is
  planned only when those capabilities are (or are not) installed; and
- a `patch` may carry `mode: "replace"` to rewrite its region instead of
  appending, which is how `loom`, `mail` and `storage` set the `go` directive.

A capability may also declare a `di` object (`dir`, `source`, `test`). It is a
`text/template` rendered against the installed capability set and formatted as
Go; `loom` uses it for the capability-aware dependency graph.

The per-name `modules/` and `commands/` payloads are capability-aware too: their
files are `text/template` payloads rendered against the same installed set, and
the Loom command graph carries `when: [loom]` (met by every project, since every
project is a Loom project). A capability file's `when`/`whenAbsent` guards are the same
mechanism a capability uses. Everything that declares no conditional stays a
plain, unconditional payload.

## Extension point format

An extension point is a `begin`/`end` marker pair whose payload the owning
capability controls. Markers use the host file's comment syntax, so a Makefile
uses `# weld:<marker>:begin` and a Go source uses `// weld:<marker>:begin`.
`weld` recognizes either prefix, which is what lets a Go composition file host
a region.

A snippet carries a **per-capability** installed sentinel,
`weld:<capability>:installed`. The sentinel — not the marker name — makes a
patch idempotent, so several capabilities can append to one region without one
suppressing another. Two capabilities patching region `config` therefore both
apply, in any installation order, while a repeat `weld add` is a no-op.

`weld` inserts the snippet between the markers and leaves every byte outside
the region untouched, so user edits in the same file survive. A patch target
must be a file `weld` manages (or one created in the same plan); `weld` refuses
to patch an unmanaged file.

## Go route composition and the shared HTTP toolkit

The Loom server graph (`internal/di/di.go`) owns `newMux`, the single route
composition point: it registers the SPA, the JSON API and the installed business
modules on one mux, and wraps it with the shared middleware chain. The
`httpx.Router` records every route it registers, and in a development
environment (`environment: local` or `development`) the graph logs the composed
table with each route's policies at startup, so the surface and its coverage are
visible without a request; production stays silent.
`internal/httpserver/http.go` exposes the test seams:

- `NewHandler(logger, routes ...Route) http.Handler` builds a handler from
  explicit routes, so tests compose a mux — and the real middleware chain —
  without a socket or a capability.
- `Chain(logger, handler) http.Handler` applies the project middleware chain to
  an already-built handler. The server graph wraps its mux with it, so
  production and tests serve the identical middleware stack.

The stack itself lives in `internal/httpserver/middleware.go`, a project-owned
file written once and never regenerated. It declares `middlewareChain(logger)`
as an outer-to-inner list of `func(http.Handler) http.Handler`; the default is
`RequestID`, `AccessLog` and `Recover`. No authentication, CORS or rate limiting
is enabled by default — add such middleware there. Because the chain is an
explicit `func(http.Handler) http.Handler`, any third-party middleware composes
with it.

`internal/httpx` is the shared toolkit both the built-in API and the generated
business modules use: the `{code,msg,data}` JSON envelope (`JSON`, `Error`) and
the raw writers for responses that must stay unwrapped (`RawJSON`, `RawText`,
`Bytes`, `NoContent`); typed request binding (`DecodeJSON`, `QueryInt`) with a
configurable body limit, a content-type check, single-value and unknown-field
rejection and an optional code-first validator callback; and the composable
middleware (`Chain`, `RequestID`, `AccessLog`, `Recover`). It depends only on
the standard library — binding takes a validator callback, so `httpx` never
imports `go-validate`, which stays a concern of `api`. The `AccessLog` recorder
forwards `Flush` and `Hijack`, so a streaming or SSE response and a WebSocket
upgrade keep working.

`web` and `api` register their handler on the Loom server graph's `newMux`
instead of a per-capability route file, so no Go import block has to be patched
and no `init` performs route registration. `api` also adds the stable
`internal/di/api_provider.go` seam, so both compose in either order.

`internal/web` mounts the SPA on `/` and reserves `/api/`: unknown API paths
return 404 instead of the HTML shell, and only browser navigations fall back to
`index.html`. It keeps that exclusion even though a more specific `/api/` route
wins on the mux, because an uninstalled API must not answer with the SPA.

## API contract

`api` owns `internal/api`. Request and response DTOs carry JSON serialization
tags only; the validation rules live in a single `Validate() error` per request
DTO, built from `go-validate`'s `Spec`/`Constraint` constructors. The `httpx`
typed helpers call `Validate()` automatically after binding, so the rules run at
runtime and there is no separate description to keep in sync. `Register`
declares each route in one place — the method, the relative path, the binding and
the handler together — so there is no route table beside the code, and the
caller owns the mount prefix (see `internal/httpserver.APIPrefix`).

Enums show the pattern: `ItemStatus` is a typed string alias with named
constants, and `validate.Enum(itemStatusValues()...)` rejects an invalid value.
If that boilerplate repeats across resources, a future `weld add enum` step (or
go-enum code generation) is the right home; the API capability deliberately does
not grow its own enum framework.

The wire format is a default JSON envelope: responses are `{code, msg, data}`,
where `code` repeats the HTTP transport status (200, 201, 400, 404, 500) instead
of inventing a business code, `msg` is `success` or a client-safe error message,
and `data` is the payload or `null`. Request bodies are the DTOs themselves. The
`httpx` typed helpers bind and validate the request, and weld ships no OpenAPI
document, generator or `/api/openapi.json` endpoint.

## Business modules (`modules/`)

`modules/` is not a capability. A capability declares a fixed set of files;
a business module is per-name, so `modules/module.json` declares a *template*
that weld renders once per `weld add module <name>`, substituting the module
name into the paths and the package, the constructor names and the URL segment.

The payload generates `internal/modules/<name>`: a typed request and response,
a `Service` interface with a `NewService` seam and an always zero-argument
`NewDemoService` fallback, a Go `net/http` handler, an `httptest`-driven test
and a README. `weld add module` installs `http` (and the `loom` and `config` it
requires), and the regenerated `internal/di` graph provides `<name>.NewService`
and registers the module on the composed mux — no separate route file is
written. Because the graph binds `NewService`, it may take a dependency (for
example `NewService(repo data.Repository)` after `weld add db`), and the
regenerated composition test serves the module through a generated fake so it
does not depend on the constructor's signature. The generated service is a
replaceable in-memory example, not persistence. Unlike generated capability
files, module files are written once and never regenerated.

With `--command`, the module's `Service` is also exposed on the command line
(see the command-group payload below): the module is registered on the Loom
server graph and the command group resolves the same `Service` from its own
stable command graph.

The module is recorded in `weld.json`'s `modules` list, not the capability list.

## Command groups (`commands/`)

`commands/` is not a capability either. `commands/command.json` declares two
variants that write the *same* target paths for a name, so a name is always one
command group in `internal/commands/<name>` plus one registration file in
`internal/app/<name>_command.go`:

- `generic` — the independent group written by `weld add command <name>`: no
  HTTP or database dependency (it installs `loom` and `config`), showing help on
  a bare invocation.
- `module` — the group written by `weld add module <name> --command`: backed by
  the module package's `Service` interface and `NewService` constructor, so the
  command line and the HTTP handler share one business layer.

Both register through the base app's `RegisterCommand` seam, so there is one
root command tree and no per-project global dispatcher. The command files are
written once and never regenerated; the command is recorded in `weld.json`'s
`commands` list, not the capability list.

Both variants also write a stable command graph
`internal/di/<name>_graph.go` and a lazy registration. The graph includes
`commonModule`, the shared provider set `di.go` regenerates (configuration,
logger and the installed db/redis/mail/storage
bindings), plus the command's dependency root, and never the HTTP server or the
cron scheduler; Loom prunes every provider the root does not consume. Because
the shared providers live in the regenerated module rather than in the command
graph, a capability installed after the command graph still reaches the command
and weld never rewrites the stable graph. The generated command package does not
import the Loom runtime: it defines a `Lifecycle` interface and resolves the
graph only when a real subcommand runs, so help and unrelated commands never
build it. A generated command file that already exists and was edited is
refused rather than overwritten, so a non-destructive conflict is reported
instead of a silent overwrite.

## go-validate dependency

`api` requires the published `github.com/Xwudao/go-validate v0.2.0`, which
exposes the `Spec`/`Constraint` API. Generated projects build without a sibling
checkout or local replacement; no local path is committed into the scaffold.

## Tests

The test validates that every embedded descriptor parses, that each declared
payload exists, that `base` stays CLI-only and ships no configuration, that
`config` needs only `base` and patches the git-ignore and `go.mod` dependency
regions, that `http` owns the serve command, ships the `internal/httpx` toolkit
and the project-owned middleware configuration, and appends its section to the
`config` files, that `api` and the module payload write the shared response
envelope, that `web` requires `http` and `api` requires `http` (not `web`) and patches the `go.mod` dependency extension
point, and that `db` requires `loom` but not `http`, ships genuine sqlc output
plus a pinned sqlc/goose tool module, and patches the `go.mod` dependency,
Makefile `db` and both `config` extension points. It also proves the migration
targets are guarded (no `psql`, explicit `DATABASE_URL`, one-step confirmed
down) and the integration test isolates itself in its own schema with no global
`DROP TABLE`. For the logging milestone it proves `base` ships the `log/slog`
factory and `main` logs a failed command through it (no `fmt.Fprintln`, no
process default logger), that the `serve` command records the listening and
shutdown lines through the injected logger while `httpserver.Serve` itself never
writes to the process streams, and that the Loom graph provides and injects one.
