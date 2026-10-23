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
weld add jwt --with-dependencies  # HS256 bearer tokens + middleware (installs http)
weld add user --with-dependencies  # users table, repository, bcrypt login + JWT sessions
weld db migrate up     # apply migrations with the pinned goose tool
weld db create add_users  # timestamped goose migration skeleton
weld doctor            # read-only project health checks
weld upgrade           # move installed capabilities to this weld's payloads
weld config example    # print the optional, user-owned weld.yml starter
weld dev               # backend + pnpm frontend dev servers, side by side
weld build             # builds the frontend and cross-compiles the binary
weld docker init       # scaffolds the user-owned Dockerfile and .dockerignore
weld seed              # runs the database seeders (seed build tag)
weld test              # runs go test, pointing SQL tests at database_test
weld deploy            # builds the linux binary, uploads it and runs the remote script
make build             # the Makefile stays as the no-weld fallback
make run ARGS=serve    # serves the frontend and /api on http://localhost:8080
```

## Scaffold first, then customize

Create business modules with `weld add module <name>` before editing their
services, DTOs and handlers. Likewise create commands, enums and seeders through
their `weld add` commands first. A handwritten directory does not register a
module: the server graph uses the `modules` record in `weld.json`, not a
filesystem scan. Do not manually edit the manifest to adopt existing code;
back it up and migrate it into a weld-created skeleton.

Never put business wiring in `internal/di/di.go`, `di_test.go` or `loom_gen.go`.
Declare custom providers in the stable `providers.go`; with HTTP installed,
consume custom route services in `NewProjectRoutes` (`http_routes.go`) and
server-only initialization/background services in `NewAppDependencies`
(`app_provider.go`). Both files are project-owned seams, written once.
Modules created by weld are already mounted: do not mount them again.
Validate business configuration in its consuming provider; return errors to
abort startup, and attach background work through Loom lifecycle hooks.
The startup seam runs before `NewServer` and must not depend on the server or
scheduler. Short commands do not consume it. Run `make generate` after changing
provider signatures, and test routes and startup behavior as well as compilation.

Adding/upgrading capabilities refuses to replace modified generated DI sources,
including during `--dry-run`, and displays a diff. Migrate handwritten wiring
into the stable seams and restore the original generated source before retrying;
never change baseline hashes to bypass the conflict. Missing sources can still
be regenerated. For legacy sources without trustworthy baselines, review and
preserve their wiring, then remove only the generated source to regenerate it. Existing projects gain the new seams with
`weld upgrade loom` (resolve any generated-source conflict first).
`weld skills` teaches this workflow even before the first module is installed.

## Embedded templates

The CLI, catalog, planner and declarative payloads live in this repository.
`internal/weldtemplate` embeds `capabilities/`, `modules/` and `commands/`
through `weldtemplate.FS()`. A built `weld` carries the whole scaffold; no
separate template checkout, module replacement or network access is needed at
runtime. Template changes and the CLI are tested and released together.

## Commands

```
weld new <name> [--module path] [--dir dir] [--dry-run]
weld add <capability> [capability...] [--dir dir] [--dry-run]
weld add module <name> [--command] [--dir dir] [--dry-run]
weld add command <name> [--dir dir] [--dry-run]
weld add enum <name> <value> [value...] [--dir dir] [--dry-run]
weld db migrate up|status|down [--config path] [--yes] [--allow-missing] [--dir dir] [--dry-run]
weld db create <name> [--dir dir] [--dry-run]
weld doctor [--json] [--dir dir]
weld upgrade [capability...] [--check] [--prune] [--verify vet|build|none] [--dir dir] [--dry-run]
weld gen [enum|loom|sqlc] [--dir dir] [--dry-run]
weld dev [--dir dir]
weld build [--docker] [--image tag] [--dockerfile path] [--platform os/arch] [--dir dir]
weld docker init [--dir dir] [--dry-run]
weld seed [name] [--tag tag] [--dir dir]
weld test [--db auto|off|require] [--db-url dsn] [--config path] [--] [go test args]
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

### Docker images for generated projects

Initialize explicitly **after installing the desired capabilities**:

```sh
weld docker init                  # creates Dockerfile and .dockerignore
weld docker init --dry-run        # preview; no Docker daemon needed
weld build --docker --image my-app:latest
weld build --docker --platform linux/arm64 --image my-app:arm64
```

Existing Docker files are always kept and are user-owned (not tracked in
`weld.json`, not upgraded). The generated Dockerfile records the capabilities it
was built for (`web`/`http`); `weld build --docker` warns when the project's
capabilities no longer match, but it never rewrites the file. After adding `web`,
edit your Dockerfile or explicitly remove it and re-run init. This does not modify
`weld.yml` or SSH deployment. The ordinary `weld build` remains unchanged; Docker
mode builds inside Docker, loads one local image and never pushes it. Docker with
BuildKit/buildx is required. Cross-platform builds cross-compile on the build
platform; only running the resulting image on a different architecture may need
emulation on the Docker host.

```yaml
build:
  docker:
    image: my-app:latest
    dockerfile: Dockerfile
    platform: linux/amd64
```

Flags (`--image`, `--dockerfile`, `--platform`) override these settings; defaults
are `<project-name>:latest`, `Dockerfile`, and `linux/amd64`, independent of
`build.targets`. `--dry-run` prints the Docker arguments without invoking Docker.
Build tags and expanded ldflags are passed as build arguments; **do not put
secrets in them**. CGO defaults to off; `build.cgo: true` needs a custom Dockerfile
with a compatible runtime, not the generated static runtime.

The multi-stage Dockerfile uses the project's Go version, optionally builds the
frontend (Node 22; pnpm 12/npm/yarn), embeds `internal/web/dist`, and runs in a
non-root distroless image with CA certificates. Custom frontend layouts/output
paths or package-manager versions may require Dockerfile edits. Dependency install
honors available lockfiles where supported; review/pin dependencies, the
Node/Go/pnpm versions and image digests for reproducible production releases.

Local `config.yml`, `.env*`, Git metadata and private-key files are excluded from
the context; review `.dockerignore` for any additional project-specific secrets.
Applications with configuration need a readable config file even when environment
variables override its values:

```sh
# CLI-only application:
docker run --rm my-app:latest help
# HTTP/web application (config.yml must be readable by uid 65532):
docker run --rm -p 8080:8080 \
  --mount type=bind,src="$(pwd)/config.yml",dst=/app/config.yml,readonly \
  my-app:latest
```

The mounted config stays outside the image; provision production credentials
separately rather than baking them into the build. The default HTTP address is
`:8080` (ensure the configured address is not restricted to localhost).

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
  `logging.Logger` contract (`internal/logging`) with a default slog implementation whose sink is an injected
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
  policies in `internal/di/http_policies.go`, with service injection; route names
  live in `internal/httpserver/policy.go`, while mount prefixes live in
  `internal/httpserver/mounts.go` and error mapping in `internal/httpserver/errors.go`. The router records every route it
  registers (`Routes`, `LogRoutes`, `PrintRoutes`), and the composition logs the
  table with each route's policies at startup in a development environment. Its
  managed `Server` takes the injected `logging.Logger`, and `serve` reads the listen address through the shared
  `config` loader. CORS is off until `config.yml`'s `cors` section enables it
  (see [CORS](#cors)); no global authentication or rate limiting is enabled by
  default. The named administrator policy denies all requests until real
  authentication and authorization are wired. Generated projects include
  `internal/httpserver/README.md` with composition, binding and testing examples.
- `web` (add) — React + TypeScript + Vite frontend served by `http`, with the
  build's precompressed `.br` and `.gz` sidecars embedded and negotiated per
  request, and fingerprinted assets cached immutably while the shell is
  revalidated. Requires pnpm and Node.js 20.19+ or 22.12+ (Vite 8).
- `db` (add) — PostgreSQL persistence: SQL migrations and queries, sqlc-generated
  code, an injectable connection pool and a repository, and optional startup
  migration from the migrations embedded in the binary. Requires `loom` but not
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
  `internal/di/events_listeners.go` (automatically wired before HTTP startup).
  It is not a queue (see
  [`weld add events`](#weld-add-events)).
- `mail` (add) — an opt-in SMTP sender with typed, secret-redacted
  configuration and an explicit TLS policy. Requires `loom` but not `http`;
  installing it never sends and never connects.
- `storage` (add) — an opt-in S3-compatible object-storage client. Requires
  `loom` but not `http`; installing it creates no bucket and uploads nothing.
- `jwt` (add) — opt-in JWT bearer-token authentication: HS256 issue/verify with a
  required issuer, an optional audience, an expiry, clock leeway and role claims,
  plus `net/http` middleware that stores validated claims on the request context.
  It requires `http` (and therefore `loom` and `config`). Installing it constructs
  no manager, issues no token and guards no route; inject the manager into
  `NewHTTPPolicies` in `internal/di/http_policies.go` to switch it on. The secret
  is required only when a manager is built, must be at least 32 bytes, and is held
  as `config.Secret`. It is stateless: no refresh token or revocation list.
- `user` (add) — opt-in user accounts: the `users` migration and sqlc query,
  a repository at `internal/data/user_repo.go` over the existing generated
  `internal/data/sqlc` package, and bcrypt registration/login with JWT session
  issuance in `internal/auth/user.go`. It requires `db` and `jwt`. Installing it
  creates no account, administrator or route; new accounts have no roles, and an
  administrator grants them with `UserService.SetRoles`. It writes no generated
  code: run `make sqlc` after install (the command is printed by `weld add user`)
  to generate `users.sql.go` and the `User` model. The account service is a
  pruned Loom binding in `internal/di/user_provider.go` until a handler or policy
  consumes it.
- `cron` (add) — an opt-in in-process scheduler that starts and stops with the
  `serve` command and never with a short command. Requires `http` (and therefore
  `loom`); installing it schedules nothing until you register jobs in
  `internal/di/cron_jobs.go`. Inject a service into `NewCronJobs` and register
  its ordinary `func(context.Context) error` method. Five-field specs and
  readable helpers (`Every`, `DailyAt`, `WeeklyOn`, `MonthlyOn`) are supported;
  job errors are logged automatically. No dependency bundle is needed.
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
`--with-dependencies` installs the whole chain in the same plan. Several
capabilities can be named in one command (`weld add loom http redis`); they
share one plan, so a requirement they have in common is installed once, and
naming more than one implies `--with-dependencies`. `loom` requires
`config`, and every capability requires `loom`, so the opt-in installs Loom and
the shared loader together and the user never adds `config` by hand. On a fresh project it generates the local `config.yml`
(git-ignored) from the committed `config.example.yml` and appends only the
section the capability needs. Adding another capability later merges its section
into the same file without clobbering the user's edits. On a fresh clone the
git-ignored `config.yml` is absent while the manifest still records it, so the
next add restores it from the committed example before appending; a missing or
corrupt example fails with the exact restore command instead of a silent
default.

`http` requires `loom`; `web`, `jwt` and a business module each require `http`,
and `user` requires both `db` and `jwt`, so `weld add web`, `weld add jwt`,
`weld add user` and `weld add module <name>` can be run in any
order and are served by the one `serve` command. `db`, `redis`, `mail` and
`storage` require `loom` but not `http`: they add pruned bindings, so they can be
added to a CLI-only project and compose with the HTTP capabilities in any order.
`seed` requires `db`: it adds a short `seed` command over the repositories you
wire, and seeds nothing on its own. `loom` is the foundation, not
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
   installed business modules on one mux. `internal/httpserver/handler.go`
   exposes `NewHandler(cfg, logger, policies, routes ...Route)` as the shared
   production/test entry. Each `Route` receives an `*httpx.Router`, just like a
   business module. The builder defines policies, installs project error mapping,
   logs development routes and applies the project middleware stack from
   `internal/httpserver/middleware.go` (including configured CORS). Composition is explicit and in one place: no global
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
the frontend never masquerades as an API that is not installed. The frontend
build's Vite compression plugin (`vite-plugin-compression2`) writes a
precompressed `.br` and `.gz` sidecar next to every compressible asset and
embeds them, so the handler serves the best sidecar the client accepts
(`Content-Encoding: br`, else `gzip`) when the client accepts one (honouring
`Accept-Encoding` q-values) instead of compressing on every request. A request
for a `.br` or `.gz` path itself returns 404: a sidecar is an encoding of the
plain path, not a second asset. Responses also carry a caching policy:
fingerprinted files under `assets/` are served `Cache-Control: public,
max-age=31536000, immutable`, while the `index.html` shell (and anything else
un-fingerprinted) is served `no-cache` so a deploy is picked up on the next
navigation.

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

## `weld doctor`

`weld doctor` runs read-only checks on a project and reports them as `ok`,
`warn` or `fail`:

```sh
weld doctor
weld doctor --json   # machine-readable report for CI
```

It checks the manifest, `weld.yml`, `config.yml` (presence and YAML validity),
managed-file drift, `go.mod` and its module path, the Go toolchain, the node/pnpm
toolchain when `web` is installed, docker when a Dockerfile or `build.docker` is
configured, ssh/scp for a deploy section, installed capabilities against this
weld's catalog, the generated Loom graph, generated enum files and the `db`
migration layout. It never writes, builds, connects to a database or prints a
credential.

A warning is advisory: a user-edited managed file, a missing optional tool or an
available upgrade. A failure makes the command exit non-zero, so `weld doctor`
can gate CI. It is deliberately weaker than `weld upgrade --check`: doctor
describes the project's local health, while upgrade-check decides what upgrade
would write.

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
| `seam` | `internal/httpserver/middleware.go`, `internal/httpserver/mounts.go`, `internal/httpserver/policy.go`, `internal/httpserver/errors.go`, `internal/di/providers.go`, `internal/di/http_policies.go`, `internal/di/events_listeners.go`, `internal/di/cron_jobs.go`, capability provider seams, `internal/data/seeder/register.go`, the `Makefile`, `.gitignore`, `config.yml` | written once, then yours: **never rewritten**, reported with a diff when the shipped version moves |
| `generated` | `internal/di/di.go`, `internal/di/di_test.go` | re-rendered while unmodified; edited sources block the plan with a diff; never merged or pruned |

**A versioned migration** is a payload file whose project path weld chose at
install time: the manifest records both the written path
(`db/migrations/<version>_create_users.sql`) and the payload source that produced
it. An upgrade therefore rewrites that file and never renames it — a migration may
already be applied — and `--prune` still recognises it as shipped.

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
`internal/di/loom_gen.go`) is reconciled rather than merged: unmodified graph
sources are re-rendered from the installed capability set and the pinned
generator re-runs, with the same snapshot-and-rollback the check uses. Hand-edited
`di.go` or `di_test.go` sources block replacement with a conflict and diff, so
business wiring cannot silently disappear while compilation still succeeds.

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

- `db/migrations/<version>_create_items.sql`, one goose migration (`-- +goose
  Up` / `-- +goose Down`), plus `db/query/items.sql` and `sqlc.yaml`. The version
  is chosen when the capability is installed, so it sorts after whatever the
  project already has;
- `db/tools/`, a nested Go module that pins the generator and the goose
  migration tool with `tool` directives
  (`tool github.com/sqlc-dev/sqlc/cmd/sqlc` and
  `tool github.com/pressly/goose/v3/cmd/goose`), so neither tool's large
  dependency graph ever enters the app `go.mod` or `go test ./...`;
- `db/embed.go`, which embeds `db/migrations/*.sql` into the binary, and
  `internal/migrate`, which applies them through the goose library. The goose
  *library* is an application dependency; the goose and sqlc *commands* remain in
  `db/tools`. `internal/di/migrate_provider.go` makes the runner a no-op unless
  the setting below is on;
- `internal/data/sqlc/`, the generated code, shipped so the project compiles and
  tests immediately with no tool installed;
- `internal/data/data.go`, the `Repository`/`Item` wrapper, `NewPool` and
  `WithTx`, plus a unit test and a gated integration test;
- `internal/testdb/`, the test helper the integration test asks for a pool
  (`testdb.Pool(t)`). It resolves the dedicated test database from
  `config.yml`'s `database_test` section or `PG_TEST_DSN`, isolates each test in
  a throwaway schema, applies `db/migrations` there and drops only that schema;
- the `database_test` section in `config.yml` / `config.example.yml`. The db
  capability reads it from `internal/testdb`; the application config loader
  ignores the key, so `weld upgrade db` does not depend on a newer config
  payload;
- `internal/data/README.md`, and the `go.mod` dependency and `Makefile` `db`
  regions patched.

The generator is reproducible: `weld gen sqlc` (the same command as `make sqlc`,
or `cd db/tools && go tool sqlc generate -f ../../sqlc.yaml`) regenerates
`internal/data/sqlc`, and a regeneration must reproduce the committed files byte
for byte. `weld gen sqlc --check` runs the diff form for CI. Migrations run
through the pinned goose tool: `make migrate-up` applies pending changes
idempotently, `make migrate-status` reports versions, and `make migrate-down`
reverts exactly one change only with `CONFIRM=1`. Every migration target
requires `DATABASE_URL` explicitly, so none can silently fall back to a local
database. `pgx/v5` is pinned in the
`weld:deps` region at the newest release that keeps the project's Go toolchain
floor; no local path or credential is committed.

### Startup migrations

Set `database.auto_migrate` (or `DB_AUTO_MIGRATE`) to make `serve` apply every
pending embedded migration before it binds the socket:

```yaml
database:
  # ...
  auto_migrate: true
```

A failed migration fails graph construction, so the process never serves against
a half-migrated schema. The runner applies only pending `Up` migrations, uses the
same `goose_db_version` table as `make migrate-up` and `weld db migrate up`, and
takes a PostgreSQL session-level advisory lock so concurrent replicas serialize.

It is off by default: starting a binary with the wrong dsn must not silently
change that database's schema. The setting is read by the server graph only; a
short command such as `seed` does not migrate. `make migrate-up` remains the
explicit deploy path, and `migrate-down` still requires `CONFIRM=1`. `weld
upgrade` moves an existing `config.yml` only as an advisory because the file is
project-owned, so add the `auto_migrate` line yourself after upgrading a project
created before this setting existed.

`db` is independent of `http` and `web`. The HTTP DTOs are never derived from
sqlc rows, and `internal/data` never
imports `net/http`, so the capability can be added to a
CLI-only project and composes with the HTTP capabilities in any order. Loom
declares the pool and repository as bindings but prunes them until a provider
asks for the repository, so an unrelated serve needs no database credential.

### Database integration tests

The SQL is exercised by a focused integration test against real PostgreSQL. It
is off unless a **dedicated test database** is configured, so `go test ./...`
stays green without one. Add to the local, git-ignored `config.yml`:

```yaml
database:
  host: localhost
  port: 5432
  user: app
  password: secret
  name: app
database_test:
  name: app_test
```

`database_test` inherits host, port, user and password from `database` and only
changes the database name; every field can be overridden. An existing project
upgraded from an older db payload adds the `database_test` block to its
project-owned `config.yml` by hand: `weld upgrade` never rewrites that file.
Then run:

```sh
go test ./...                  # integration tests run when database_test is set
weld test                     # same, with PG_TEST_DSN injected for the child
weld test --db=require        # fail if no test database is configured
weld test ./internal/data/... -- -run TestPostgresRepositoryIntegration -v
make test-integration         # the no-weld fallback, with WELD_REQUIRE_DB_TESTS=1
```

`weld test` never starts a database. It resolves `database_test` (or a
`PG_TEST_DSN` / `TEST_DATABASE_URL` override) and runs `go test`; a configured
test database is passed to the child through `PG_TEST_DSN`, never argv, and the
dsn is never printed. CI, where `config.yml` is git-ignored, sets the full dsn
instead:

```sh
PG_TEST_DSN='postgres://user:pw@localhost:5432/app_test?sslmode=disable' \
  WELD_REQUIRE_DB_TESTS=1 \
  go test ./...
```

The generated `internal/testdb` helper never falls back to the application
database: it does not use `database.dsn` or `DATABASE_URL` to name the test
database, it refuses a test target that matches the application target, and it
creates and drops only a unique throwaway schema. The explicit
`database_test.schema_only: true` mode reuses the application database in that
schema and is refused outside a local, development or test environment. With no
test database configured the integration test skips visibly; setting
`WELD_REQUIRE_DB_TESTS=1` turns that skip into a failure so CI cannot pass
without exercising the SQL.

### `weld db`

The `db` capability also ships a `weld db` command namespace so migrations do
not require exporting `DATABASE_URL` or changing into `db/tools` by hand:

```sh
weld db migrate up        # apply every pending migration
weld db migrate up --allow-missing  # also apply one older than the current version
weld db migrate status    # show which migrations are applied
weld db migrate down      # revert exactly one migration (requires --yes or CONFIRM=1)
weld db create add_users  # create db/migrations/<version>_add_users.sql
```

`weld db migrate` runs the goose tool pinned in `db/tools`, from that module,
against the database described by the application's `config.yml`: an explicit
`database.dsn`, or the host/port/user/password/name fields with the same `DB_*`
environment overrides as the generated loader. `DATABASE_URL` takes precedence
when non-empty. The resolved dsn is passed to goose but never printed; `--dry-run`
shows `$DATABASE_URL` in the command instead. `--config` selects a different
application configuration file. As with the Makefile target, the dsn is an
argument to the goose process (visible to that process's own OS account); weld
itself never writes it to output or a log.

`up --allow-missing` passes goose's out-of-order opt-in. weld does not need it for
its own migrations: `weld db create` and an installed capability both take their
version as the later of the UTC timestamp and one past the highest version in
`db/migrations`, so a new file never sorts before one that is already there. Keep
the flag for a project weld scaffolded before that rule, whose payload migration
still carries a fixed number such as `user`'s `000002_create_users.sql`. `down`
reverts one migration and refuses to run without `--yes` (or `CONFIRM=1`,
matching the Makefile). `create` validates a lower_snake_case name, refuses a
duplicate name, and writes a goose `Up`/`Down` skeleton. The Makefile targets
remain available as the no-weld fallback.

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
- `logging.Logger` — provided by the graph (`NewLogger`) from the base
  `logging` factory and injected into the managed server, so startup, a serve
  failure and a graceful shutdown are recorded through one logging protocol
  with no process default logger. Business code uses the `logging.Logger`
  interface, not backend types.
- `*pgxpool.Pool` and `data.Repository` (`db`) — the pool parses the dsn lazily
  and is **not** connected at build, test or startup, so no database is needed;
  the constructor returns a cleanup that Loom runs exactly once, whether
  construction later fails or the lifecycle stops normally.
- `*httpserver.Server` — the framework-neutral HTTP runtime. DI registers its
  `Start(ctx)` and `Stop(ctx)` as Loom hooks. Startup binds synchronously; stop
  honors the application shutdown context and forcibly closes active connections
  if draining times out. `Addr()` exposes the bound address and `Fail()` reports
  the serve result. The serve command waits for a signal or a runtime failure.
- `*cron.Scheduler` (`cron`) — the graph root consumes the scheduler, so it is
  always constructed and its `loom.Hook` start/stop run with the server: the
  socket is bound before the scheduler starts and the scheduler stops before the
  server. `NewCronJobs` in `internal/di/cron_jobs.go` injects business services
  and registers their methods before startup. It starts no jobs unless that
  seam registers them, and it takes no database or Redis lock.
- `*redis.Client` (`redis`), `*mailsender.Sender` (`mail`),
  `*objectstore.Store` (`storage`), `*jwtauth.Manager` (`jwt`) and
  `*auth.UserService` (`user`) — declared bindings the default graph never
  depends on, so Loom prunes them and an unrelated serve needs none of those
  settings. Each provider lives in a stable seam (`redis_provider.go`,
  `mail_provider.go`, `storage_provider.go`, `jwt_provider.go`,
  `user_provider.go`).
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
`mail_provider.go`, `storage_provider.go`, `jwt_provider.go`,
`user_provider.go`, `cron_provider.go`, `cron_jobs.go`, `http_policies.go` and
`providers.go`) and a
`go.mod` go-directive extension point, while `di.go`
and `loom_gen.go` alone are regenerated.

Because the generated initializer imports `github.com/Xwudao/loom`, installing
`loom` raises the project's Go directive to `go 1.25.0`. The generator's
`x/tools` dependency stays in `tools/loom`.

## `weld add events` (in-process, synchronous by default)

`weld add events` adds an in-process event dispatcher: `events.On[E](d, listener)`
registers a listener keyed on the event's concrete Go type, and `Publish` runs
every listener registered for that type, synchronously, in registration order.
It requires `loom` but not `http`, so a CLI-only project can dispatch events.

What lands in the project:

- `internal/events/dispatcher.go` — `Publisher` (the interface application code
  depends on), `Listener[T]`, `On[T]`, `Publish` and the concrete `*Dispatcher`.
  `Publish` joins the listeners' errors with `errors.Join`, recovers a panicking
  listener into a logged error, and never logs the event value.
- `internal/events/async.go` — `Async`, an opt-in `Publisher` that enqueues
  events onto a bounded in-process channel and delivers them through the same
  dispatcher from worker goroutines. `Publish` blocks and applies backpressure;
  `TryPublish` returns `events.ErrQueueFull`; `Stop(ctx)` drains within a
  deadline; `Stats()` reports occupancy and counters. A caller that depends only
  on `events.Publisher` reaches those with `events.TryPublish(ctx, publisher,
  event)` and `events.StatsOf(publisher)`, so the mode stays invisible. It is
  **best effort**: a process that exits drops whatever it has not delivered.
- `internal/events/recorder.go` — `Recorder`, a `Publisher` that records instead
  of delivering, so a test asserts what a handler published with no listener and
  no graph.
- `internal/di/events_listeners.go` — the stable business listener seam. Add
  service parameters and `events.On` calls, then run `make generate`; the HTTP
  graph automatically runs it before startup. Consumer services can also inject
  Publisher without a DI construction cycle.
- `internal/di/events_bindings.go` and `internal/events/registration.go` — expose
  the publisher's same dispatcher for registration in both delivery modes.
- `internal/di/events_provider.go` — the stable Loom provider seam, written when
  `loom` is installed. It returns the `events.Publisher` interface and selects the
  delivery mode from the `asyncEvents` switch at the top of the file (false by
  default): synchronous or `events.Async`. Because the seam owns the choice, the
  graph regenerates without knowing which was picked.

Delivery is **synchronous** by default: `Publish` returns only after every
listener returned, in the caller's goroutine. That is what makes a listener's
failure visible to the caller and the behavior assertable in a test without a
worker, a poll or a wait. It is also a real constraint, and the generated
`internal/events/README.md` says so: a slow listener slows the caller, an event
published inside a database transaction is delivered before the commit, and a
listener that panics is contained but the event is not retried. Setting
`asyncEvents = true` in the seam instead decouples a slow listener behind the
bounded channel, but it is still in-process and best effort, so it is **not a
durable queue**. Moving slow or unreliable work behind a durable queue is a queue
capability's job; because callers depend on `events.Publisher` rather than the
concrete type, that change stays in the wiring.

Installing it registers no business listener and publishes nothing. To consume
an event in B, add B's service to the wiring function and bind its method:

```go
// internal/di/events_listeners.go
func NewEventListeners(d *events.Dispatcher, b bmodule.Service) (*EventListeners, error) {
    events.On(d, b.HandleCreated)
    return &EventListeners{}, nil
}
```

Then run `make generate`. B's provider must be declared (HTTP module providers
are declared by `weld add module`). B's handler remains a normal method that
other code can call directly. The HTTP graph constructs the publisher and
registers listeners before startup; a registration error fails startup, not the
first request. Command/seed graphs still prune unused publishers and do not
implicitly consume server listeners. Business listener registration has one
entry point: `internal/di/events_listeners.go`.

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
  and the stable command graph is not rewritten. Loom prunes providers outside
  the group's `Deps` dependency closure. Every real subcommand constructs the
  entire group's `Deps`, even if it uses only one field; split dependency roots
  and graphs when subcommands need independent resource sets. The generated
  command package does not import the Loom
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
the enum generator when `internal/enums` exists, `weld gen loom` when Loom is
installed, and `weld gen sqlc` when the db capability is installed. `weld gen
loom` rebuilds `internal/di/loom_gen.go` with the generator pinned in
`tools/loom` — the same generator `make generate` runs, invoked without the
make fallback. The `make generate` target is kept as the no-weld fallback for
the Loom graph.

The sqlc generator is the same one `make sqlc` runs, pinned in the nested
`db/tools` module and driven from the project root so `sqlc.yaml` resolves its
paths correctly:

```bash
weld gen sqlc          # rewrite internal/data/sqlc from db/migrations + db/query
weld gen sqlc --check  # CI: fail when the committed generated files are stale
```

`--check` runs `sqlc diff`, prints the unified diff and fails without writing;
run the command without `--check` to refresh the files. Both forms are also
available through `make sqlc`.

A `*_gen.go` file is only overwritten when it carries the generated header, so a
user file that happens to share the name is never clobbered. Use a pointer
(`*Status`) for an optional field: the zero value `""` is not a valid value and
`ParseStatus` fails closed on anything undeclared.

## Typed HTTP clients between projects (preview)

Export the producer's typed module routes without starting its services, then
generate a standard-library-only client in the consumer:

```sh
weld http export --dir ./producer
weld client add producer --dir ./consumer --from ../producer/weld-api.json
```

The output includes `consumer/internal/clients/producer/{client_gen,types_gen,errors_gen}.go`
(and enum helper files when needed):

```go
client, err := producer.New("http://producer:8080",
	producer.WithHTTPClient(httpClient),
	producer.WithBearerToken(token),
)
if err != nil {
	return err
}
response, err := client.WidgetGreet(ctx, producer.WidgetRequest{Name: "Ada"})
```

The client encodes path/query/JSON, unwraps `{code,msg,data}`, preserves context
cancellation and returns `*producer.APIError` for a non-success response.
The producer's DTO shapes are copied; its services, internal imports and
validation code are not. Policy names are informational, not credential rules.
The default administrator guard still rejects all requests until wired.

Give routes stable unique IDs with `httpx.Operation("widget_greet")`.
Existing named handlers can export without that option (using a module/handler
name fallback); anonymous handlers require an explicit ID.
Export uses AST/Go type analysis and executes neither constructors nor init
functions. Source must type-check. V1 supports direct, straight-line typed
registrations, static Group/Require and simple ModulePrefix expressions.
Select one module with `--module widget`; `--prefix /external/widget` supplies
an explicit mount when its prefix is dynamic.

DTOs support scalars/aliases, structs, pointers, slices, fixed arrays,
json.RawMessage, time.Time, and maps keyed by strings, integers, weld enums or
time.Time. Exported anonymous fields preserve Go JSON promotion, shadowing and
ambiguity rules; path/query binding still uses only explicit top-level tags.
Unchanged weld enums copy constants, parsing and strict text/JSON decoding.
Ignored JSON fields without path/query bindings are not copied. Fixed arrays
are limited to 1,048,576 elements. Raw routes are listed as skipped. Dynamic
registration helpers, wildcard paths, unexported or special-codec embeddings,
interface/generic DTOs and arbitrary custom serialization are rejected.
New exports use contract version 2; the generator still accepts version 1 snapshots. Export is a transport snapshot, not validation, OpenAPI, service
discovery or compatibility analysis.

Errors are structured: `RequestError` for local binding/encoding/construction,
`TransportError` for network/read failures, `ProtocolError` for invalid success
responses, and `APIError` for non-success HTTP statuses. Each records the
operation/method/route template; response errors also record status and request
ID. `errors.Is`/`errors.As` preserve underlying causes, including cancellation,
deadlines and JSON decoder errors. `APIError` exposes JSON Details/Data and
response Headers, while its Error string never dumps raw response bodies or
request URLs. Known HTTP failure statuses survive body read/size/parse errors.

Server handlers can explicitly send safe structured details with
`httpx.BadRequest("invalid item").WithDetails(map[string]string{"field": "name"})`.
Unknown failures stay redacted. Refresh existing producers with
`weld upgrade http` to obtain this optional server API.

After an API change, re-export with `http export --force`, then repeat
`client add`. Generated client files are tool-owned and rewritten; keep custom
wrappers in separate files. Hand-owned collisions require `--force`.
Both commands support `--dry-run`. Paths are relative to `--dir`, with an
absolute `--from` also allowed.

See [the runnable client example](examples/http-client/README.md) for actual
generated files and a two-project walkthrough.

## Configuration

`config` is a first-class, shared capability, not a base dependency: the base
scaffold stays a minimal CLI with `logging` and no `config.yml`. `loom` requires
`config`, and every added capability requires `loom`, so any `weld add` installs
it and the user never adds it by hand.

What lands in the project:

- `internal/config/` — typed Go structs (`Config`, `HTTP`, `CORS`, `Database`,
  and the capability-added `JWT`, `Redis`, `Mail`, `Storage` and `Cron` sections)
  with `yaml` tags for names only (no validation tags), a loader whose `readFile`
  and `lookupEnv` are injected (`NewLoader`) so it is testable without the
  filesystem or the process environment, and a `Secret` type that redacts itself
  in `fmt`, `logging`, JSON and YAML.
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

## JWT bearer tokens

`weld add jwt --with-dependencies` installs `http` (and its `loom`/`config`
chain), then writes `internal/jwtauth` and a `jwt` section in `config.yml`:

```yaml
jwt:
  secret: ""              # required when a manager is built; >= 32 bytes
  issuer: "my-app"        # required; written into and verified from every token
  audience: ""            # optional; empty disables the audience check
  ttl_seconds: 900
  leeway_seconds: 30
```

`JWT_SECRET`, `JWT_ISSUER`, `JWT_AUDIENCE`, `JWT_TTL_SECONDS` and
`JWT_LEEWAY_SECONDS` override the file, and `config.Secret` keeps the secret out
of logs, formatted output, JSON and YAML. The manager accepts **HS256 only**,
requires a valid signature, issuer and expiry, optionally requires an audience,
and records its failure as one generic `ErrInvalidToken` so an attacker cannot
probe the verification rules. A secret shorter than 32 bytes is rejected.

Installing `jwt` does not issue a token or guard a route. The manager is a pruned
Loom binding in `internal/di/jwt_provider.go`. Wire it into the project's policy
seam:

```go
func NewHTTPPolicies(auth *jwtauth.Manager) ([]httpserver.NamedPolicy, error) {
	return []httpserver.NamedPolicy{
		{Name: httpserver.PolicyUser, Middleware: auth.Middleware},
		{Name: httpserver.PolicyAdmin, Middleware: httpx.Chain(
			auth.Middleware,
			jwtauth.RequireRole("admin"),
		)},
	}, nil
}
```

Then run `make generate`. `Manager.Middleware` reads only
`Authorization: Bearer <token>`, stores validated `*jwtauth.Claims` on the
request context, and returns a `401` in the shared envelope for a missing or
invalid token. `jwtauth.ClaimsFrom(ctx)` exposes the subject and roles;
`RequireRole`/`RequireAnyRole` enforce roles with a `403`.
`httpserver.PolicyUser` is a new policy name, still resolved by
`NewHTTPPolicies`; the generated `AdminGuard` stays deny-all until replaced.

The package provides `Issue(subject, roles)` for the endpoint that has already
verified credentials. It does not authenticate a password, look up a user or
assign roles. Tokens are stateless: there is no refresh token or revocation
list, so a token remains valid until it expires; keep the TTL short, rotate the
secret by restarting every instance together, and serve the application over
HTTPS. Never put passwords, API keys or private data in claims: a JWT is signed,
not encrypted. See `internal/jwtauth/README.md` in the generated project.

## User accounts

`weld add user --with-dependencies` installs `db` and `jwt` (and their chains),
then adds the account table and the code around it. It follows the project's
data/auth split rather than adding a top-level `internal/user` package:

```
db/migrations/<version>_create_users.sql   goose schema
db/query/users.sql                      named sqlc statements
sqlc.yaml                               the existing single config
internal/data/sqlc/users.sql.go         generated by `make sqlc` (not shipped)
internal/data/user_repo.go              UserRepository over the generated queries
internal/auth/user.go                   bcrypt registration/login + JWT issuance
internal/di/user_provider.go            stable Loom seam
```

The table stores `id`, `username`, `password_hash`, `roles`, `created_at` and
`updated_at`. `id` is a PostgreSQL `bigint GENERATED ALWAYS AS IDENTITY`, so it is
a compact increasing number assigned by the database rather than a random
string. It is not a secret: sequential IDs reveal creation order, so every
read/write must still check ownership; add a separate opaque `public_id` if an
API needs one. It never stores a plaintext password. The username is normalized
to lower case by the service and the migration enforces
`username = lower(username)` with a unique index, so `Alice` and `alice` are one
account. New accounts have **no roles**.

The service is the reusable account layer:

```go
accounts, err := auth.NewUserService(data.NewUserRepository(pool), tokens)
if err != nil {
	return err
}
account, err := accounts.Register(ctx, "alice", "correct horse battery staple")
session, err := accounts.Login(ctx, "alice", "correct horse battery staple")
// session.Account has no hash; session.Token is the JWT.
```

`Login` authenticates with bcrypt and then issues the token, so the subject is
the decimal account ID and the claims carry the account's current roles. An unknown
username and a wrong password both return `auth.ErrInvalidCredentials`, and the
unknown path still runs a dummy bcrypt comparison to reduce account-enumeration
timing. `Register` grants no roles; an administrator uses
`accounts.SetRoles(ctx, id, []string{"admin"})` from a controlled command, a
seeder or an existing administrator, never from an unauthenticated registration
endpoint.

Installing `user` creates no account and mounts no route. The Loom graph declares
`NewUserService(pool, jwtManager)` but prunes it until a handler or policy
consumes it, so an ordinary serve needs no database or JWT secret. Inject
`*auth.UserService` into a module's `NewService` or into `NewHTTPPolicies`; the
application still owns the login/registration handlers, their DTOs, rate
limiting and HTTPS. See `internal/auth/README.md` in the generated project.

The users migration is versioned when it is installed: `weld add user` writes
`<version>_create_users.sql` with a version after every migration the project
already has, so applying it needs no `--allow-missing`. A project scaffolded by an
older weld keeps `000002_create_users.sql`, and a database that already has later
versions needs goose's out-of-order opt-in for it:

```sh
weld db migrate up --allow-missing
```

Installing `user` does not ship generated code. Run the project's existing
generator once before building:

```sh
make sqlc
```

It reads the same single `sqlc.yaml`, all migrations and all query files, and
writes `internal/data/sqlc/users.sql.go` and the `User` model alongside the
existing items output.

## Hooks

`weld.yml` is user-owned, and hooks are the user-owned extension point for the
commands that build, run, seed and deploy the project. A hook is a command you
declare, run at a named point in a weld command, so a project can add code
generation, asset steps or notifications without weld learning about them:

```yaml
hooks:
  enabled: true
  build:
    pre:
      - name: tidy
        run: [go, mod, tidy]
    post:
      - run: "gofmt -w ."
        platforms: [linux, darwin]
  deploy:
    pre:
      - name: build assets
        run: [pnpm, run, build]
        dir: web
        when: { capability: [web] }
    post:
      - run: "./scripts/notify.sh"
        continue_on_error: true
        env: { SLACK_CHANNEL: "#deploys" }
```

### Events

Every hookable command exposes a `pre` and a `post` list. Steps run in order,
and `pre` runs before the command's own work, `post` after it succeeds.

| event | when |
|---|---|
| `build.pre` / `build.post` | around `weld build` (and `weld build --docker`) |
| `dev.pre` / `dev.post` | around a `weld dev` session, `dev.post` on exit |
| `seed.pre` / `seed.post` | around `weld seed` |
| `deploy.pre` / `deploy.post` | around `weld deploy`, before the build/upload and after the remote script |

A hook is tied to the command you typed, not to the commands it calls: `weld
deploy` builds the artifact internally but does **not** run `build.pre` /
`build.post`, so a hook never runs twice. Hooks do not run for `weld new`,
`weld add`, `weld upgrade` or `weld gen`, and they are never run by `weld add` or
recorded in `weld.json`.

### Step fields

| field | meaning |
|---|---|
| `run` (required) | A scalar is a shell command line (`sh -c` on Unix, `cmd /C` on Windows), so pipes and redirection work. A sequence is an argv vector run directly, with no shell and no word splitting; it is the portable form and is preferred for simple commands. |
| `name` | A label for logs and errors. Optional; the command is used when absent. |
| `dir` | Working directory, relative to the project root. Optional. |
| `env` | Environment variables overlaid on the inherited process environment. Optional. |
| `platforms` | Restricts the step to `linux`, `darwin` (or `macos`/`mac`) and/or `windows`. Empty runs everywhere. |
| `when` | Conditions below. All must hold. |
| `timeout` | A Go duration (`30s`, `2m`). Zero is unbounded. |
| `continue_on_error` | Report a failure and keep going instead of aborting. Default is fail-fast. |

`when` accepts any of: `capability` (each named capability must be installed,
matched against the manifest), `env` (each variable must equal the given value)
and `files` (each path must exist, relative to the project root). A condition is
after the platform check, so a step can be both platform- and capability-gated.

### Execution

- **Fail-fast.** The first step that fails aborts the command with a message that
  names the event, the step and the exit code. Hooks run outside weld's
  transactional apply, so a failed hook does **not** roll back files weld already
  wrote; `continue_on_error` turns a failure into a warning that does not abort.
- **A hook inherits the environment**, with the step's `env` and a set of
  `WELD_*` variables overlaid: `WELD_COMMAND`, `WELD_EVENT`, `WELD_DIR`,
  `WELD_VERSION`, `WELD_PROJECT`, plus `WELD_BUILD_OUTPUT`, `WELD_DEPLOY_HOST` /
  `WELD_DEPLOY_TARGET` and `WELD_SEED_TAG` for the matching command.
- **dry-run is truthful.** `weld deploy --dry-run` and `weld build --docker
  --dry-run` print the hook steps they would run instead of running them. A
  command that ignores `--dry-run` (plain `weld build`, `weld dev`, `weld seed`)
  still runs its hooks, so the flag never silently skips them.
- **Opt out for a run** with `--no-hooks`, or globally with `WELD_NO_HOOKS=1`
  (for CI), or in the file with `hooks.enabled: false`.

### Trust

Declaring a hook makes `weld.yml` executable: a cloned repository can run code
when you `weld build` or `weld dev`. That is the same trust you already extend to
its `Makefile` and `package.json` scripts, and weld only reads the project's own
`weld.yml`. Each step is printed (`[hook] build.pre: ...`) before it runs, and
`--no-hooks` / `WELD_NO_HOOKS=1` is the escape hatch for an untrusted checkout.

Hooks are validated when `weld.yml` loads: an unknown command (`hooks.buld`), an
unknown step field, an empty `run`, an absolute or escaping `dir`, an unknown
platform and a negative `timeout` are all load-time errors rather than hooks that
quietly never run. `weld config example` documents the block.

## Tests

```sh
go test ./... -count=1
go vet ./...
```

Test conventions:

- No test opens a socket to a service. HTTP behavior is asserted with
  `net/http/httptest` against handlers built from injected routes/fixtures
  (`httpserver.NewHandler(cfg, logger, policies, routes...)`, and `web.Handler` over an
  `fstest.MapFS`). The generated `internal/httpx` test covers the envelope
  writers, typed binding (content type, body limit, unknown field, trailing
  JSON) and the middleware (order, request id, recovery, Flusher/Hijacker
  forwarding) without a socket; its `cors_test.go` covers the preflight answered
  before the handler, the refused origin, method and header, the safelisted
  header, the actual request's headers, the plain `OPTIONS` left to the router,
  the wildcard never paired with credentials, the case-insensitive match and the
  `Vary` and max-age output. Loopback-only server tests verify occupied-port
  startup failure, runtime failure reporting, graceful shutdown and forced
  connection closure when the caller cancels shutdown. Handler tests cover
  policies, error logging, development route logging and configured CORS.
- The generated `serve` command is exercised as a Cobra command tree
  (`internal/app/serve_test.go`): the root builds the serve command, the shared
  `--config`/`--addr` flags parse and their precedence resolves, and the
  lifecycle helper runs against a cancellable context — rather than by listening.
- The generated `internal/config` test drives the loader through an injected
  `readFile`/`lookupEnv`: it covers the flag/environment/file/default precedence,
  a missing file, an invalid file that must not echo its content, a blank
  `HTTP_ADDR` that must not fall back to the default, the separate database
  validation, and a `Secret` sentinel that must not survive `fmt`, `logging`, JSON
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
- The base CLI logs a failed command through its injected `logging` logger, and
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
  row-to-domain conversion, dsn validation and the test-database resolver. The
  integration test is off unless `config.yml`'s `database_test` section (or
  `PG_TEST_DSN`) names a dedicated, disposable test database; there is no
  fallback to the application dsn, so the suite never targets a live database by
  accident. `internal/testdb` refuses a test target that matches the application
  target, isolates itself in a unique throwaway schema, points the pool's
  `search_path` at it, applies the migrations there, and drops only that schema
  with `CASCADE`, so it never drops or mutates the database's real tables. With
  no test database configured the test skips visibly; `WELD_REQUIRE_DB_TESTS=1`
  turns the skip into a failure for CI.
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
- The jwt capability ships a descriptor test (it requires `http`, patches the
  config markers, declares its provider seam and ships no login route), a config
  test (file, environment and defaults precedence and fail-closed validation),
  and a verifier test (round-trip, wrong secret/issuer/audience, expiry,
  not-before, `alg: none` rejection, bearer extraction, 401/403 middleware and
  role checks). A scaffold test generates a project, adds jwt, proves the unused
  manager is pruned from the default Loom composition, and runs the generated
  project's gofmt/build/test.
- The user capability ships a descriptor test (it requires `db` and `jwt`, uses
  the single `sqlc.yaml`, keeps the repository in `internal/data` and the
  account service in `internal/auth`, declares its provider seam and creates no
  default account or route), plus unit tests for bcrypt registration, credential
  failures, role normalization, token subject/roles, generated-row projections
  and duplicate-user mapping. A scaffold test generates a project, adds user,
  proves the unused account service is pruned from the default Loom composition,
  runs `make sqlc`, then the generated project's gofmt/build/test, and proves a
  second `make sqlc` leaves `internal/data/sqlc` byte-identical.
- `internal/db` tests cover dsn resolution precedence (`DATABASE_URL`, an explicit
  dsn, the typed fields and `DB_*` overrides), a missing or invalid config,
  missing fields, the exact pinned goose command, the redacted display form,
  dry-run, the down confirmation and timestamped migration creation.
  `internal/doctor` and the CLI tests cover a healthy project, managed-file drift
  as a warning, a missing project as a failure, capability upgrade/unknown
  reporting, missing web/deploy tools, a missing Loom graph and a missing db tool
  layout, plus the JSON report used by CI.

## Layout

```
cmd/weld/            CLI entry point
internal/cli/        argument parsing and output
internal/db/         `weld db` migration planning, dsn resolution and migration creation
internal/doctor/     read-only project health checks
internal/template/   capability catalog, module and command templates, placeholder rendering
internal/project/    manifest, marker regions and splicing, unified diff, best-effort rollback apply
internal/scaffold/   create/add/upgrade/add-module/add-command plans and dependency resolution
internal/semver/     comparison of the dotted capability versions weld records
internal/skills/     project agent skill generation and safe write
```
