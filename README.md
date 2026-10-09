# weld

`weld` is a progressive Go scaffold. Start with a minimal Go CLI, then add
capabilities to the *same* project over time — `weld add web` modifies your
existing project to gain a web frontend instead of generating a second
template.

```
weld new demo          # minimal Go CLI: Cobra command tree, version + help
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
weld add module <name> [--command] [--dir dir] [--dry-run]
weld add command <name> [--dir dir] [--dry-run]
weld list [--dir dir]
weld skills [--dir dir] [--dry-run]
weld version
weld help
```

Capabilities:

- `base` (scaffold) — minimal Go CLI on a Cobra command tree with a `log/slog`
  logging factory (`internal/logging`) whose sink is an injected `io.Writer`.
  It ships no configuration and no server; the only dependency is Cobra itself.
- `config` (add) — the shared typed configuration capability: `config.yml` from
  the working directory, environment and flag overrides, and a redacting
  `Secret` type. Installed automatically by the first of `http` and `db`.
- `http` (add) — HTTP server lifecycle, a composable handler builder, and the
  single `serve` command. Its `Serve` takes the injected `*slog.Logger`, and
  `serve` reads the listen address through the shared `config` loader.
- `web` (add) — React + TypeScript + Vite frontend, served by `http`.
- `api` (add) — JSON HTTP API: typed DTOs, `go-validate` rules and an OpenAPI
  3.1 document built from the same contract, served by `http`.
- `db` (add) — PostgreSQL persistence: SQL migrations and queries, sqlc-generated
  code, an injectable connection pool and a repository. Requires `base` and
  `config`.
- `redis` (add) — an opt-in Redis client with typed connection configuration.
  Requires `base` and `config`; installing it never connects Redis to a service.
- `mail` (add) — an opt-in SMTP sender with typed, secret-redacted
  configuration and an explicit TLS policy. Requires `base` and `config`;
  installing it never sends and never connects.
- `storage` (add) — an opt-in S3-compatible object-storage client. Requires
  `base` and `config`; installing it creates no bucket and uploads nothing.
- `cron` (add) — an opt-in in-process scheduler that starts and stops with the
  `serve` command and never with a short command. Requires `base` and `config`;
  installing it schedules nothing until you register jobs in
  `internal/cron/register.go`.
- `loom` (add) — a compile-time [Loom](https://github.com/Xwudao/loom)
  dependency-injection graph that wires the HTTP server, the JSON API service, the
  database, Redis, mail, storage and the cron scheduler with lifecycle
  start/stop. Requires `http` and is **opt-in**: `web`, `api`, `db`, `redis`,
  `mail`, `storage` and `cron` never install it.

`http` and `db` each require `config`, so the first of them installs it
automatically: `weld add http` or `weld add db` on a fresh project adds the
shared loader, generates the local `config.yml` (git-ignored) from the committed
`config.example.yml`, and appends only its own section. Adding the other later
merges its section into the same file without clobbering the user's edits. On a
fresh clone the git-ignored `config.yml` is absent while the manifest still
records it, so the next add restores it from the committed example before
appending; a missing or corrupt example fails with the exact restore command
instead of a silent default.

`web` and `api` are independent and both require `http`, so `weld add web` and
`weld add api` can be run in either order and both are served by the one `serve`
command. `db` requires `base` and `config`: it is independent of the HTTP
capabilities, so it can be added to a CLI-only project and composes with
`http`/`web`/`api` in any order. `redis` likewise requires only `base` and
`config`; it installs a lazy client the caller owns and never wires Redis into a
service, so it composes with `db` and the HTTP capabilities in any order. `loom`
is opt-in and requires `http`: it wires whatever of `db`, `api` and `redis` is
installed, prunes the Redis client until a provider asks for it, and regenerates
its graph when they are added, in either order.

`weld add module <name>` is separate from the capability set: it generates one
HTTP business module under `internal/modules/<name>` and wires it under
`/api/<name>` (see [`weld add module`](#weld-add-module-stage-5)). It installs
`http` and the `config` it requires automatically when they are absent. With
`--command` it also generates a root command group backed by the module's own
`Service` (see [`weld add command`](#weld-add-command-stage-2b)).

`weld add command <name>` is likewise separate from the capability set: it
generates an independent root command group under `internal/commands/<name>`,
registered through the base app's `RegisterCommand` seam, with no HTTP,
configuration or database requirement.

## Staged architecture

```
new ──▶ base project ──▶ add capability ──▶ add capability ──▶ …
         (go.mod, main.go,   (http: internal/httpserver,   (web: internal/web + internal/httpserver/web_route.go,
          internal/app,       internal/app/serve.go)        api: internal/api + internal/httpserver/api_route.go)
          Makefile)                                             db: db/migrations, db/query, db/tools,
                                                                  internal/data + generated internal/data/sqlc,
                                                                  redis: internal/redisclient + internal/config/redis.go)
```

Every file `weld` writes is recorded in `weld.json` with the capability and
version that produced it. Capabilities are **one-way additive**: later
capabilities extend the project; nothing removes a capability. `weld add module
<name>` sits beside the capability set: it adds one HTTP business module
(`internal/modules/<name>`, recorded under `module:<name>` and listed in the
manifest's `modules`) without touching the capability list.

Capabilities are also **independent and dependency-resolved**. `weld new`
produces a CLI that builds and runs with no HTTP server, no database, no Redis
and no frontend. `web` and `api` each require `http`; `weld add web` or
`weld add api` installs `http` first automatically, so the user never has to know
the order. `web` and `api` can be added in either order and share the one serve
command. `db` and `redis` require only `base` (and `config`), so they never pull
in an HTTP server and can be added to a CLI-only project.

## How capabilities integrate (owned extension points, not replacement)

No capability performs arbitrary text substitution. There are three owned
extension points:

1. **Go command tree** — `internal/app` builds the root command with Cobra and
exposes the contribution seams: `RegisterCommand(func() *cobra.Command)` for a
top-level command, `ConfigureRoot(func(*cobra.Command))` for shared persistent
flags, and `SetDefaultRun` for what a bare invocation does. `http` adds
`internal/app/serve.go`, whose `init` registers the `serve` command, installs
`--config`/`--addr` as root persistent flags and makes a bare `__name__` run the
same serve lifecycle. Commands are the only init-time command registration
point; HTTP routes are not registered at init time.
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

### Migrating a project generated before the Cobra CLI (stage 2a)

`weld new` now generates a Cobra command tree, but `weld add` never rewrites
`internal/app/app.go`. Before installing a new Cobra-based `http` or `loom`
capability into a project with the older hand-rolled dispatcher, weld fails
**during planning**, without changing files. There is no automatic upgrade:
re-scaffold on the current base and port your application changes, or manually
migrate `internal/app/app.go`, any existing `internal/app/serve.go`, the Cobra
`go.mod` dependency and `go.sum` together before retrying. An existing project
can still add capabilities that do not require a new Cobra-based serve file.
New projects are unaffected.

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

## `weld skills`

`weld skills` writes a project-specific agent skill to
`.agents/skills/weld/SKILL.md`. It is generated from `weld.json` and the
capability catalog, never from project source, so the document explains the
installed capabilities without copying a local value or secret. Re-run it after
adding a capability:

```sh
weld skills --dir . --dry-run   # print the plan without writing
weld skills --dir .             # write or refresh SKILL.md
```

The document records the project name and module, each installed capability and
version, what being **installed** does and does not mean, and which files weld
generates. It is honest about the difference between installed and **wired**:
the `api` service is an in-memory development demo that `db` never replaces on
its own, `db` installs a pool and repository but connects nothing, and `redis`
installs a client that never dials or pings. A capability the running binary
knows but this command does not curate is described from the catalog summary,
and an unrecognized one is listed without an invented description.

The write is safe and deterministic:

- **Idempotent.** Re-running it on an unchanged project reports `up to date` and
  leaves the file untouched.
- **Refuses user edits.** The generated file ends with a trailer recording a
  fingerprint of its body. If the file no longer matches — the user edited it,
  not `weld` — `weld skills` refuses to overwrite it rather than lose the user's
  notes, including after a later `weld add` changed the installed set. Delete or
  rename the file to regenerate from scratch. The file is re-checked when the
  plan is applied too, so an edit made between planning and writing is a
  conflict rather than a silent overwrite.
- **Preserves unrelated files.** Only `.agents/skills/weld/SKILL.md` is written;
  sibling files under `.agents/skills/` are untouched.
- **Not in the manifest.** The skill file is deliberately not recorded in
  `weld.json`, so it can be edited freely; `weld` tracks the generated version
  with the fingerprint instead.

The document is derived only from `weld.json` and the catalog, so it is stable
for a given manifest and catalog and contains no timestamps.

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

## `weld add loom` (stage 4)

`weld add loom` is an **opt-in** capability that requires `http` and is never
installed by `web`, `api`, `db`, `redis`, `mail`, `storage` or `cron`. It adds
`internal/di`, a [Loom](https://github.com/Xwudao/loom) dependency graph, and
rewires the single `serve` command to run `InitApp`.

What Loom binds, and why it is not ceremonial:

- `*config.Config` — the shared typed configuration from `internal/config`,
  loaded by an injected `*config.Loader` (never scattered `os.Getenv`), exposing
  the listen address for `http` and the database settings for `db`. The
  `--config` path and the `--addr` flag are carried in the context, so the flag
  outranks the environment, which outranks `config.yml`. When `db` is installed
  the database section is validated: the credentials must come from `config.yml`
  or the environment, there is no invented default, so a database-backed serve
  cannot silently reach a local database, and an error never echoes the dsn.
- `*slog.Logger` — provided by the graph (`NewLogger`) from the base
  `log/slog` factory and injected into the managed server, so startup, a serve
  failure and a graceful shutdown are recorded through one logging protocol
  with no process default logger and no custom logger interface.
- `*pgxpool.Pool` and `data.Repository` (`db`) — the pool parses the dsn lazily
  and is **not** connected at build, test or startup, so no database is needed;
  the constructor returns a cleanup that Loom runs exactly once, whether
  construction later fails or the lifecycle stops normally.
- `api.Service` (`api`) — bound to the repository through a generated adapter
  when `db` is installed, otherwise the default in-memory service.

> **Known issue (separate fix).** The adapter above means that installing `db`
> silently switches an already-installed `api` from the in-memory service to
> PostgreSQL, and a `db`+`loom` graph requires the database settings merely
> because `db` is installed. The intended direction is that `weld add db` only
> installs the capability and the API↔DB wiring is an explicit step (for example
> a future `weld connect api db`), and that an unrelated `serve` does not need
> the database. This is **not** changed here: the config milestone only unifies
> the loader and does not broaden the feature. The generated demo API stays
> in-memory unless that explicit wiring exists.
- `*httpserver.Server` — the composed mux plus its `loom.Hook` start/stop
  lifecycle. `OnStart` binds the listen address synchronously, so a port already
  in use fails `Start`; the serve command waits for a signal or a reported serve
  failure, then stops gracefully.
- `*cron.Scheduler` (`cron`) — the graph root consumes the scheduler, so it is
  always constructed and its `loom.Hook` start/stop run with the server: the
  socket is bound before the scheduler starts and the scheduler stops before the
  server. It starts no jobs until `internal/cron/register.go` registers them, and
  it takes no database or Redis lock.
- `*redis.Client` (`redis`), `*mailsender.Sender` (`mail`) and
  `*objectstore.Store` (`storage`) — declared bindings the default graph never
  depends on, so Loom prunes them and an unrelated serve needs none of those
  settings. Each provider lives in a stable seam (`redis_provider.go`,
  `mail_provider.go`, `storage_provider.go`).
- `*App` — the graph root, holding every bound service.

The graph is capability aware and order independent. `weld add loom` renders
`internal/di/di.go` for the installed set and generates `internal/di/loom_gen.go`
with the real pinned generator; adding `web`, `api`, `db`, `redis`, `mail`,
`storage` or `cron` afterwards regenerates both. Generation runs at add time with
the generator pinned in the
nested `tools/loom` module, not in the application `go.mod`, and a generation
failure rolls the whole plan back so a project is never left with a graph that
does not match `loom_gen.go`. Installed capabilities other than `loom` stay
Loom-free: only the `serve` registration region and the `go.mod` go-directive
region gain a generic extension point.

Because the generated initializer imports `github.com/Xwudao/loom`, installing
`loom` raises the project's Go directive to `go 1.25.0`. The generator's
`x/tools` dependency stays in `tools/loom`.

## `weld add module` (stage 5)

`weld add module <name>` adds a first-class **HTTP business module**:
`internal/modules/<name>` with a typed request and response, a `Service` seam,
and a Go HTTP handler registered under `/api/<name>`. It is a deliberately
small, replaceable example, not persistence: the generated service holds state
only in memory.

- **Name validation.** `<name>` must be a canonical lower-case Go package name
  and URL segment. A Go keyword and the reserved built-in API paths `api`,
  `items` and `openapi` are rejected, as is a module already installed.
- **Dependency install.** It installs `http`, and the `config` that `http`
  requires, automatically when absent, so it works on a fresh CLI project.
- **Ownership.** Every module file is recorded in `weld.json` under
  `module:<name>`, and the module is recorded in the manifest's `modules` list.
  The files are written once and never regenerated, so your edits survive later
  `weld add` commands; an existing unmanaged file under `internal/modules/<name>`
  is a conflict, never an overwrite, and a repeat add is a no-op that still
  succeeds.
- **Wiring is explicit and order independent.**
  - Without Loom, weld writes `internal/httpserver/<name>_route.go` and appends
    one line to the `weld:routes` extension point in
    `internal/httpserver/http.go`, so the route list stays explicit and needs no
    init-time registration.
  - With Loom, the module is provided by its own package's `NewService` and
    registered on the regenerated graph's mux. The graph is re-rendered from the
    manifest's `modules` list, so installing Loom before the module, or the
    module before Loom, both produce a graph that includes it, without weld
    rewriting any module file.
- **Database semantics are unchanged.** With `db` and Loom installed, the graph
  still declares the pool and repository as available bindings only: the default
  composition never constructs them, so serving the module needs no database
  credential.

The generated `README.md` inside the package says all of this, keeps the
`Service` interface and `NewService` symbol as the stable seam, and notes that
the demo service is not persistence. The HTTP surface is a plain
`http.Handler`, so the generated `module_test.go` exercises it with
`net/http/httptest`, with no socket and no database.

With `--command`, `weld add module <name> --command` additionally writes
`internal/commands/<name>` and `internal/app/<name>_command.go`: a root command
group backed by the same `Service` interface and `NewService` constructor the
HTTP handler uses, so the command line and `GET /api/<name>` share one business
layer. On a module that already exists it writes only the command files and the
manifest facet, never rewriting your module service or handler.

## `weld add command` (stage 2b)

`weld add command <name>` adds an **independent root command group** for
non-business or multi-business commands:

- **Independent.** It needs no HTTP server, no configuration and no database,
  so it can be added to a fresh CLI project. It starts no server and never
  starts the cron scheduler: a bare `__name__ <name>` shows the group's help.
- **Ownership.** `internal/commands/<name>/command.go` and the registration file
  `internal/app/<name>_command.go` are written once and recorded in `weld.json`
  under `command:<name>`, so your edits survive later `weld add` commands; a
  repeat add is a no-op that still succeeds.
- **One shared registration mechanism.** The registration file calls the base
  app's `RegisterCommand(func() *cobra.Command)`, the same seam `http` uses for
  `serve`. There is no per-project global dispatcher: `internal/app/app.go`
  stays the one command tree, and weld never rewrites it.
- **A stable seam for business services.** `internal/commands/<name>/command.go`
  exposes `NewCommand`, and the registration file is the editable seam: change
  `NewCommand()` to take the `Service` of one or more modules added with
  `weld add module`, then inject them in the registration, so a command can span
  several business services without weld ever wiring the database or Redis.
- **Name rules.** `<name>` must be a canonical lower-case Go package name. The
  reserved root commands `serve`, `help` and `version` are rejected, and a name
  is either a command or a module, never both: adding a command for an installed
  module's name is a conflict (and the reverse too).
- **Cobra seam.** The base is a Cobra command tree; on a project scaffolded with
  the earlier hand-rolled dispatcher the plan fails during planning, before any
  file changes, with the same migration message `http` and `loom` use.

## Configuration (`weld add http` / `weld add db`)

`config` is a first-class, shared capability, not a base dependency: the base
scaffold stays a minimal CLI with `log/slog` and no `config.yml`. Both `http` and
`db` require `config`, so the first of them installs it and the user never adds
it by hand. `weld add config` alone is also possible; it adds only the loader.

What lands in the project:

- `internal/config/` — typed Go structs (`Config`, `HTTP`, `Database`) with
  `yaml` tags for names only (no validation tags), a loader whose `readFile` and
  `lookupEnv` are injected (`NewLoader`) so it is testable without the
  filesystem or the process environment, and a `Secret` type that redacts itself
  in `fmt`, `log/slog`, JSON and YAML.
- `config.yml` — generated locally and added to `.gitignore`. It is not silently
  omitted: `weld` writes it and records it in the manifest.
- `config.example.yml` — committed, and documents the shape.
- the `go.mod` dependency region gains `gopkg.in/yaml.v3`.

Configuration is read from `config.yml` in the working directory by default;
`--config` overrides the path. Precedence is **command-line flag > environment
variable > `config.yml` > explicit code defaults**. A missing file and an invalid
file are distinct errors and neither is a silent fallback to defaults; an
explicitly set-but-blank `HTTP_ADDR` (or `--addr`) is an error too. A parse error
never quotes the file content, so a malformed secret cannot reach a log record.

`http` uses `config.HTTP.Addr` (default `:8080`). `db` uses a typed
`host`, `port`, `user`, `password`, `name` or an explicit `dsn`, and the database
section is validated only where the database is actually used: an http-only
project never needs it. There is no default credential, and the password may
live in the local `config.yml` for development while a production environment
overrides `DB_PASSWORD` or `DATABASE_URL`. `weld` never logs the value.

`config.yml` and `config.example.yml` carry a `weld:config` marker region that
each capability appends to: `http` appends the `http` section and `db` appends
the `database` section, so a db-only project has no gratuitous http section.
Because the append is marker-based and sentinel-guarded, `weld add db` after
`weld add http` (or the reverse) merges the new section without clobbering the
user's edits — including a database password — and a repeat add is a no-op. The
generated Loom graph uses the same loader (`NewConfigLoader`/`NewConfig`), so the
plain `serve` command and the graph share one configuration path.

Because `config.yml` is git-ignored, a fresh clone has no local file while the
manifest still records it as weld-managed. Each patch that targets `config.yml`
declares the tracked `config.example.yml` as its `bootstrap`, so a later
`weld add http` or `weld add db` restores the local file from the example before
appending its section, instead of failing on the missing extension point. The
restore runs only when the local file is absent, so a local value or comment is
never overwritten, and the restored file is written and recorded in the manifest
with its hash. If the example is absent or no longer carries the marker region,
the plan fails with an actionable restore instruction (for example
`git checkout config.example.yml`) — never a silent default. The generated
runtime is unchanged: `config.Load` still errors on a missing `config.yml` until
the file exists.

## Tests

```sh
go test ./... -count=1
go vet ./...
```

Test conventions:

- No test opens a socket to a service. HTTP behavior is asserted with
  `net/http/httptest` against handlers built from injected routes/fixtures
  (`httpserver.NewHandler`, and `web.Handler` over an `fstest.MapFS`). The
  generated graph tests bind an ephemeral loopback socket only to prove that an
  occupied port fails `Start` and that a serve failure is reported, and close it
  immediately. The generated `internal/httpserver` test occupies a loopback port
  with `httptest` to prove `Serve` reports the bind failure without logging a
  listening line, and closes the occupying server.
- The generated `serve` command is exercised as a Cobra command tree
  (`internal/app/serve_test.go`): the root builds the serve command, the shared
  `--config`/`--addr` flags parse and their precedence resolves, and the
  lifecycle helper runs against a cancellable context — rather than by listening.
- The generated `internal/config` test drives the loader through an injected
  `readFile`/`lookupEnv`: it covers the flag/environment/file/default precedence,
  a missing file, an invalid file that must not echo its content, a blank
  `HTTP_ADDR` that must not fall back to the default, the separate database
  validation, and a `Secret` sentinel that must not survive `fmt`, `slog`, JSON
  or YAML. Scaffold tests prove the first `http`/`db` add installs `config`, that
  a db-only project has no http section, that both add orders merge the sections,
  that an edited address and a local database password survive later adds, and
  that deleting only the git-ignored `config.yml` (a fresh clone) is repaired from
  the committed example in both add orders — with the example persisted, no local
  secret copied into it, and a missing or corrupt example failing actionably.
- The base CLI logs a failed command through its injected `log/slog` logger, and
  a generated `internal/logging` test covers text/JSON formatting, handler
  level filtering and an error record against a fake writer. A scaffold matrix
  test proves the logging wiring composes with `base`, `http`, `api`, `db` and
  `loom` (including `api+db` and `api+db+loom`): the factory is present, no
  generated file reaches for `slog.Default` or the `slog.Info`/`Error` globals,
  and each composed project gofmt/builds/vets/tests.
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
- The scaffold tests for `loom` generate a real project, run the pinned
  generator, and assert the committed `internal/di/loom_gen.go` is generator
  output and reproduces byte for byte (`generate -dry-run`). The generated
  `internal/di/di_test.go` exercises the composed graph with an injected
  `EnvLookup` and an in-memory repository: no database and no `DATABASE_URL`.
  A regression test proves a `db`+`loom` project with `DATABASE_URL` unset still
  builds and tests (the graph tests inject a fake environment) and only
  resolving the graph fails, naming `DATABASE_URL` without inventing or echoing
  a dsn. A second regression runs `serve` with a malformed `DATABASE_URL`
  carrying a sentinel password and asserts the structured log names the problem
  (`invalid PostgreSQL dsn`) without echoing the credential. Combination tests
  cover `http`+`loom`, `db`↔`loom`, `api`↔`loom` and
  `api`+`db`+`loom` in either order, and assert the initializer really constructs
  `NewPool`/`NewRepository`/`NewAPIService` and registers the pool cleanup. Loom
  integration tests skip visibly when the pinned generator cannot be built.
- The scaffold tests for `weld add module` prove the generated module's handler
  in process with `net/http/httptest` (no socket, no database); that a repeat add
  and a dry run write nothing; that a reserved or malformed name and an unmanaged
  file are rejected before any write; that the module package survives a later
  capability add; and that the Loom graph includes the module whether Loom is
  installed before or after it — while a `db`+Loom graph still declares but never
  constructs the pool and repository.
- The scaffold tests for `weld add command` and `weld add module --command`
  prove the generated command group is independent (base + command installs no
  HTTP or config), that a generated command factory test builds and rejects an
  unknown subcommand, that a module command shares the module's `Service`, that
  the `--command` upgrade of an existing module writes only the command files
  and never rewrites a user-edited service, that a Loom module + command builds
  and tests, that reserved names and command/module collisions are rejected
  before any write, and that a custom command never starts the server.
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
internal/template/   capability catalog, module and command templates, placeholder rendering
internal/project/    manifest, marker patching, best-effort rollback apply
internal/scaffold/   create/add/add-module/add-command plans and dependency resolution
internal/skills/     project agent skill generation and safe write
```
