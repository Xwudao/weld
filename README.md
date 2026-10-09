# weld

`weld` is a progressive Go scaffold. Start with a minimal Go CLI, then add
capabilities to the *same* project over time — `weld add web` modifies your
existing project to gain a web frontend instead of generating a second
template.

Install the latest patch release with Go 1.23 or newer (the tool itself builds on Go
1.23; a generated project that adds any capability installs Loom and raises its
Go directive to `go 1.25.0`):

```bash
go install github.com/Xwudao/weld/cmd/weld@v0.1.1
```

```bash
weld new demo          # minimal Go CLI: Cobra command tree, version + help
cd demo
weld add web           # installs http, then the React + TS + Vite frontend
weld add api           # JSON API: typed handlers, DTO validation, {code,msg,data}
weld add db            # PostgreSQL: SQL migrations + sqlc, pool and repository
make build             # builds the frontend, embeds it, builds the app
make run ARGS=serve    # serves the frontend and /api on http://localhost:8080
```

## Embedded templates

The CLI, catalog, planner and declarative payloads live in this repository.
`internal/weldtemplate` embeds `capabilities/`, `modules/` and `commands/`
through `weldtemplate.FS()`. A built `weld` carries the whole scaffold; no
separate template checkout, module replacement or network access is needed at
runtime. Template changes and the CLI are tested and released together.

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
  `Secret` type. `environment` (overridden by `APP_ENV`) is `local`,
  `development`, `test` or `production`; it gates development conveniences only
  — currently logging the composed route table at startup — and carries no
  security weight. Installed automatically with `loom`, which every added
  capability requires.
- `http` (add) — HTTP server lifecycle, the shared `internal/httpx` toolkit, a
  composable handler builder, and the single `serve` command. Requires `loom`.
  The toolkit owns the `{code,msg,data}` response envelope, the `Router` with
  grouping and named route policies, typed endpoint helpers, query/path and JSON
  binding, and the composable middleware chain; the global stack is declared in
  the stable, project-owned `internal/httpserver/middleware.go` and the route
  policies in `internal/httpserver/policy.go`. The router records every route it
  registers (`Routes`, `LogRoutes`, `PrintRoutes`), and the composition logs the
  table with each route's policies at startup in a development environment. Its
  `Serve` takes the injected
  `*slog.Logger`, and `serve` reads the listen address through the shared
  `config` loader. No authentication, CORS or rate limiting is enabled by
  default.
- `web` (add) — React + TypeScript + Vite frontend, served by `http`.
- `api` (add) — JSON HTTP API: typed, self-validating DTOs and routes declared
  once with the `internal/httpx` typed helpers, served by `http`. Responses are
  wrapped in the shared `{code,msg,data}` envelope; the mount prefix comes from
  the server graph, so the surface can be versioned without changing the API
  package.
- `db` (add) — PostgreSQL persistence: SQL migrations and queries, sqlc-generated
  code, an injectable connection pool and a repository. Requires `loom` but not
  `http`: it adds a pruned binding and no server.
- `redis` (add) — an opt-in Redis client with typed connection configuration.
  Requires `loom` but not `http`; installing it never connects Redis to a
  service.
- `mail` (add) — an opt-in SMTP sender with typed, secret-redacted
  configuration and an explicit TLS policy. Requires `loom` but not `http`;
  installing it never sends and never connects.
- `storage` (add) — an opt-in S3-compatible object-storage client. Requires
  `loom` but not `http`; installing it creates no bucket and uploads nothing.
- `cron` (add) — an opt-in in-process scheduler that starts and stops with the
  `serve` command and never with a short command. Requires `http` (and therefore
  `loom`); installing it schedules nothing until you register jobs in
  `internal/cron/register.go`.
- `loom` (add) — the compile-time [Loom](https://github.com/Xwudao/loom)
  dependency-injection foundation. Its `commonModule` always wires the
  configuration, the logger and the installed `db`/`redis`/`mail`/`storage`
  bindings; once `http` is installed it also declares the server graph (the HTTP
  server, the JSON API service, the business modules and the cron scheduler)
  with lifecycle start/stop. Requires `config` and is installed automatically:
  every `weld add` produces a Loom project.

`loom` requires `config`, and every capability requires `loom`, so any
`weld add` installs Loom and the shared loader together and the user never adds
`config` by hand. On a fresh project it generates the local `config.yml`
(git-ignored) from the committed `config.example.yml` and appends only the
section the capability needs. Adding another capability later merges its section
into the same file without clobbering the user's edits. On a fresh clone the
git-ignored `config.yml` is absent while the manifest still records it, so the
next add restores it from the committed example before appending; a missing or
corrupt example fails with the exact restore command instead of a silent
default.

`http` requires `loom`; `web` and `api` each require `http`, so `weld add web`
and `weld add api` can be run in either order and both are served by the one
`serve` command. `db`, `redis`, `mail` and `storage` require `loom` but not
`http`: they add pruned bindings, so they can be added to a CLI-only project and
compose with the HTTP capabilities in any order. `loom` is the foundation, not
opt-in: it declares `commonModule` for every project and the server graph once
`http` is installed, and `weld add` regenerates its graph when a capability
changes the installed set.

`weld add module <name>` is separate from the capability set: it generates one
HTTP business module under `internal/modules/<name>` and registers it under
`/api/<name>` on the Loom server graph (see
[`weld add module`](#weld-add-module-stage-5)). It installs `http` (and the
`loom` and `config` it requires) automatically when absent. With `--command` it
also generates a root command group backed by the module's own `Service` (see
[`weld add command`](#weld-add-command-stage-2b)).

`weld add command <name>` is likewise separate from the capability set: it
generates an independent root command group under `internal/commands/<name>`,
registered through the base app's `RegisterCommand` seam, with no HTTP or
database requirement. It installs `loom` (and `config`) automatically and
resolves the group's dependencies from its own Loom graph only when a real
subcommand runs.

## Staged architecture

```
new ──▶ base project ──▶ add capability ──▶ add capability ──▶ …
         (go.mod, main.go,   (loom + config + the first    (web: internal/web,
          internal/app,       capability: internal/di,     api: internal/api,
          Makefile)           internal/config)            db: db/migrations, db/query, db/tools,
                                                           internal/data + generated internal/data/sqlc,
                                                           modules: internal/modules/<name>,
                                                           commands: internal/commands/<name>)
```

Every file `weld` writes is recorded in `weld.json` with the capability and
version that produced it. Capabilities are **one-way additive**: later
capabilities extend the project; nothing removes a capability. `weld add module
<name>` sits beside the capability set: it adds one HTTP business module
(`internal/modules/<name>`, recorded under `module:<name>` and listed in the
manifest's `modules`) without touching the capability list.

Capabilities are also **independent and dependency-resolved**. `weld new`
produces a bare CLI that builds and runs with no Loom, no configuration, no HTTP
server, no database, no Redis and no frontend. Any `weld add` installs `loom` and
`config`; `web` and `api` each require `http` (which requires `loom`), so
`weld add web` or `weld add api` installs the server stack first automatically
and the user never has to know the order. `web` and `api` can be added in either
order and share the one serve path. `db`, `redis`, `mail` and `storage` require
`loom` but not `http`, so they never pull in a server and can be added to a
CLI-only project.

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
2. **Go route composition** — the regenerated Loom server graph in
   `internal/di/di.go` owns `newMux`, which registers the SPA, the JSON API and
   the installed business modules on one mux. `internal/httpserver/http.go`
   exposes `NewHandler(logger, routes ...Route)` as the injectable test seam and
   `httpserver.Chain(logger, handler)` applies the project middleware stack
   (declared in the stable `internal/httpserver/middleware.go`). The server graph
   wraps the mux it composes with `Chain`, so production and tests serve
   identical middleware. Composition is explicit and in one place: no global
   route registry, no per-capability route file, no init-time route
   registration.
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

`weld add api` is a stage-2 capability: an HTTP contract of typed, self-validating
DTOs and routes declared once with the `internal/httpx` typed helpers. It
requires `http` and does not require `web`. Its routes are registered by the
Loom server graph's `newMux`, which mounts them under `httpserver.APIPrefix`; no
separate route file is written, and a version change is a one-line edit to that
stable constant.

The generated `go.mod` pins the published `github.com/Xwudao/go-validate
v0.2.0`, which provides the `Spec`/`Constraint` API the DTO `Validate` methods
use. A generated API project builds without a sibling checkout or a local module
replacement.

## `weld add db` (stage 3)

`weld add db` is a persistence capability that requires `loom` (and its
`config`) but not `http`. It keeps SQL authoritative: `db/migrations` holds the
schema, `db/query` the named queries, and `sqlc.yaml` reads both to generate
`internal/data/sqlc`. The
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
CLI-only project and composes with the HTTP capabilities in any order. Loom
declares the pool and repository as bindings but prunes them until a provider
asks for the repository, so an unrelated serve needs no database credential.

## `weld add loom` (stage 4)

`weld add loom` adds `internal/di`, a
[Loom](https://github.com/Xwudao/loom) dependency graph, and installs the
`serve` command that runs `InitApp`. It is not opt-in: every capability except
the bare base CLI requires `loom` (directly or through `http`) and `weld add`
pulls it in, so a project is always either the base CLI or a Loom project. It
requires `config`.

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
- `api.Service` (`api`) — the default in-memory development service
  (`api.NewService()`). Installing `db` or `redis` does **not** switch it: the
  graph binds the stable `NewAPIService` provider in `internal/di/api_provider.go`,
  which you edit to inject a repository-backed service.
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

The graph is capability aware and order independent. `weld add` renders
`internal/di/di.go` for the installed set and generates `internal/di/loom_gen.go`
with the real pinned generator; adding `web`, `api`, `db`, `redis`, `mail`,
`storage`, `cron` or a module regenerates both. Generation runs at add time with
the generator pinned in the
nested `tools/loom` module, not in the application `go.mod`, and a generation
failure rolls the whole plan back so a project is never left with a graph that
does not match `loom_gen.go`. Capabilities stay independent of the graph source:
each contributes its own stable provider seam (`api_provider.go`,
`redis_provider.go`, `mail_provider.go`, `storage_provider.go`,
`cron_provider.go`) and a `go.mod` go-directive extension point, while `di.go`
and `loom_gen.go` alone are regenerated.

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
  and URL segment. A Go keyword and the reserved built-in API paths `api` and
  `items` are rejected, as is a module already installed.
- **Dependency install.** It installs `http` (and the `loom` and `config` it
  requires) automatically when absent, so it works on a fresh CLI project.
- **Ownership.** Every module file is recorded in `weld.json` under
  `module:<name>`, and the module is recorded in the manifest's `modules` list.
  The files are written once and never regenerated, so your edits survive later
  `weld add` commands; an existing unmanaged file under `internal/modules/<name>`
  is a conflict, never an overwrite, and a repeat add is a no-op that still
  succeeds.
- **Wiring is explicit, through one Loom path.** The module is provided by its
  own package's `NewService` and registered on the Loom server graph's mux. The
  graph is re-rendered from the manifest's `modules` list, so a module added at
  any time is included without weld rewriting any module file, and no separate
  route file is written. Because the graph binds `NewService`, you may change its
  signature — for example to take `data.Repository` after `weld add db` — and the
  regenerated composition test serves the module through a generated fake instead
  of `NewService`, so the test never depends on the constructor's signature.
- **Database semantics are unchanged.** The graph declares the pool and
  repository as available bindings only: the default composition never constructs
  them, so serving the module needs no database credential.

The generated `README.md` inside the package says all of this, keeps the
`Service` interface and `NewService` symbol as the stable seam (`NewDemoService`
is the always zero-argument fallback), and notes that the demo service is not
persistence. The HTTP surface is a plain `http.Handler`, so the generated
`module_test.go` exercises it with `net/http/httptest`, with no socket and no
database.

With `--command`, `weld add module <name> --command` additionally writes
`internal/commands/<name>` and `internal/app/<name>_command.go`: a root command
group backed by the same `Service` interface and `NewService` constructor the
HTTP handler uses, so the command line and `GET /api/<name>` share one business
layer. On a module that already exists it writes only the command files and the
manifest facet, never rewriting your module service or handler. The group also
gets a stable command graph in `internal/di/<name>_graph.go` and resolves the
module's `Service` lazily through it (see
[`weld add command`](#weld-add-command-stage-2b)).

## `weld add command` (stage 2b)

`weld add command <name>` adds an **independent root command group** for
non-business or multi-business commands:

- **Independent.** It needs no HTTP server and no database, so it can be added
  to a fresh CLI project; it installs `loom` (and `config`) and records a
  command-specific graph. It starts no server and never starts the cron
  scheduler: a bare `__name__ <name>` shows the group's help.
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
- **Loom DI is added in place.** The command also gets a stable command graph
  `internal/di/<name>_graph.go` and a lazy registration: it
  includes `commonModule`, the shared provider set `di.go` regenerates
  (configuration, logger and the installed db/redis/mail/storage bindings), plus
  the command's dependency root (`<name>.Deps`), and never the HTTP server or the
  cron scheduler. Because the shared providers live in the regenerated module
  rather than in the command graph, a capability installed *after* the command
  graph was written still reaches it — `weld add db` extends `commonModule`,
  and the stable command graph is not rewritten. Loom prunes every provider a
  subcommand does not consume, so a short command constructs nothing beyond what
  its `Deps` asks for, and the generated command package does not import the Loom
  runtime — it resolves the graph through the shared `internal/commandkit` seam
  only when a real subcommand runs, so help, `version` and unrelated commands
  never build it. Injecting a dependency is a field on `Deps` plus a parameter on
  `NewDeps`: a provider already in `commonModule` (configuration, logger, and the
  installed db/redis/mail/storage bindings) needs no graph edit, and the
  generated `status` subcommand is the worked example. Each graph keeps its own
  instance cache, so the HTTP graph and a
  command graph never share a process-wide singleton. The graph is written from
  the stable command template; a generated file that already exists and was
  edited is refused rather than overwritten.
- **Name rules.** `<name>` must be a canonical lower-case Go package name. The
  reserved root commands `serve`, `help` and `version` are rejected, and a name
  is either a command or a module, never both: adding a command for an installed
  module's name is a conflict (and the reverse too).

## Configuration

`config` is a first-class, shared capability, not a base dependency: the base
scaffold stays a minimal CLI with `log/slog` and no `config.yml`. `loom` requires
`config`, and every added capability requires `loom`, so any `weld add` installs
it and the user never adds it by hand.

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
generated Loom graph uses the same loader (`NewConfigLoader`/`NewConfig`), so
`serve` has one configuration path.

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
loader is unchanged: `config.Load` still errors on a missing `config.yml` until
the file exists.

## Tests

```sh
go test ./... -count=1
go vet ./...
```

Test conventions:

- No test opens a socket to a service. HTTP behavior is asserted with
  `net/http/httptest` against handlers built from injected routes/fixtures
  (`httpserver.NewHandler(logger, routes...)`, and `web.Handler` over an
  `fstest.MapFS`). The generated `internal/httpx` test covers the envelope
  writers, typed binding (content type, body limit, unknown field, trailing
  JSON) and the middleware (order, request id, recovery, Flusher/Hijacker
  forwarding) without a socket. The generated graph tests bind an ephemeral
  loopback socket only to prove that an
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
  `Service`, so the handler is exercised with no socket and no database; the
  module tests additionally exercise the no-input route, query binding,
  validation failure, the administrator policy rejection and a raw response.
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
  before any write, and that a custom command never starts the server. They
  also cover automatic Loom installation, adding commands before or after other
  capabilities, stable command graphs surviving later adds and user edits,
  unused `commonModule` providers being pruned, short commands not opening a
  database or starting the server, and a module whose `NewService` signature
  changes after `weld add db` still building and testing.
- The API integration tests use the published `go-validate v0.2.0`; no sibling
  checkout or local module replacement is needed. Generated projects also
  build independently. Set `WELD_GO_VALIDATE_DIR` only when intentionally
  testing an unpublished local change to that dependency.
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
