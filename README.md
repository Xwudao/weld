# weld

`weld` is a progressive Go scaffold. Start with a minimal Go CLI, then add
capabilities to the *same* project over time — `weld add web` modifies your
existing project to gain a web frontend instead of generating a second
template. It also drives the assembled project: `weld dev` runs the backend and
frontend together, `weld build` builds the frontend and cross-compiles the
binary, `weld seed` runs the project's database seeders, `weld deploy`
uploads the built binary to the configured host and runs its remote script, and
`weld upgrade` moves an installed capability to the payload this weld ships
without touching a file you changed — `weld dev`, `weld build`, `weld seed` and
`weld deploy` are configured by the optional, user-owned `weld.yml`.

Install the latest patch release with Go 1.23 or newer (the tool itself builds on Go
1.23; a generated project that adds a capability installs Loom and raises its
Go directive to `go 1.25.0`):

```bash
go install github.com/Xwudao/weld/cmd/weld@v0.2.1
```

```bash
weld new demo          # minimal Go CLI: Cobra command tree, version + help
cd demo
weld add web --with-dependencies  # installs http (and loom, config), then the frontend
weld add module items  # JSON business module: typed handlers, DTO validation, {code,msg,data}
weld add enum status draft published  # typed string enum + generated methods
weld add db            # PostgreSQL: SQL migrations + sqlc, pool and repository
weld upgrade           # move installed capabilities to this weld's payloads
weld config example    # print the optional, user-owned weld.yml starter
weld dev               # backend + pnpm frontend dev servers, side by side
weld build             # builds the frontend and cross-compiles the binary
weld seed              # runs the database seeders (seed build tag)
weld deploy            # builds the linux binary, uploads it and runs the remote script
make build             # the Makefile stays as the no-weld fallback
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
weld add enum <name> <value> [value...] [--dir dir] [--dry-run]
weld upgrade [capability...] [--check] [--prune] [--verify vet|build|none] [--dir dir] [--dry-run]
weld gen [enum|loom] [--dir dir] [--dry-run]
weld dev [--dir dir]
weld build [--dir dir]
weld seed [name] [--tag tag] [--dir dir]
weld deploy [--no-build] [--artifact file] [--dir dir]
weld config example
weld config init [--force]
weld list [--dir dir]
weld skills [--dir dir] [--dry-run]
weld version
weld help
```

`weld dev` runs the project's backend and, when the web capability is
installed, the frontend development server under one supervisor. Ctrl-C stops
both; control commands on stdin restart or open either side
(`rs backend|b`, `rs frontend|f`, `rs all|a`, `o`, `status|st`, `help|h`,
`quit`).

`weld build` builds the frontend first when the web capability is installed and
then cross-compiles the Go binary for every configured target. It is the
release path; `make build` stays available as the self-contained, no-weld
fallback.

`weld seed` runs the project's database seeders. The seeders live behind the
`seed` build tag (`//go:build seed`), so the binary `go build` / `weld build`
produce compiles in neither the seed data nor any faker a seeder imports.
`weld seed` compiles the tag in and runs the project's short `seed` command,
optionally naming one seeder (`weld seed items`); the generated `make seed`
target does the same. The tag defaults to `seed` and can be overridden with
`weld.yml`'s `seed.tag` or `weld seed --tag`. It requires the `seed`
capability.

`weld deploy` uploads the built binary and runs the remote deploy script over
ssh, driven by `weld.yml`'s `deploy` section (`host`, `remote_upload_dir`,
`remote_script`, optional `target`). It builds the deploy target first
(`deploy.target`, default `linux/amd64`) and uploads that one artifact, so a
deploy never cross-compiles the unrelated `build.targets` entries; `--no-build`
uploads an existing artifact instead and `--artifact file` names one
explicitly (which also skips the build). The artifact is `scp`'d to
`host:remote_upload_dir` and the remote script is run as `sh <remote_script>
<uploaded-path>`, so the script decides how to swap the running service over.
`--dry-run` prints the `scp`/`ssh` steps without building or running anything.

Both commands read the optional, user-owned `weld.yml` (print a commented
starter with `weld config example`, or write one into a project that has none
with `weld config init`, which keeps an existing file unless `--force` is
given). `weld.yml` is deliberately **not** the
machine-owned `weld.json` manifest: `weld` never rewrites it and `weld add`
never touches it, so your hand-edited build, dev, seed and deploy settings
survive every later capability. Every setting is optional and defaults from the
installed capabilities and the host platform (the deploy target defaults to
`linux/amd64`).

Capabilities:

- `base` (scaffold) — minimal Go CLI on a Cobra command tree with a
  `log/slog` factory (`internal/logging`) whose sink is an injected
  `io.Writer`. Text records are colored by [`tint`](https://github.com/lmittmann/tint)
  and JSON stays slog's handler; `NewFromEnv`, `ParseLevel` and the
  `WithContext`/`FromContext` pair are built in. It ships no configuration and
  no server; its only direct dependencies are Cobra and tint.
- `config` (add) — the shared typed configuration capability: `config.yml` from
  the working directory, environment and flag overrides, and a redacting
  `Secret` type. `environment` (overridden by `APP_ENV`) is `local`,
  `development`, `test` or `production`; it gates development conveniences only
  — currently logging the composed route table at startup — and carries no
  security weight. Required by `loom`, which every added capability requires,
  so `--with-dependencies` installs it too.
- `http` (add) — HTTP server lifecycle, the shared `internal/httpx` toolkit, a
  composable handler builder, a configuration-driven CORS policy, and the single
  `serve` command. Requires `loom`.
  The toolkit owns the `{code,msg,data}` response envelope, the `Router` with
  grouping and named route policies, typed endpoint helpers, raw route methods
  (`Router.Get`, `Router.Post`, ...) for a response the typed envelope does not
  model,
  query/path and JSON
  binding, and the composable middleware chain; the global stack is declared in
  the stable, project-owned `internal/httpserver/middleware.go` and the route
  policies in `internal/httpserver/policy.go`. The router records every route it
  registers (`Routes`, `LogRoutes`, `PrintRoutes`), and the composition logs the
  table with each route's policies at startup in a development environment. Its
  `Serve` takes the injected
  `*slog.Logger`, and `serve` reads the listen address through the shared
  `config` loader. CORS is off until `config.yml`'s `cors` section enables it
  (see [CORS](#cors)); no authentication or rate limiting is enabled by default.
- `web` (add) — React + TypeScript + Vite frontend, served by `http`.
- `db` (add) — PostgreSQL persistence: SQL migrations and queries, sqlc-generated
  code, an injectable connection pool and a repository. Requires `loom` but not
  `http`: it adds a pruned binding and no server.
- `seed` (add) — opt-in database seeding: named, idempotent seeders run by a
  short `seed` command (`make seed`, `weld seed`) over the repositories the Loom
  seed graph injects. The seeders are behind the `seed` build tag, so the
  production binary carries none of them (nor any faker they import) and the
  `seed` command appears only in a seed-tagged build. Requires `db`; installing
  it seeds nothing until you register a seeder. `weld add seeder <name>` writes
  one `seed_<name>.go` per seeder and adds it to
  `internal/data/seeder/register.go`, and repositories are wired in
  `internal/di/seed_providers.go`.
- `redis` (add) — an opt-in Redis client with typed connection configuration.
  Requires `loom` but not `http`; installing it never connects Redis to a
  service.
- `events` (add) — an opt-in in-process event dispatcher: `events.On[E]`
  registers a listener keyed on the event's concrete Go type and `Publish` runs
  every listener for that type synchronously, in registration order, joining
  their errors and isolating a panic. Requires `loom` but not `http`; installing
  it dispatches nothing until you register a listener in
  `internal/events/register.go`. It is not a queue (see
  [`weld add events`](#weld-add-events)).
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
  configuration, the logger and the installed `db`/`redis`/`mail`/`storage`/`events`
  bindings; once `http` is installed it also declares the server graph (the HTTP
  server, the business modules and the cron scheduler)
  with lifecycle start/stop. Requires `config`, and every capability beyond the
  bare CLI requires Loom, so `weld add <capability> --with-dependencies`
  installs Loom and the shared loader with it.

Requirements are never installed silently. `weld add <capability>` on a project
that lacks one of its requirements fails and names the missing capabilities;
`--with-dependencies` installs the whole chain in the same plan. `loom` requires
`config`, and every capability requires `loom`, so the opt-in installs Loom and
the shared loader together and the user never adds `config` by hand. On a fresh project it generates the local `config.yml`
(git-ignored) from the committed `config.example.yml` and appends only the
section the capability needs. Adding another capability later merges its section
into the same file without clobbering the user's edits. On a fresh clone the
git-ignored `config.yml` is absent while the manifest still records it, so the
next add restores it from the committed example before appending; a missing or
corrupt example fails with the exact restore command instead of a silent
default.

`http` requires `loom`; `web` and a business module each require `http`, so
`weld add web` and `weld add module <name>` can be run in either order and both
are served by the one
`serve` command. `db`, `redis`, `mail` and `storage` require `loom` but not
`http`: they add pruned bindings, so they can be added to a CLI-only project and
compose with the HTTP capabilities in any order. `seed` requires `db`: it adds a
short `seed` command over the repositories you wire, and seeds nothing on its
own. `loom` is the foundation, not
opt-in: it declares `commonModule` for every project and the server graph once
`http` is installed, and `weld add` regenerates its graph when a capability
changes the installed set.

`weld add module <name>` is separate from the capability set: it generates one
HTTP business module under `internal/modules/<name>` and registers it under
`/api/<name>` on the Loom server graph (see
[`weld add module`](#weld-add-module-stage-5)). It requires `http` (and the
`loom` and `config` it requires); a missing requirement is an error unless
`--with-dependencies` is given. With `--command` it
also generates a root command group backed by the module's own `Service` (see
[`weld add command`](#weld-add-command-stage-2b)).

`weld add command <name>` is likewise separate from the capability set: it
generates an independent root command group under `internal/commands/<name>`,
registered through the base app's `RegisterCommand` seam, with no HTTP or
database requirement. It requires `loom` (and `config`, which Loom requires); a
missing requirement is an error unless `--with-dependencies` is given, and it
resolves the group's dependencies from its own Loom graph only when a real
subcommand runs.

## Staged architecture

```
new ──▶ base project ──▶ add capability ──▶ add capability ──▶ …
         (go.mod, main.go,   (loom + config + the first    (web: internal/web,
          internal/app,       capability: internal/di,     db: db/migrations, db/query, db/tools,
          Makefile)           internal/config)            internal/data + generated internal/data/sqlc,
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
server, no database, no Redis and no frontend. A capability's requirements are
not installed silently: `weld add <capability>` reports the missing
requirements and `--with-dependencies` installs the chain. `web` and a business
module each require `http` (which requires
`loom`), so
`weld add web --with-dependencies` or
`weld add module <name> --with-dependencies` installs the server stack first
and the user never has to know the order. `web` and a module can be added in
either
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
   `internal/di/di.go` owns the composition, which registers the SPA and the
   installed business modules on one mux. `internal/httpserver/http.go`
   exposes `NewHandler(cfg, logger, routes ...Route)` as the injectable test seam
   and
   `httpserver.Chain(cfg, logger, handler)` applies the project middleware stack
   (declared in the stable `internal/httpserver/middleware.go`, reading the
   cross-origin policy from the configuration). The server graph
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

## `weld upgrade`

`weld upgrade` moves installed capabilities to the template versions the running
`weld` ships. A capability records the version that wrote its files in
`weld.json`, every managed file records the sha256 of what weld last wrote, and
every file carries the **role** its capability declared for it. An upgrade decides
from those three facts — what the payload ships, what weld wrote, what is on disk
— instead of from a heuristic:

```sh
weld upgrade                      # every installed capability
weld upgrade http                 # one capability
weld upgrade --dry-run            # print the plan, write nothing
weld upgrade --check              # CI: fail when anything is out of date
weld upgrade --prune              # also delete retired, unmodified files
weld upgrade --verify=build       # check with go build instead of go vet
weld upgrade --verify=none        # write without checking
```

### Every file has a role

A capability declares the role of each file it ships, and the role decides what an
upgrade may do with it:

| role | example | upgrade behaviour |
|---|---|---|
| `payload` (the default) | `internal/httpx/*.go`, `README.md` | rewritten while you have not changed it; otherwise reported with a diff |
| `patched` | `internal/config/config.go` | the newer payload supplies the skeleton and your live marker regions are spliced into it |
| `seam` | `internal/httpserver/middleware.go`, `internal/httpserver/policy.go`, `internal/di/*_providers.go`, `internal/*/register.go`, the `Makefile`, `.gitignore`, `config.yml` | written once, then yours: **never rewritten**, reported with a diff when the shipped version moves |
| `generated` | `internal/di/di.go`, `internal/di/di_test.go` | re-rendered from the graph, never merged, never pruned |

`go.mod` and `go.sum` belong to the Go toolchain: `go mod tidy`, every build and
`go get` rewrite them, and a project legitimately keeps its own requirements
outside the `weld:deps` region — the region is regenerated, so anything hand
written has to live elsewhere. An upgrade never rewrites them.

**A `patched` file** is a payload file that also carries marker regions another
capability extends. `weld upgrade` renders the newer payload, then splices the
live region bodies into it, so the payload change and everything inside a region
both survive: upgrading `config` brings in the `cors` section while keeping the
`redis` fields. Everything *outside* the regions belongs to weld, so a project
addition to such a file goes inside a marker region.

**A `seam`** is the promise the file itself makes — "weld writes this once and
never regenerates it". An upgrade keeps that promise even when the file still
matches the recorded baseline, which is what stops a payload update from quietly
dropping middleware you added to the chain. When the shipped version of a seam
changes, the upgrade prints the diff and you fold it in; a contract change in a
seam (for example a different function signature) therefore needs the one manual
edit, and the check below makes that visible instead of leaving a broken project.

The regenerated Loom graph (`internal/di/di.go`, `internal/di/di_test.go` and
`internal/di/loom_gen.go`) is reconciled rather than merged: an older or hand
edited graph is re-rendered from the installed capability set and the pinned
generator re-runs, with the same snapshot-and-rollback the check uses.

**A retired file** — one the manifest records as a plain payload file that the
newer payload no longer ships — is reported, and `--prune` deletes it only when it
is unmodified. Seams, `patched` files, generated files and marker-patch targets
are never pruned: they are not the capability's to remove.

### The record is a baseline, and it is only refreshed for what weld wrote

`weld.json` (manifest version 2) marks every record written by this weld with
`baseline: true`, and `RefreshManifest` re-hashes only the paths weld's own
tooling rewrote (`go.mod`, `go.sum`, the Loom initializer). A version 1 manifest
refreshed *every* hash after *every* `weld add`, which absorbed your edits into
the baseline; that is how an upgrade came to believe an edited middleware seam
was untouched and overwrote it. A version 1 manifest is loaded with that fact
recorded, and the upgrade says so in its report: it stays conservative, project
seams are never rewritten, and each file becomes a trusted baseline as weld writes
it.

### It checks the project before it keeps the change

After writing the plan, `weld upgrade` runs a check inside the project and rolls
the entire upgrade back if it fails — so an upgrade can never be the reason a
project stops compiling, and the error you read is the compiler's rather than a
generator's:

```text
weld: go vet ./...: exit code 1
# example.com/demo/internal/httpserver
internal/httpserver/middleware.go:57:24: cfg.CORS undefined (type *config.Config has no field or method CORS)
 (changes rolled back)
```

`--verify` selects the check: `vet` (the default; it compiles the tests too),
`build`, or `none`. The check compares diagnostics rather than only asking whether
the project builds: a project that is already broken stays upgradable, and the
upgrade fails only on a diagnostic it introduced itself. That matters because
migrating a project-owned seam is a manual step — while the seam is migrated and
the payload has not moved yet, the project deliberately does not build, and the
upgrade still has to be able to land.

`--check` fails when weld would write anything at all, including a drifted
generated file or an outstanding seam advisory, which makes it a CI gate rather
than only a version comparison.

### What it deliberately does not do

Upgrading is not a migration engine. A payload change that cannot be expressed as
"this is the new content of a file weld owns" — a renamed configuration key, a
requirement removed from `go.mod`, a seam whose *signature* changed — is reported
for you to apply, because weld performs no text substitution and rewrites nothing
you own. Marker patch snippets are not re-applied either: a shared file moves
forward by splicing the live regions into the newer skeleton, so a *changed
snippet* on an existing region stays manual. A capability whose recorded version
equals the catalog version is never rewritten, so an upgrade is deterministic and
repeatable. The payloads of `weld add module`, `weld add command` and
`weld add enum` are written once and belong to you; `weld upgrade` does not touch
them.

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
a business module's default `Service` is an in-memory development demo that
`db` never replaces on
its own, `db` installs a pool and repository but connects nothing, `redis`
installs a client that never dials or pings, and `events` installs a dispatcher
that publishes nothing. A capability the running binary
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

`db` is independent of `http` and `web`. The HTTP DTOs are never derived from
sqlc rows, and `internal/data` never
imports `net/http`, so the capability can be added to a
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
with the real pinned generator; adding `web`, `db`, `redis`, `mail`,
`storage`, `cron` or a module regenerates both. Generation runs at add time with
the generator pinned in the
nested `tools/loom` module, not in the application `go.mod`, and a generation
failure rolls the whole plan back so a project is never left with a graph that
does not match `loom_gen.go`. Capabilities stay independent of the graph source:
each contributes its own stable provider seam (`redis_provider.go`,
`mail_provider.go`, `storage_provider.go`,
`cron_provider.go`) and a `go.mod` go-directive extension point, while `di.go`
and `loom_gen.go` alone are regenerated.

Because the generated initializer imports `github.com/Xwudao/loom`, installing
`loom` raises the project's Go directive to `go 1.25.0`. The generator's
`x/tools` dependency stays in `tools/loom`.

## `weld add events` (synchronous, in-process)

`weld add events` adds an in-process event dispatcher: `events.On[E](d, listener)`
registers a listener keyed on the event's concrete Go type, and `Publish` runs
every listener registered for that type, synchronously, in registration order.
It requires `loom` but not `http`, so a CLI-only project can dispatch events.

What lands in the project:

- `internal/events/dispatcher.go` — `Publisher` (the interface application code
  depends on), `Listener[T]`, `On[T]`, `Publish` and the concrete `*Dispatcher`.
  `Publish` joins the listeners' errors with `errors.Join`, recovers a panicking
  listener into a logged error, and never logs the event value.
- `internal/events/recorder.go` — `Recorder`, a `Publisher` that records instead
  of delivering, so a test asserts what a handler published with no listener and
  no graph.
- `internal/events/register.go` — the stable, user-editable registration file.
  `weld add events` writes it once and never regenerates it.
- `internal/di/events_provider.go` — the stable Loom provider seam, written when
  `loom` is installed.

Delivery is **synchronous**: `Publish` returns only after every listener
returned, in the caller's goroutine. That is what makes a listener's failure
visible to the caller and the behavior assertable in a test without a worker, a
poll or a wait. It is also a real constraint, and the generated
`internal/events/README.md` says so: a slow listener slows the caller, an event
published inside a database transaction is delivered before the commit, and a
listener that panics is contained but the event is not retried. Moving slow work
behind a durable queue is a queue capability's job; because callers depend on
`events.Publisher` rather than the concrete dispatcher, that change stays in the
wiring.

Installing it dispatches nothing: `internal/events/register.go` registers no
listener, and the Loom graph prunes the dispatcher until a provider the graph
consumes depends on `events.Publisher` — for example by adding the parameter to a
business module's `NewService`, after which the graph constructs the dispatcher
and runs `Register`. A listener that fails to register fails startup, not the
first request.

## `weld add module` (stage 5)

`weld add module <name>` adds a first-class **HTTP business module**:
`internal/modules/<name>` with a typed request and response, a `Service` seam,
and a Go HTTP handler registered under `/api/<name>`. It is a deliberately
small, replaceable example, not persistence: the generated service holds state
only in memory.

- **Name validation.** `<name>` must be a canonical lower-case Go package name
  and URL segment. A Go keyword and the reserved name `api` (the root of the
  module URL space) are rejected, as is a module already installed.
- **Dependency install.** It requires `http` (and the `loom` and `config` it
  requires); a missing requirement is an error unless `--with-dependencies` is
  given, so it works on a fresh CLI project with that flag.
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
  to a fresh CLI project; it requires `loom` (and `config`, which Loom requires),
  which `--with-dependencies` installs, and records a command-specific graph. It
  starts no server and never starts the cron
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

## `weld add enum` and `weld gen`

`weld add enum <name> <value>...` gives the project typed string enumerations
without pulling in an external code generator. It writes
`internal/enums/<name>.go` — the exported type and its constants — once, then
generates `internal/enums/<name>_gen.go` with the derived methods:

```bash
weld add enum status draft published in_progress
```

```go
// internal/enums/status.go — human-owned, written once
type Status string

const (
	StatusDraft      Status = "draft"
	StatusPublished  Status = "published"
	StatusInProgress Status = "in_progress"
)
```

```go
// internal/enums/status_gen.go — tool-owned, regenerated by `weld gen enum`
func (v Status) String() string
func StatusValues() []Status
func StatusNames() []string
func ParseStatus(raw string) (Status, error)
func (v Status) IsValid() bool
func (v Status) MarshalText() ([]byte, error)
func (v *Status) UnmarshalText(text []byte) error
func (v Status) Value() (driver.Value, error)
func (v *Status) Scan(src any) error
```

One enum is one pair of files: the definition file and its generated methods.
`<name>` is lower_snake_case and the exported type is its CamelCase form
(`config_key` -> `ConfigKey`), and the generated file is `<name>_gen.go`.
The definition file is recorded in `weld.json` and never rewritten, so a repeat
add is a no-op and your edits survive. Detection is by AST — the source of truth
is the real `type ... string` and its constants, not a magic comment — so no
type checker and no external tool are involved. A constant without its own type
inherits the previous spec's type within the same `const` block; a spec that
repeats the previous value is an alias and is skipped. Only string enumerations
with at least one constant are detected.

Adding a value is a normal edit to the definition file followed by a refresh:

```bash
# edit internal/enums/status.go to add:
#   StatusArchived Status = "archived"
weld gen enum          # rewrites internal/enums/status_gen.go
weld gen enum --check  # CI: fail when a generated file is stale or missing
weld gen enum --prune  # delete a *_gen.go whose enum no longer exists
```

`weld gen` with no subcommand runs every generator that applies to the project:
the enum generator when `internal/enums` exists, and `weld gen loom` when Loom is
installed. `weld gen loom` rebuilds `internal/di/loom_gen.go` with the generator
pinned in `tools/loom` — the same generator `make generate` runs, invoked
without the make fallback. The `make generate` target is kept as the no-weld
fallback for the Loom graph.

A `*_gen.go` file is only overwritten when it carries the generated header, so a
user file that happens to share the name is never clobbered. Use a pointer
(`*Status`) for an optional field: the zero value `""` is not a valid value and
`ParseStatus` fails closed on anything undeclared.

## Configuration

`config` is a first-class, shared capability, not a base dependency: the base
scaffold stays a minimal CLI with `log/slog` and no `config.yml`. `loom` requires
`config`, and every added capability requires `loom`, so any `weld add` installs
it and the user never adds it by hand.

What lands in the project:

- `internal/config/` — typed Go structs (`Config`, `HTTP`, `CORS`, `Database`)
  with `yaml` tags for names only (no validation tags), a loader whose `readFile`
  and `lookupEnv` are injected (`NewLoader`) so it is testable without the
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
each capability appends to: `http` appends the `http` and `cors` sections and
`db` appends
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

## CORS

The `http` capability ships a cross-origin policy, wired but **off by default**.
That default is deliberate: the built SPA is served by the same server, and the
generated Vite dev server proxies `/api`, so a browser is already same-origin and
needs no CORS header at all. Turning CORS on is a `config.yml` edit, not a Go
edit:

```yaml
cors:
  enabled: true
  allowed_origins: ["http://localhost:5173", "https://app.example.com"]
  allowed_methods: [GET, POST, PUT, PATCH, DELETE, HEAD, OPTIONS]
  allowed_headers: [Content-Type, Authorization, X-Request-Id]
  exposed_headers: []
  allow_credentials: false
  max_age_seconds: 600
```

What the policy does, and what it deliberately does not:

- **A preflight is answered before the router.** `internal/httpserver/middleware.go`
  installs `httpx.CORS` as the outermost middleware, so an `OPTIONS` preflight
  never reaches the mux, a route handler or a named route policy. A preflight is
  browser bookkeeping rather than application traffic, so it is not access-logged.
- **It fails closed and it fails loudly.** An enabled section with no origin, an
  entry that is not an origin (a trailing slash, a missing scheme, a pasted URL
  with a path), a negative `max_age_seconds` and the invalid
  `"*"`-with-`allow_credentials` pair are all startup errors, so a
  misconfiguration never silently allows or blocks every origin:

  ```text
  config: cors.allowed_origins entry "http://localhost:5173/" is not an origin;
  use scheme://host[:port] with no path and no trailing slash, or "*"
  ```

  A preflight that names a method or a header the policy does not allow is a
  `403` in the shared `{code,msg,data}` envelope, so the developer reads a real
  error instead of the browser's opaque CORS failure.
- **The headers are already on the response when anything later refuses it.**
  Because CORS runs first, a `401`, `403` or `500` from a policy or a handler is
  still readable by the allowed origin, instead of surfacing as an anonymous CORS
  error. A refused origin gets no CORS header on an actual request but still
  reaches the handler: CORS decides which origins may *read* a response, never
  who may make the request. Authorization stays in the named route policies,
  which CORS never bypasses.
- **It is a correct-by-construction implementation, not a header stamp.**
  `internal/httpx/cors.go` matches origins exactly (case-insensitively, port
  included) or by the `"*"` wildcard, echoes the origin rather than `"*"` when
  credentials are allowed, always allows the CORS-safelisted request headers,
  echoes the requested headers a preflight asked for, emits the full
  `Vary: Origin, Access-Control-Request-Method, Access-Control-Request-Headers`
  set, and clamps `Access-Control-Max-Age` to 24h. It depends only on the
  standard library, so it stays usable by a project that configures CORS in code.
- **Deployments can override every value.** `CORS_ENABLED`,
  `CORS_ALLOWED_ORIGINS` (comma-separated), `CORS_ALLOWED_METHODS`,
  `CORS_ALLOWED_HEADERS`, `CORS_EXPOSED_HEADERS`, `CORS_ALLOW_CREDENTIALS` and
  `CORS_MAX_AGE_SECONDS` follow the shared precedence (environment over file), so
  an origin list is not baked into the image.

`httpx.CORS` is a general middleware; the generated
`internal/httpserver/middleware.go` is the one place that translates the typed
`config.CORS` section into it, and it is written once and never regenerated, so a
project can reorder it, replace it or add its own middleware beside it.

A project scaffolded by an earlier weld does not gain the policy from a repeat
`weld add http`: a repeat add of an installed capability is a no-op that only
reports the new files as drift, and the `weld:http:installed` sentinel means the
config region is not re-appended either. `weld upgrade` is the path that closes
that gap. It writes the payload files the project never touched
(`internal/httpx/cors.go`, `internal/httpx/cors_test.go`), it brings the `cors`
section into `internal/config/config.go` by splicing the live marker regions into
the newer payload, and it reports the one file it will not touch — the stable,
project-owned `internal/httpserver/middleware.go` — with the unified diff that
adds the `httpx.CORS` entry. Two parts stay manual, on purpose: the
`internal/httpserver/middleware.go` seam itself, because the project owns it, and
the commented `cors:` block that lives in a patch snippet — a snippet already
present in a region is never re-inserted, so add the block by hand or set the
`CORS_*` environment variables. The policy is off by default either way. See
[`weld upgrade`](#weld-upgrade).

## Tests

```sh
go test ./... -count=1
go vet ./...
```

Test conventions:

- No test opens a socket to a service. HTTP behavior is asserted with
  `net/http/httptest` against handlers built from injected routes/fixtures
  (`httpserver.NewHandler(cfg, logger, routes...)`, and `web.Handler` over an
  `fstest.MapFS`). The generated `internal/httpx` test covers the envelope
  writers, typed binding (content type, body limit, unknown field, trailing
  JSON) and the middleware (order, request id, recovery, Flusher/Hijacker
  forwarding) without a socket; its `cors_test.go` covers the preflight answered
  before the handler, the refused origin, method and header, the safelisted
  header, the actual request's headers, the plain `OPTIONS` left to the router,
  the wildcard never paired with credentials, the case-insensitive match and the
  `Vary` and max-age output. The generated graph tests bind an ephemeral
  loopback socket only to prove that an
  occupied port fails `Start` and that a serve failure is reported, and close it
  immediately. The generated `internal/httpserver` test occupies a loopback port
  with `httptest` to prove `Serve` reports the bind failure without logging a
  listening line, and closes the occupying server; it also proves the composed
  chain reads the configured `cors` section (a preflight is answered) and sends
  no CORS header while it is disabled.
- The generated `serve` command is exercised as a Cobra command tree
  (`internal/app/serve_test.go`): the root builds the serve command, the shared
  `--config`/`--addr` flags parse and their precedence resolves, and the
  lifecycle helper runs against a cancellable context — rather than by listening.
- The generated `internal/config` test drives the loader through an injected
  `readFile`/`lookupEnv`: it covers the flag/environment/file/default precedence,
  a missing file, an invalid file that must not echo its content, a blank
  `HTTP_ADDR` that must not fall back to the default, the separate database
  validation, and a `Secret` sentinel that must not survive `fmt`, `slog`, JSON
  or YAML. It also covers CORS: an omitted section is off with usable defaults,
  the typed section and the comma-separated `CORS_*` overrides load, a malformed
  override is an error, and the fail-closed validation rejects an enabled policy
  with no origin, a trailing slash, a missing scheme, a path, the
  wildcard-with-credentials pair and a negative max age while accepting an exact
  origin, the wildcard and a localhost development origin. Scaffold tests prove
  the first `http`/`db` add installs `config`, that
  a db-only project has no http section, that both add orders merge the sections,
  that an edited address and a local database password survive later adds, and
  that deleting only the git-ignored `config.yml` (a fresh clone) is repaired from
  the committed example in both add orders — with the example persisted, no local
  secret copied into it, and a missing or corrupt example failing actionably.
- The base CLI logs a failed command through its injected `log/slog` logger, and
  a generated `internal/logging` test covers tint text records (color on and
  off, the painted statement, the `time.DateTime` clock), JSON formatting,
  handler-level filtering, `ParseLevel`, `OptionsFromEnv`/`NewFromEnv` and the
  context helpers against a fake writer. A scaffold matrix
  test proves the logging wiring composes with `base`, `http`, `db` and
  `loom` (including `db+loom`): the factory is present, no
  generated file reaches for `slog.Default` or the `slog.Info`/`Error` globals,
  and each composed project gofmt/builds/vets/tests.
- The scaffold tests generate a real project, run `gofmt`, `go build`, `go vet`
  and the generated `go test ./...`, so the composed files are compiled rather
  than only string-matched.
- The generated module tests use `net/http/httptest` with an injected fake
  `Service`, so the handler is exercised with no socket and no database; they
  exercise the no-input route, query binding,
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
  must produce no diff. Combination tests cover `base`→`db` and `web`→`db` in
  either order.
- The scaffold tests for `loom` generate a real project, run the pinned
  generator, and assert the committed `internal/di/loom_gen.go` is generator
  output and reproduces byte for byte (`generate -dry-run`). The generated
  `internal/di/di_test.go` is module-independent: it exercises configuration and
  the server lifecycle with no database and no `DATABASE_URL`,
  and adding a module leaves it byte-for-byte unchanged.
  A regression test proves a `db`+`loom` project with `DATABASE_URL` unset still
  builds and tests (the graph tests inject a fake environment) and only
  resolving the graph fails, naming `DATABASE_URL` without inventing or echoing
  a dsn. A second regression runs `serve` with a malformed `DATABASE_URL`
  carrying a sentinel password and asserts the structured log names the problem
  (`invalid PostgreSQL dsn`) without echoing the credential. Combination tests
  cover `http`+`loom` and `db`↔`loom` in either order, and assert the
  initializer really constructs
  `NewPool`/`NewRepository` and registers the pool cleanup. Loom
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
- Coverage includes create, dependency resolution, add, repeat (idempotency),
  unmanaged-file conflicts, dry-run, no unintended edits, and the extension
  point format (two capabilities into one region in either order, duplicate
  inserts, malformed markers, and preserved user text).
- The `weld upgrade` tests prove a project this weld wrote is a no-op, that a
  file an older payload wrote is updated while its recorded version moves
  forward, that a new payload file is written, that an edited file produces an
  advisory carrying the project's own line and is never overwritten, that a
  deleted file is never resurrected, that a retired file is kept until `--prune`
  and never deleted once modified, that a drifted generated Loom graph is
  reconciled and regenerated, that a capability this weld no longer ships is
  reported rather than fatal, and that a name the project does not have is an
  error. Four of them are regression guards for the ownership model: a
  project-owned seam is never rewritten even when its recorded hash matches the
  edit, a payload file recorded under the capability that patched it is still
  upgraded by the capability that ships it and keeps the live marker region, a
  generated DI file is never treated as retired (so `--prune` cannot delete the
  graph), and a plan that would not compile is rolled back while a project that
  already fails the check is written with a note. The CLI tests cover `--check`,
  `--dry-run` writing nothing, the apply path, `--verify` selection and the seam
  report. `internal/project` covers the unified diff (a replaced line, an
  insertion into an empty file, two hunks for distant changes, identical content
  and the bound on huge input), marker-region splicing (live regions kept,
  orphaned regions reported, a new region shipped, both comment styles, an
  unclosed region rejected), a scoped `RefreshManifest` and a patch that extends a
  file without taking it over; `internal/semver` covers version ordering and the
  refusal to compare a version it cannot parse.
- The events capability ships its own descriptor test (it requires `loom` and
  never `http`, writes no shared file, adds no dependency, and registers
  listeners only through the stable file) and its dispatcher test, which runs in
  the generated project: registration order, keying on the concrete type, joined
  errors, a recovered panic, the event value staying out of the log, nil safety
  and concurrent publish/register under `-race`. A scaffold test generates a
  project, registers a listener, and asserts delivery through the generated
  package's own `go test` run.

## Layout

```
cmd/weld/            CLI entry point
internal/cli/        argument parsing and output
internal/template/   capability catalog, module and command templates, placeholder rendering
internal/project/    manifest, marker regions and splicing, unified diff, best-effort rollback apply
internal/scaffold/   create/add/upgrade/add-module/add-command plans and dependency resolution
internal/semver/     comparison of the dotted capability versions weld records
internal/skills/     project agent skill generation and safe write
```
