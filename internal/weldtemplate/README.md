# Embedded weld templates

This directory contains the declarative payloads for the `weld` scaffold.
The CLI reads them from `weldtemplate.FS()`, an embedded `io/fs.FS` in this
same Go module. A built `weld` binary therefore carries every capability; no
separate template module, checkout or network access is needed at runtime.

The CLI also drives the assembled project: `weld dev` runs the backend and
frontend together, `weld build` builds the frontend and cross-compiles the
binary, and both read the optional, user-owned `weld.yml`. `weld.yml` is not a
payload here: it is created by the user (a starter is printed by
`weld config example`), it is never shipped in a generated project and it is
never recorded in `weld.json`, so `weld add` never rewrites it.

## Staged architecture

Capabilities are one-way additive. A project is created from exactly one
`base` capability and then grows by adding capabilities, each of which records
its version in the project manifest (`weld.json`).

**Changing a payload requires bumping the capability's `version`.** A recorded
version is what `weld upgrade` gates on: a capability whose version equals the
one in the manifest is left alone, and only a version the catalog advances makes
the upgrade rewrite the files weld wrote and the project never touched. A payload
edited without a version bump therefore never reaches an existing project, and
`weld upgrade --check` will not flag it either.

### File roles decide what an upgrade may touch

Every file entry declares a `role`, and `weld upgrade` obeys it. Declaring the
wrong one is how a payload change ends up either destroying a project edit or
never arriving:

| role | who owns the file after install | upgrade |
|---|---|---|
| `payload` (default) | weld | rewrites while the project has not changed it, otherwise reports a diff |
| `patched` | weld, plus the capabilities that patch its marker regions | renders the newer payload and splices the live region bodies into it |
| `seam` | the project | **never writes it**; reports the diff when the shipped version moves |
| `generated` (implied by `di`, never declared) | the Loom generator | re-renders it from the installed capability set; modified DI sources are conflicts, never silently replaced |

Use `seam` for every file whose doc comment promises "weld writes this once and
never regenerates it": `internal/httpserver/middleware.go`, `mounts.go`,
`policy.go` and `errors.go`, the
`*_providers.go` wiring, the `register.go` registration files, the `Makefile`,
`.gitignore`, `config.yml` and `config.example.yml`.

Use `patched` for a file weld ships *and* another capability extends through a
marker patch — today that is `internal/config/config.go`. Its regions are the
extension points, so project additions belong inside a region: the splice keeps
regions verbatim and replaces everything outside them with the newer payload.

A marker patch must never take ownership of the file it extends
(`Manifest.SetPatchedFile` keeps the shipping capability as the owner), because
the owner is what decides which capability upgrades the file.

`go.mod` and `go.sum` need no role: `weld upgrade` skips them, because the Go
toolchain rewrites them and a project legitimately keeps its own requirements
outside the `weld:deps` region.

Two consequences for capability authors:

- **A seam's contract change is a manual migration.** `http` 0.5.0 changed
  `middlewareChain(logger)` to `middlewareChain(cfg, logger)`. Projects whose
  `middleware.go` is unmodified by hand see the diff; projects that edited it see
  the difference too and fold it in themselves. The post-upgrade check
  (`go vet ./...`) refuses to keep an upgrade that leaves the project unable to
  build, so the migration is visible as a compiler error rather than as silently
  dropped code.
- **A changed patch snippet is not re-applied.** A snippet already present in a
  region keeps its sentinel and is left as it is; the shared file moves forward
  through the payload skeleton. Snippet upgrades are a known limitation, so ship
  a config change that matters for existing projects as an advisory-shaped change
  (documented in the capability README) rather than relying on the region.

Every capability payload that changes shape should also keep its generated-project
test green: `internal/scaffold` builds a real project, runs `gofmt`, `go build`,
`go vet` and `go test` on it, and the upgrade tests build a project from the
current payloads and move it forward again.

```
capabilities/
  base/            kind: base   -> scaffolds a minimal, dependency-free CLI
    capability.json
    files/         main.go (builds the logger, logs a failed command),
                   internal/app (dispatch + command registry),
                   internal/logging (logging.Logger contract (default slog), injectable writer),
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
                   net/http middleware, the configuration-driven CORS policy in
                   cors.go), internal/app/serve.go (the one serve
                   command), config.yml / config.example.yml http + cors sections
                   (snippet)
  web/             kind: add    -> frontend, requires http
    capability.json
    files/         web/ (React + TS + Vite; the build writes brotli and gzip
                   sidecars with vite-plugin-compression2), internal/web (SPA
                   handler that serves the best sidecar the client accepts),
                   registered by the Loom server graph
  db/              kind: add    -> PostgreSQL, requires loom
    capability.json
    files/         db/embed.go (migrations embedded into the binary),
                   db/migrations (goose), db/query, sqlc.yaml, db/tools (nested
                   module pinning sqlc and goose), internal/data (Repository +
                   pool), internal/migrate (opt-in startup migration through the
                   goose library), generated internal/data/sqlc,
                   internal/testdb (database_test resolver and per-test-schema
                   pool), internal/di/migrate_provider.go (stable Loom seam),
                   snippets
  seed/            kind: add    -> database seeding, requires db
    capability.json
    files/         internal/data/seeder (Deps, Registry, Runner, the stable
                   register.go index and its `!seed` register_stub.go stand-in,
                   one seed_<name>.go per seeder, added by
                   `weld add seeder <name>`), internal/app/seed.go (the short seed command),
                   internal/di/seed_providers.go (stable Loom repository seam),
                   Makefile seed target (db region), README
  redis/           kind: add    -> opt-in Redis client, requires loom
    capability.json
    files/         internal/redisclient (lazy go-redis client, caller owns the
                   lifecycle), internal/config/redis.go (typed connection
                   settings), internal/di/redis_provider.go (stable Loom seam),
                   snippets that extend the shared config, README
  events/          kind: add    -> opt-in in-process event dispatcher, requires loom
    capability.json
    files/         internal/events (typed Dispatcher + Publisher + On/Publish,
                   the stable register.go listener seam, a Recorder test double,
                   the dispatcher's own test, README),
                   internal/di/events_provider.go (stable Loom seam);
                   standard library only, so it patches no shared file at all
  loom/            kind: add    -> Loom DI graph, requires config
    capability.json
    files/         internal/di/di.go.tmpl (capability-aware graph source),
                   tools/loom (nested module pinning the Loom generator),
                   stable http_routes.go and app_provider.go business seams
                   (when http is installed), README; the stable provider seams are contributed by the
                   capabilities that need them (seed, redis, events, mail,
                   storage, cron)
  cron/            kind: add    -> in-process scheduler, requires http
    capability.json
    files/         internal/cron (registry + stable deps.go and register.go job
                   seams), internal/config/cron.go,
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
ships `internal/logging`: one `logging.Logger`, built through an injected
`io.Writer`. All consumers depend on the backend-neutral `logging.Logger`
interface; only its default implementation uses slog. Text records go through
[`tint`](https://github.com/lmittmann/tint), a zero-dependency slog handler, so
a console record is colored while the project keeps its `time.DateTime` clock
and can paint a statement field; JSON stays slog's own handler for a log
pipeline. `New` is the short form, `NewWithOptions` is the full one, and
`NewFromEnv` reads `LOG_LEVEL`/`LOG_FORMAT`/`NO_COLOR` so an operator tunes
logging without editing code. `ParseLevel`, `Err` and the
`WithContext`/`FromContext` pair round out the factory.

`config` is the shared configuration capability, required by `loom` and
installed with any added capability's `--with-dependencies`. It ships `internal/config`:
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
injected `logging.Logger`, and `serve` loads the listen address through the shared
`config` loader and runs the Loom graph. `web` and each business module require
`http` and
register their handler on the Loom server graph, the only composition point, so
either can be added first and both are served by the same `serve` path.

`db` requires `loom` (and its `config`) but not `http`. It can be added to a
CLI-only project, and it is independent of `http` and `web`: the
repository speaks domain types,
not sqlc rows, and the HTTP DTOs never enter the persistence layer. It can therefore be
added in any order relative to the HTTP capabilities, and adding it appends its
`database` section to the local `config.yml`.

`seed` requires `db` (and therefore `loom`). It ships the seeding package
(`internal/data/seeder`) with a stable, user-editable registration file
(`internal/data/seeder/register.go`) that registers one idempotent example
seeder over the `items` table, and the stable `internal/di/seed_providers.go`
seam that wires the repositories the seeders receive. A short `seed` command
resolves a dedicated Loom graph — the only graph that consumes `*pgxpool.Pool` —
so serving still needs no database credential; installing seed seeds nothing
until you register a seeder. The seed command, `register.go` and every
`seed_<name>.go` carry `//go:build seed`, and `register_stub.go` is the
`//go:build !seed` stand-in, so an untagged (production) build has no seed
command, no seed data and no faker a seeder imports; `make seed` and
`weld seed` compile the tag in.

`redis` is likewise independent: it requires `loom` but not `http`, so it can
be added to a CLI-only project and composed with `http`/`db` in any order.
It installs an opt-in client, never a connection: `internal/redisclient` builds a
lazy `go-redis` client from the typed `redis` configuration section (added to
`config.yml` through the same extension point as `database`), and the caller owns
its lifecycle. Nothing in the generated application imports the client, so an
ordinary build, test or serve needs no Redis server; `weld add redis` never wires
Redis into a service.

`loom` requires `config` and is the foundation: every capability except the bare
`base` CLI requires it directly or through `http`, so
`weld add <capability> --with-dependencies` installs it and
a project is always either the base CLI or a Loom project. It renders
`internal/di/di.go` against the installed capability set and generates
`internal/di/loom_gen.go` with the real pinned generator, so the graph follows
whatever of `http`, `db`, `seed`, `redis`, `mail`, `storage` and `cron` is
installed. It is the only capability whose payload is capability aware; every
other capability stays Loom-free. The
graph also provides the `logging.Logger` (via `NewLogger`) that the managed HTTP
server uses to record startup, a serve failure and a graceful shutdown, so the
logger is injected rather than reached for through a global. The `db`, `redis`,
`mail` and `storage` bindings are declared but pruned until a provider depends
on them; `cron` is different — the graph root consumes the scheduler, so the
scheduler is constructed and its lifecycle hooks run with the server (the socket
is bound before it starts and it stops before the server).

`cron` requires `http` (and therefore `loom`). It ships an in-process
scheduler (`internal/cron`) with a stable business wiring file
(`internal/di/cron_jobs.go`) that declares no jobs. Add service parameters to
`NewCronJobs` and register their ordinary `func(context.Context) error` methods,
then run `make generate`. The HTTP graph consumes registration automatically
before startup; scheduler errors are logged with the job name. The stable
`cron_provider.go` seam also builds the scheduler, and because the Loom server
graph root consumes it the scheduler is constructed and its start/stop hooks run
with `serve` — after the socket is bound
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
capability requires `loom`, so `--with-dependencies` installs it and the user
never adds it by hand. `base` stays a minimal CLI with no configuration and no `config.yml`.

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

`Secret` is a string that redacts itself in `fmt`, `logging`, JSON and YAML
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
migration file per change, with `-- +goose Up` / `-- +goose Down` sections and a
version chosen at install or creation time, never a fixed payload number),
`db/query` holds the named sqlc queries, and `sqlc.yaml` lists the query files
and generates `internal/data/sqlc`. A capability that adds another query package
appends a second entry to the same `sqlc.yaml`, so there is still one config and
one `weld gen sqlc` command (`make sqlc` is the no-weld fallback). The
hand-written `internal/data/data.go` wraps the generated queries behind a small
`Repository` interface, converts generated rows to a domain `Item`, exposes
`NewPool` for the connection pool and `WithTx` for a narrow transaction.

The generated package is shipped, so `weld add db` produces a project that
compiles and tests immediately with no generator installed and no PostgreSQL
running. The SQL integration test asks `internal/testdb.Pool(t)` for a pool and
never builds a dsn; the helper reads the `database_test` section of `config.yml`
(or `PG_TEST_DSN`), refuses the application database, isolates each test in a
unique throwaway schema, applies the migrations there and drops only that
schema. Regeneration is reproducible: the sqlc version is pinned by a `tool`
directive in the nested `db/tools/go.mod` module
(`tool github.com/sqlc-dev/sqlc/cmd/sqlc`), alongside the goose migration tool
(`tool github.com/pressly/goose/v3/cmd/goose`). That module is separate on
purpose, so neither tool's large dependency graph enters the application
`go.mod` or its `go test ./...`:

```sh
weld gen sqlc            # or: make sqlc
weld gen sqlc --check    # CI: fail on stale generated files
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
newest release that keeps the project's Go toolchain floor. The goose *library*
is pinned there too, because `internal/migrate` applies the embedded migrations
when `database.auto_migrate` is on; the goose *command* stays in the nested
`db/tools` module, whose dependency graph is much larger. Generated sqlc code
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
  reports a missing requirement as an error by default; `--with-dependencies`
  installs the chain, so a user controls the install and the order in which they
  add capabilities does not matter.

Two optional fields let a payload vary with the installed set:

- a `file` or `patch` may carry `when: [cap]` / `whenAbsent: [cap]`, so it is
  planned only when those capabilities are (or are not) installed; and
- a `patch` may carry `mode: "replace"` to rewrite its region instead of
  appending, which is how `loom`, `mail` and `storage` set the `go` directive.

A `file` may carry `versioned: true`, which marks a **goose migration**. Its
`path` is the file's name without a version (`db/migrations/create_users.sql`) and
the project receives `<version>_create_users.sql`. The version is chosen when the
capability is installed — the later of the UTC timestamp and one past the highest
version already in that directory — because goose orders migrations by that
integer and refuses an out-of-order file without `--allow-missing`. A fixed
number in a payload would always sort before the project's own timestamped
migrations, and would collide with a migration the project wrote itself. The
manifest records both the chosen path and the payload `source`, so upgrade
rewrites that file and never renames it (a migration may already be applied), and
`--prune` does not mistake it for a retired file. `weld db create` follows the
same rule, so a project has one numbering convention.

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

The Loom server graph (`internal/di/di.go`) supplies modules and the SPA to
`httpserver.NewHandler(cfg, logger, policies, routes...)`, the shared production
and test composition entry in `internal/httpserver/handler.go`. Route callbacks
receive an `*httpx.Router`, just like a module's Register function. The builder
installs named policies and project error mapping, registers routes on one mux,
logs the development surface and applies the project middleware stack. Tests
inject fake services and guards without opening a socket.

`httpx.Router` records each successful registration. In a development environment
(`environment: local` or `development`) the handler builder logs the route table
and policy coverage; production stays silent. `Chain(cfg, logger, handler)` is
the lower-level entry for wrapping an already-built handler.

The stack itself lives in `internal/httpserver/middleware.go`, a project-owned
file written once and never regenerated. It declares
`middlewareChain(cfg, logger)` as an outer-to-inner list of
`func(http.Handler) http.Handler`; the default is `CORS` (only when `config.yml`
enables the `cors` section), `RequestID`, `AccessLog` and `Recover`. The CORS
middleware is first so a preflight is answered before the router and the headers
are on the response when a policy or a handler refuses a request; the file
translates the typed `config.CORS` section into `httpx.CORS`, so the toolkit
stays free of the project's configuration types. No authentication or rate
limiting is enabled by default — add such middleware there. Because the chain is
an explicit `func(http.Handler) http.Handler`, any third-party middleware
composes with it.

`internal/httpx` is the shared toolkit the generated
business modules use: the `{code,msg,data}` JSON envelope (`JSON`, `Error`) and
the raw writers for responses that must stay unwrapped (`RawJSON`, `RawText`,
`Bytes`, `NoContent`); typed request binding (`DecodeJSON`, `QueryInt`) with a
configurable body limit, a content-type check, single-value and unknown-field
rejection and an optional code-first validator callback; raw route methods on the
router (`Router.Get`, `Router.Post`, ...), so a route built from a path constant
never assembles a `"METHOD /path"` string by hand; the
composable middleware (`Chain`, `RequestID`, `AccessLog`, `Recover`); and the
cross-origin policy (`CORS`, `CORSConfig`) the project enables from `config.yml`,
which answers a preflight before the router, refuses a disallowed origin, method
or header, echoes the origin instead of `"*"` when credentials are allowed, and
emits the matching `Vary` headers. It uses the standard library and the project's
logging interface — binding takes a validator callback, so `httpx`
never imports `go-validate` (a DTO that wants it wires it in through the
callback), and the CORS middleware takes its policy as a value rather than
reading the project's configuration types. The `AccessLog`
recorder forwards `Flush` and `Hijack`, so a streaming or SSE response and a
WebSocket
upgrade keep working.

`web` and each business module register through the Loom graph's `composeHandler`
instead of a per-capability route file, so no Go import block has to be patched
and no `init` performs route registration.

`internal/web` mounts the SPA on `/` and reserves `/api/`: unknown API paths
return 404 instead of the HTML shell, and only browser navigations fall back to
`index.html`. It keeps that exclusion even though a more specific `/api/` route
wins on the mux, because an uninstalled API must not answer with the SPA.

## The shared response envelope

A DTO that implements `Validate() error` is validated automatically after
binding (including pointer-receiver validators), and the `httpx`
typed helpers call it, so the rules run at
runtime and there is no separate description to keep in sync. `Register`
declares each route in one place — the method, the relative path, the binding and
the handler together — so there is no route table beside the code, and the
caller owns the mount prefix (see `internal/httpserver.APIPrefix`).

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
and a README. `weld add module --with-dependencies` installs `http` (and the
`loom` and `config` it requires), and the regenerated `internal/di` graph provides `<name>.NewService`
and registers the module on the composed mux — no separate route file is
written. Because the graph binds `NewService`, it may take a dependency (for
example `NewService(repo data.Repository)` after `weld add db` and registering
the repository in `internal/di/providers.go`), and the
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
  HTTP or database dependency (it requires `loom` and `config`; `--with-dependencies`
  installs them), showing help on
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
logger and the installed db/redis/mail/storage/events
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

## Tests

The test validates that every embedded descriptor parses, that each declared
payload exists, that `base` stays CLI-only and ships no configuration, that
`config` needs only `base` and patches the git-ignore and `go.mod` dependency
regions, that `http` owns the serve command, ships the `internal/httpx` toolkit
and the project-owned middleware configuration, and appends its section to the
`config` files, that the module payload writes the shared response
envelope, that `web` requires `http` and patches the `go.mod` dependency extension
point, and that `db` requires `loom` but not `http`, ships genuine sqlc output
plus a pinned sqlc/goose tool module, and patches the `go.mod` dependency,
Makefile `db` and both `config` extension points. It also proves the migration
targets are guarded (no `psql`, explicit `DATABASE_URL`, one-step confirmed
down) and the integration test isolates itself in its own schema with no global
`DROP TABLE`. For the logging milestone it proves `base` ships the `logging`
factory and `main` logs a failed command through it (no `fmt.Fprintln`, no
process default logger), that the `serve` command records the listening and
shutdown lines through the injected logger while the HTTP runtime never
writes directly to the process streams, and that the Loom graph provides and injects one.
