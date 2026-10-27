// Package skills renders the per-project agent skill file and plans its safe,
// idempotent write.
//
// `weld skills` explains a generated project to a coding agent: which
// capabilities are installed, what "installed" does and does not mean, and
// which files weld generates. The document is derived from weld.json and the
// capability catalog only, never from project source, so a local credential can
// never reach it.
package skills

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Xwudao/weld/internal/project"
	"github.com/Xwudao/weld/internal/template"
)

// Path is the project-relative path of the generated skill file.
const Path = ".agents/skills/weld/SKILL.md"

// The generated file ends with a trailer line recording the fingerprint of the
// body above it. The fingerprint lets a later run tell weld output from a file
// the user edited, so an edit is never overwritten silently.
const (
	trailerPrefix = "<!-- weld:skills sha256="
	trailerSuffix = " -->"
)

// Catalog supplies the catalog description of an installed capability. It is
// satisfied by *template.Catalog; tests inject a fake so a future capability
// can be exercised without shipping it in the embedded template.
type Catalog interface {
	Get(name string) (*template.Capability, error)
}

// Result is a planned skill file.
type Result struct {
	// Path is the project-relative skill path.
	Path string
	// Content is the bytes to write.
	Content []byte
	// Existing reports that the file already existed and was not user-edited.
	Existing bool
	// Changed reports that applying the plan would change the file.
	Changed bool

	// original is the file content read when the plan was built, and
	// originalExisted reports whether it existed. Apply re-reads the file and
	// refuses when either changed, so a user edit made between planning and
	// application is never overwritten silently.
	original        []byte
	originalExisted bool
}

// Generate plans the skill file for a project rooted at root.
//
// A file weld did not write, or one the user edited after it was generated, is
// a conflict rather than something to overwrite, so a user's own skill notes
// are never lost.
func Generate(root string, manifest *project.Manifest, catalog Catalog) (*Result, error) {
	content, err := Render(manifest, catalog)
	if err != nil {
		return nil, err
	}
	existing, readErr := os.ReadFile(filepath.Join(root, Path))
	switch {
	case errors.Is(readErr, os.ErrNotExist):
		return &Result{Path: Path, Content: content, Changed: true}, nil
	case readErr != nil:
		return nil, readErr
	}
	if Edited(existing) {
		return nil, &project.ConflictError{
			Path:   Path,
			Reason: "was edited since it was generated; refusing to overwrite (delete or rename it to regenerate)",
		}
	}
	return &Result{
		Path:            Path,
		Content:         content,
		Existing:        true,
		Changed:         !bytes.Equal(existing, content),
		original:        existing,
		originalExisted: true,
	}, nil
}

// Apply writes the planned file when it changed and does nothing otherwise, so
// a repeat `weld skills` leaves the file untouched.
//
// It re-reads the file first and refuses when it no longer matches the state the
// plan was built against, so a user edit made between planning and application
// is a conflict rather than a silent overwrite.
func (r *Result) Apply(root string) error {
	if !r.Changed {
		return nil
	}
	if err := r.recheck(root); err != nil {
		return err
	}
	return project.Apply(root, []project.Operation{{Path: r.Path, Content: r.Content, Overwrite: true}})
}

// recheck re-reads the on-disk file and reports a conflict when it differs from
// the state stored in the plan, so a concurrent edit is never overwritten.
func (r *Result) recheck(root string) error {
	current, err := os.ReadFile(filepath.Join(root, r.Path))
	switch {
	case errors.Is(err, os.ErrNotExist):
		if r.originalExisted {
			return &project.ConflictError{Path: r.Path, Reason: "was removed since the plan was generated; refusing to overwrite"}
		}
		return nil
	case err != nil:
		return err
	}
	if !r.originalExisted {
		return &project.ConflictError{Path: r.Path, Reason: "appeared since the plan was generated; refusing to overwrite"}
	}
	if !bytes.Equal(current, r.original) {
		return &project.ConflictError{Path: r.Path, Reason: "was edited since the plan was generated; refusing to overwrite (delete or rename it to regenerate)"}
	}
	return nil
}

// Edited reports whether content is not a weld-generated document, or is one
// that was modified after generation. An unrecognized file is treated as edited
// so it is never overwritten.
func Edited(content []byte) bool {
	body, hash, ok := splitTrailer(content)
	if !ok {
		return true
	}
	return fingerprint(body) != hash
}

// splitTrailer separates the generated body from its trailer fingerprint. It
// reports false when content does not end with a well-formed trailer.
func splitTrailer(content []byte) (body []byte, hash string, ok bool) {
	s := string(content)
	index := strings.LastIndex(s, trailerPrefix)
	if index < 0 {
		return nil, "", false
	}
	rest := s[index+len(trailerPrefix):]
	end := strings.Index(rest, trailerSuffix)
	if end < 0 || rest[end+len(trailerSuffix):] != "\n" {
		return nil, "", false
	}
	return []byte(s[:index]), rest[:end], true
}

func fingerprint(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

// stamp appends the trailer recording the fingerprint of body.
func stamp(body []byte) []byte {
	out := make([]byte, 0, len(body)+len(trailerPrefix)+64+len(trailerSuffix)+1)
	out = append(out, body...)
	out = append(out, trailerPrefix...)
	out = append(out, fingerprint(body)...)
	out = append(out, trailerSuffix...)
	out = append(out, '\n')
	return out
}

// installedCapability is one capability recorded in the manifest.
type installedCapability struct {
	name    string
	version string
}

// installedCapabilities returns the base and added capabilities in lexical
// order, so the document is independent of install order and deterministic.
func installedCapabilities(manifest *project.Manifest) []installedCapability {
	versions := map[string]string{}
	if manifest.Base.Name != "" {
		versions[manifest.Base.Name] = manifest.Base.Version
	}
	for _, capability := range manifest.Capabilities {
		versions[capability.Name] = capability.Version
	}
	caps := make([]installedCapability, 0, len(versions))
	for name, version := range versions {
		caps = append(caps, installedCapability{name: name, version: version})
	}
	sort.Slice(caps, func(i, j int) bool { return caps[i].name < caps[j].name })
	return caps
}

// Render builds the skill document for the capabilities recorded in manifest.
func Render(manifest *project.Manifest, catalog Catalog) ([]byte, error) {
	if manifest == nil {
		return nil, errors.New("skills: manifest is required")
	}
	caps := installedCapabilities(manifest)
	set := make(map[string]bool, len(caps))
	for _, capability := range caps {
		set[capability.name] = true
	}

	var b strings.Builder
	writeFrontmatter(&b)
	writeHeader(&b, manifest)
	writeScaffoldCommands(&b, set)
	writeCapabilities(&b, caps, catalog)
	writeWiring(&b, set)
	writeWiringRules(&b)
	writeModules(&b, manifest)
	writeCommands(&b, manifest)
	writeEditing(&b, set)
	writeFileIndex(&b, manifest)
	writeRegenerate(&b, set)
	return stamp([]byte(b.String())), nil
}

func writeFrontmatter(b *strings.Builder) {
	b.WriteString("---\n")
	b.WriteString("name: weld\n")
	b.WriteString("description: Use before writing or changing code in this weld-scaffolded Go project. Create modules, CRUD resources, commands, middleware, event listeners, cron jobs, seeders, enums and migrations with weld commands instead of hand-writing them, then customize the generated seams; also explains the installed capabilities, wiring seams and generated files.\n")
	b.WriteString("---\n\n")
}

func writeHeader(b *strings.Builder, manifest *project.Manifest) {
	fmt.Fprintf(b, "# weld project skill: %s\n\n", manifest.Name)
	b.WriteString("This project is a Go application scaffolded with [weld](https://github.com/Xwudao/weld).\n")
	if manifest.CreatedWith != "" {
		fmt.Fprintf(b, "It was created with weld %s and its Go module is `%s`.\n\n", manifest.CreatedWith, manifest.Module)
	} else {
		fmt.Fprintf(b, "Its Go module is `%s`.\n\n", manifest.Module)
	}
	b.WriteString("`weld new` creates a minimal Go CLI and `weld add <capability>` extends that\n")
	b.WriteString("same project one capability at a time. Capabilities are additive and never\n")
	b.WriteString("removed; a missing requirement is an error unless `weld add` is given\n")
	b.WriteString("`--with-dependencies`. `weld dev` runs the backend and (with the web\n")
	b.WriteString("capability) the frontend together, and `weld build` builds the frontend and\n")
	b.WriteString("cross-compiles the binary. Both read the user-owned `weld.yml` (not the\n")
	b.WriteString("machine-owned `weld.json` manifest), which weld never rewrites; print a\n")
	b.WriteString("starter with `weld config example`.\n\n")
}

// writeCapabilities lists every installed capability with a description. A
// capability this weld build does not curate falls back to its catalog summary,
// and one the catalog does not carry at all is described generically rather than
// invented.
func writeCapabilities(b *strings.Builder, caps []installedCapability, catalog Catalog) {
	b.WriteString("## Installed capabilities\n\n")
	for _, capability := range caps {
		version := capability.version
		if version == "" {
			version = "unknown"
		}
		fmt.Fprintf(b, "- **%s** v%s — %s\n", capability.name, version, describe(capability.name, catalog))
	}
	b.WriteString("\n")
}

func describe(name string, catalog Catalog) string {
	if note, ok := curatedNotes[name]; ok {
		return note
	}
	if catalog != nil {
		if capability, err := catalog.Get(name); err == nil && capability.Summary != "" {
			return capability.Summary
		}
	}
	return "capability recorded in `weld.json`; this weld build has no description for it."
}

// curatedNotes is the accurate, project-level description of the bundled
// capabilities. It is a map rather than a switch so a future capability falls
// through to its catalog summary instead of silently vanishing.
var curatedNotes = map[string]string{
	"base":      "minimal Go CLI on a Cobra command tree: a root-command contribution seam in `internal/app` (`RegisterCommand`, `ConfigureRoot`, `SetDefaultRun`) and a backend-neutral `logging.Logger` contract in `internal/logging` with a default slog implementation (text records colored by `tint`, JSON via slog, plus `NewFromEnv`, `ParseLevel` and context helpers). A bare invocation prints help until a capability installs a long-running root action. It ships no configuration and no server.",
	"config":    "shared typed configuration in `internal/config`: `config.yml` from the working directory with flag > environment > file > defaults precedence. `config.yml` is local and git-ignored and `config.example.yml` is committed. `config.Secret` redacts itself in `fmt`, `logging` (through its `Valuer` contract), JSON and YAML. The typed `cors` section (off by default, `CORS_*` environment overrides) configures the HTTP cross-origin policy; an enabled policy is validated fail-closed at load time.",
	"http":      "HTTP server lifecycle in `internal/httpserver` and the `serve` command in `internal/app/serve.go`. Production and tests compose routes through `httpserver.NewHandler(cfg, logger, policies, routes...)`, whose route callbacks receive `*httpx.Router`; standalone toolkits can use `httpx.NewRouter(mux)` and its typed helpers (`Get`, `Post`, `NoInput`, ...) or a raw `Router.Get`/`Router.HandleFunc`; there is no global route registry. The shared HTTP toolkit is `internal/httpx`: the `{code,msg,data}` JSON response envelope with raw writers for responses that must stay unwrapped, a router with grouping, per-group middleware and named route policies, typed handlers, query/path and JSON binding with a body limit, automatic `Validate() error` on a value or pointer-receiver DTO, explicit struct/*struct binding and rejection of present empty numeric values, `*HTTPError` status mapping, and the composable middleware (`RequestID`, `AccessLog`, `Recover`). A configuration-driven CORS policy (`httpx.CORS`) is installed outermost only when `config.yml`'s `cors` section enables it; it answers a preflight before the router, refuses a disallowed origin/method/header with a 403 envelope, echoes the origin instead of `*` when credentials are allowed, and is validated fail-closed at startup (an enabled section with no origin, a non-origin entry such as a trailing slash, and `*` with `allow_credentials` are all errors). `CORS_*` environment variables override the file. The global middleware stack lives in `internal/httpserver/middleware.go`; inject business services and wire named middleware in the stable `internal/di/http_policies.go` NewHTTPPolicies provider, then run `make generate`. Keep route names in `internal/httpserver/policy.go`, mount prefixes in `internal/httpserver/mounts.go`, and domain-error mapping in `internal/httpserver/errors.go`, avoiding service import cycles; `internal/httpserver.APIPrefix` is the mount prefix, so versioning is one line. The router records every route it registers (`Routes`, `LogRoutes`, `PrintRoutes`), and the generated composition logs the table with each route's policies at startup when `environment` is local or development, so the surface and its coverage are visible without a request. `weld routes` lists the module routes, their policies and operation IDs from source without starting the application. A route policy is only a name: the composition root resolves it into middleware with `httpx.Router.WithPolicy`, and the generated `AdminGuard` denies all requests until replaced with real authentication and administrator authorization, so the project's real check (a JWT, a session or a database role lookup) is implemented there or in a package it delegates to. For cross-project typed calls, `weld http export` statically reads direct module registrations and DTO shapes into `weld-api.json` without executing services; `weld client add <name> --from <contract>` generates standard-library-only client/DTO files under `internal/clients/<name>`. Give typed routes stable unique IDs with `httpx.Operation`; generated clients unwrap the envelope and return `*APIError` on non-success responses. Raw routes are skipped; dynamic route helpers, wildcard paths and arbitrary custom serialization are not supported in this preview. DTOs also support RawMessage, fixed arrays, exported anonymous fields and unchanged weld enums; generated errors distinguish RequestError/TransportError/ProtocolError/APIError and preserve request IDs and causes. HTTPError.WithDetails explicitly returns client-safe JSON details without exposing unknown errors. Once installed, a bare invocation runs the same serve lifecycle as `serve`, and `--config`/`--addr` are shared persistent root flags so both parse one definition.",
	"web":       "React + TypeScript + Vite frontend under `web/`, built into `internal/web` and served by **http** on `/`. The build writes precompressed `.br` and `.gz` sidecars next to compressible assets, and the handler serves the best sidecar the client accepts (`Content-Encoding: br`, else `gzip`, honouring q-values) when it accepts one (a direct `.br` or `.gz` request is 404). Fingerprinted files under `assets/` are served `Cache-Control: public, max-age=31536000, immutable`, while the `index.html` shell and other un-fingerprinted files are served `no-cache` so a deploy is picked up. `/api/` stays reserved: an unknown API path returns 404 instead of the HTML shell.",
	"db":        "PostgreSQL persistence in `internal/data`: a hand-written `Repository`/`Item` wrapper, `NewPool` and `WithTx`, over sqlc-generated `internal/data/sqlc`. Installing it connects nothing and needs no credential until you wire the repository yourself. `weld db migrate up|status|down` runs the pinned goose tool against `config.yml` or `DATABASE_URL`; `weld db create <name>` writes a versioned goose migration skeleton. `weld gen sqlc` regenerates `internal/data/sqlc` from `db/migrations` and `db/query` with the sqlc version pinned in `db/tools` (`--check` reports stale files for CI). The Makefile keeps `make sqlc` as the no-weld fallback. Integration tests go through `internal/testdb.Pool(t)`, never a hand-built dsn: the helper reads `config.yml`'s `database_test` section (or `PG_TEST_DSN`), isolates the test in a unique schema, applies `db/migrations` and drops only that schema. With no test database configured the test skips; `WELD_REQUIRE_DB_TESTS=1` turns the skip into a failure. `weld test` runs `go test` and injects the resolved test dsn, never a Docker container.",
	"jwt":       "opt-in JWT bearer-token authentication in `internal/jwtauth`: HS256 issue/verify with a required issuer, optional audience, expiry, clock leeway and role claims. `Manager.Middleware` reads only the `Authorization: Bearer` header and stores validated claims on the request context; `RequireRole`/`RequireAnyRole` check claims roles. Installing jwt constructs no manager, issues no token and guards no route; inject `*jwtauth.Manager` into `NewHTTPPolicies` in `internal/di/http_policies.go` and use `httpserver.PolicyUser` (or compose `RequireRole(\"admin\")` with `PolicyAdmin`). `jwt.secret` is required only when a manager is built and must be at least 32 bytes; it is held as `config.Secret`, is redacted everywhere, and `JWT_SECRET` overrides the file. Tokens are stateless: there is no revocation or refresh token, so rotate by restarting with a new secret and keep TTLs short.",
	"user":      "opt-in user accounts split the way the project stores data: the `users` migration and named queries live under `db/` (`users.id` is a PostgreSQL bigint identity, not a random string), generated account code lands in the existing `internal/data/sqlc` package via the project's single `sqlc.yaml` (run `make sqlc` after install), the repository is `internal/data/user_repo.go`, and bcrypt registration/login plus JWT session issuance are in `internal/auth/user.go`. It requires `db` and `jwt`, creates no default account, administrator or route, and grants new accounts no roles; an administrator calls `UserService.SetRoles`. `internal/di/user_provider.go` declares the service but Loom prunes it until a handler or policy consumes it, so an ordinary serve needs no database or JWT secret. The login error is deliberately one `auth.ErrInvalidCredentials` for an unknown user and a wrong password, and the service performs a dummy bcrypt comparison for an unknown name to reduce account enumeration timing. Run `weld db migrate up --allow-missing` when the users migration is older than the database's current version.",
	"loom":      "compile-time dependency injection in `internal/di`, required by every capability and command beyond the bare CLI (install it with `--with-dependencies`). All command graphs share available config/logger and optional infrastructure providers through `commonModule`; the HTTP server graph exists only after **http** is installed. Graphs reuse each provider once per invocation and prune unused DB/Redis/mail/storage/events/JWT bindings. A command group resolves its graph lazily through the shared `internal/commandkit` seam, so a subcommand injects a dependency by adding a field to its `Deps` and a parameter to `NewDeps` (a provider already in `commonModule` needs no graph edit; a new one needs `loom.Provide(...)` in `internal/di/<name>_graph.go`). Loom raises the project's Go directive to 1.25.",
	"redis":     "opt-in Redis client in `internal/redisclient` built from typed configuration. Installing it connects nothing: `New` never dials or pings and nothing generated imports it, so you own the client lifecycle.",
	"cron":      "opt-in in-process scheduler in `internal/cron`: five-field specs or fixed intervals (`Every`, `DailyAt`, `WeeklyOn`, `MonthlyOn`), unique names, overlap skipping, error logging, panic recovery and graceful stop. Cron installs HTTP and follows only the Loom `serve` lifecycle, never a short command. Inject business services into `NewCronJobs` in the stable `internal/di/cron_jobs.go`, register their ordinary func(context.Context) error methods with Scheduler.Register, then run `make generate`. Registration is automatically consumed before startup; no job Deps bundle is needed. `internal/di/cron_provider.go` owns construction and lifecycle, not business wiring. Installing cron schedules nothing.",
	"seed":      "opt-in database seeding in `internal/data/seeder`: named, idempotent seeders run by the short `seed` command (`make seed`, `weld seed`) over the repositories the Loom seed graph injects. The seeders live behind the `seed` build tag (`//go:build seed`), so the production build (`go build`, `weld build`) compiles in neither the seed data nor any faker a seeder imports. Installing seed seeds nothing; `weld add seeder <name>` writes one `seed_<name>.go` per seeder and registers it in the stable `internal/data/seeder/register.go` index, and `internal/di/seed_providers.go` is the stable repository seam.",
	"events":    "in-process event dispatcher in `internal/events`: `events.On[E](d, listener)` registers by concrete event type; synchronous Publish runs listeners in registration order and joins errors, recovering and logging panics without logging event values. For business listeners, add service parameters and On calls in the stable `internal/di/events_listeners.go` NewEventListeners seam, then run `make generate`. The HTTP graph automatically consumes it before startup, and consumer services may also depend on `events.Publisher` without a DI construction cycle. `internal/di/events_provider.go` selects synchronous delivery (asyncEvents=false) or bounded, best-effort `events.Async`: Publish blocks on backpressure; `events.TryPublish` returns `events.ErrQueueFull`. A listener that publishes back into Async should use TryPublish to avoid blocking its own worker. Server listener wiring constructs the publisher; command/seed graphs prune it unless consumed and do not automatically register server listeners. Installing events dispatches nothing without a publish and a listener. It is not a queue: no persistence, retry or cross-process delivery; publish after transaction commit. `events.StatsOf` reads async statistics and `events.Recorder` is the service test double.",
	"mail":      "opt-in SMTP sender in `internal/mailsender`: one connection per `Send` with an explicit TLS policy and header-injection guards. Nothing generated imports it, so serving sends no mail; `internal/di/mail_provider.go` is the stable Loom seam.",
	"ratelimit": "opt-in per-client HTTP rate limit in `internal/ratelimit`: an in-memory token bucket per client address (IPv6 grouped by /64) with a bounded client set; `X-Forwarded-For` is read only from `rate_limit.trusted_proxies`, taking the right-most untrusted hop. A refused request gets `429` with the error envelope and `Retry-After`. `httpserver.RateLimitMiddleware(cfg)` builds it from `config.yml`'s `rate_limit` section (nil when disabled, an error when invalid); add it to `middlewareChain` in `internal/httpserver/middleware.go` after the default entries. The limit is per process, not shared across instances. For another key or a per-route limit, use `ratelimit.New` and `ratelimit.Middleware` with a custom `KeyFunc`, for example as a named policy.",
	"storage":   "opt-in S3-compatible object storage in `internal/objectstore`: a streaming client with bounded presigned URLs. It never creates the bucket, and nothing generated imports it; `internal/di/storage_provider.go` is the stable Loom seam.",
}

func writeWiring(b *strings.Builder, set map[string]bool) {
	b.WriteString("## Installed is not the same as wired\n\n")
	b.WriteString("A capability is **installed** when weld wrote its files and recorded them in\n")
	b.WriteString("`weld.json`. Installed does not mean **wired**: the generated application only\n")
	b.WriteString("uses a capability where project code explicitly references it.\n\n")
	wrote := false
	if set["db"] {
		b.WriteString("- **db** installs a pool and repository but connects nothing. No pool is\n")
		b.WriteString("  constructed and no database credential is needed until a provider consumes\n")
		b.WriteString("  the pool, either directly or through a repository you register in\n")
		b.WriteString("  `internal/di/providers.go` (see `internal/data/README.md`).\n")
		wrote = true
	}
	if set["redis"] {
		b.WriteString("- **redis** installs a client but never dials or pings; nothing generated\n")
		b.WriteString("  imports it, so an ordinary serve needs no Redis setting (see\n")
		b.WriteString("  `internal/redisclient/README.md`).\n")
		wrote = true
	}
	if set["mail"] {
		b.WriteString("- **mail** installs an SMTP sender but sends nothing; constructing it opens\n")
		b.WriteString("  no connection and nothing generated imports it (see\n")
		b.WriteString("  `internal/mailsender/README.md`).\n")
		wrote = true
	}
	if set["storage"] {
		b.WriteString("- **storage** installs an S3-compatible client but connects nothing and\n")
		b.WriteString("  creates no bucket; nothing generated imports it (see\n")
		b.WriteString("  `internal/objectstore/README.md`).\n")
		wrote = true
	}
	if set["ratelimit"] {
		b.WriteString("- **ratelimit** installs a per-client limit but limits nothing until you\n")
		b.WriteString("  add `RateLimitMiddleware(cfg)` to `middlewareChain` in\n")
		b.WriteString("  `internal/httpserver/middleware.go`; set `rate_limit.trusted_proxies` behind\n")
		b.WriteString("  a reverse proxy (see `internal/ratelimit/README.md`).\n")
		wrote = true
	}
	if set["jwt"] {
		b.WriteString("- **jwt** installs a token manager and middleware but issues no token and\n")
		b.WriteString("  guards no route until you inject `*jwtauth.Manager` into\n")
		b.WriteString("  `NewHTTPPolicies` in `internal/di/http_policies.go`; the manager is pruned\n")
		b.WriteString("  until then, so an ordinary serve needs no JWT secret. The secret is required\n")
		b.WriteString("  only when a manager is constructed, must be at least 32 bytes, and is never\n")
		b.WriteString("  logged (see `internal/jwtauth/README.md`). Tokens are stateless: no\n")
		b.WriteString("  revocation or refresh token is provided.\n")
		wrote = true
	}
	if set["user"] {
		b.WriteString("- **user** installs the account table, repository and bcrypt login service but\n")
		b.WriteString("  creates no account and mounts no route. Consume `*auth.UserService` from a\n")
		b.WriteString("  handler, module or `NewHTTPPolicies`; `internal/di/user_provider.go` wires the\n")
		b.WriteString("  pool and JWT manager lazily. Grant roles with `SetRoles` from a controlled\n")
		b.WriteString("  path, never from unauthenticated registration (see `internal/auth/README.md`).\n")
		wrote = true
	}
	if set["cron"] {
		b.WriteString("- **cron** installs a scheduler but schedules nothing by itself: it starts and\n")
		b.WriteString("  stops with `serve` and runs only the jobs you add in\n")
		b.WriteString("  `internal/di/cron_jobs.go`; add service parameters and register their\n")
		b.WriteString("  func(context.Context) error methods, then run `make generate`. Lifecycle is in\n")
		b.WriteString("  `internal/di/cron_provider.go` (see `internal/cron/README.md`).\n")
		wrote = true
	}
	if set["seed"] {
		b.WriteString("- **seed** installs a seeding command but seeds nothing by itself: `seed`\n")
		b.WriteString("  runs only the seeders you register in `internal/data/seeder/register.go`,\n")
		b.WriteString("  through the repositories wired in `internal/di/seed_providers.go` (see\n")
		b.WriteString("  `internal/data/seeder/README.md`). The seeders are behind the `seed`\n")
		b.WriteString("  build tag, so the production binary carries none of them; run them with\n")
		b.WriteString("  `make seed` or `weld seed`.\n")
		wrote = true
	}
	if set["events"] {
		b.WriteString("- **events** installs a publisher but registers no listener and publishes\n")
		b.WriteString("  nothing: delivery happens only where project code publishes, and only to\n")
		b.WriteString("  the listeners you add in `internal/di/events_listeners.go` (see\n")
		b.WriteString("  `internal/events/README.md`). Add service parameters and events.On calls,\n")
		b.WriteString("  then run `make generate`; HTTP registers them automatically before startup.\n")
		b.WriteString("  Commands do not automatically consume server listeners. The stable seam\n")
		b.WriteString("  `internal/di/events_provider.go` picks the mode from its `asyncEvents`\n")
		b.WriteString("  switch (false by default): synchronous delivery, or the in-process,\n")
		b.WriteString("  best-effort `events.Async` — not a queue.\n")
		wrote = true
	}
	if set["loom"] {
		b.WriteString("- **loom** is installed with every non-base addition. Command graphs share\n")
		b.WriteString("  available bindings through `commonModule`; the HTTP server graph exists only\n")
		b.WriteString("  with **http**. Unused DB, Redis, mail, storage and event providers are pruned,\n")
		b.WriteString("  and an unconsumed JWT manager or user service is pruned too, so a serve\n")
		b.WriteString("  needs no JWT secret or database until a policy consumes it. Short commands\n")
		b.WriteString("  never start\n")
		b.WriteString("  HTTP or cron.\n")
		wrote = true
	}
	if wrote {
		b.WriteString("\n")
	}
}

// scaffoldTask is one row of the "use weld, do not hand-write" table: what an
// agent is about to build, the command that scaffolds it, the capabilities it
// needs, and what is left to do after the command ran.
type scaffoldTask struct {
	task     string
	command  string
	requires []string
	// after is the follow-up once the requirement is installed. It may name a
	// capability's files, so it is only printed when that capability is present.
	after string
}

// scaffoldTasks lists every per-name generator and the commands that keep
// generated code in sync. The order is the order an agent usually meets them.
var scaffoldTasks = []scaffoldTask{
	{"HTTP business module (routes, service, DTOs)", "weld add module <name>", []string{"http"}, "mounted under `/api/<name>` automatically; edit `service.go`, `dto.go`, `handler.go`"},
	{"database table with list/get/create/update/delete routes", "weld add crud <name> [field:type...]", []string{"http", "db"}, "writes the migration, sqlc queries, repository and module, then runs sqlc and Loom"},
	{"root CLI command group", "weld add command <name>", []string{"loom"}, "edit `internal/commands/<name>`; inject through `Deps`/`NewDeps`"},
	{"CLI commands over an existing module's service", "weld add module <name> --command", []string{"http"}, "adds only the command facet to an existing module"},
	{"HTTP middleware", "weld add middleware <name>", []string{"http"}, "add the printed `middleware.<Name>()` line to `middlewareChain` in `internal/httpserver/middleware.go`"},
	{"event type and listener", "weld add listener <name>", []string{"events"}, "add the printed `events.On(...)` line to `NewEventListeners` in `internal/di/events_listeners.go`"},
	{"scheduled job", "weld add job <name> --schedule \"0 3 * * *\"", []string{"cron"}, "add the printed `s.Register(...)` call to `NewCronJobs` in `internal/di/cron_jobs.go`"},
	{"database seeder", "weld add seeder <name>", []string{"seed"}, "registered in `internal/data/seeder/register.go` automatically"},
	{"typed string enum", "weld add enum <name> <value>...", nil, "edit the constants, then `weld gen enum`"},
	{"SQL migration", "weld db create <name>", []string{"db"}, "fill in the goose skeleton; apply with `weld db migrate up`"},
	{"typed client for another weld service", "weld client add <name> --from <contract>", nil, "the producer runs `weld http export`; regenerate instead of editing"},
}

// writeScaffoldCommands is the first instruction an agent reads: weld already
// has a generator for most of what a feature needs, and a hand-written copy is
// not registered in weld.json, not wired into the generated graph and not
// covered by upgrades. It is written for every project, even one with no module
// yet, because that is exactly when an agent would otherwise create one by hand.
func writeScaffoldCommands(b *strings.Builder, set map[string]bool) {
	b.WriteString("## Use weld commands, do not hand-write scaffolds\n\n")
	b.WriteString("**Before you create a package, file or wiring for a new feature, check this\n")
	b.WriteString("table and run the weld command instead of writing the skeleton yourself.**\n")
	b.WriteString("weld records what it generates in `weld.json`, wires it into the generated\n")
	b.WriteString("Loom graph and keeps it upgradable. A hand-written equivalent is none of\n")
	b.WriteString("those: a hand-created `internal/modules/<name>` directory is never mounted,\n")
	b.WriteString("because routes are rendered from the `weld.json` modules record, not a\n")
	b.WriteString("directory scan.\n")
	b.WriteString("Always create business modules with `weld add module <name>` first, then\n")
	b.WriteString("customize what it wrote.\n\n")

	b.WriteString("| To add | Run | Then |\n")
	b.WriteString("|---|---|---|\n")
	for _, task := range scaffoldTasks {
		var missing []string
		for _, name := range task.requires {
			if !set[name] {
				missing = append(missing, "`"+name+"`")
			}
		}
		after := task.after
		if len(missing) > 0 {
			after = "needs " + strings.Join(missing, " and ") + ", not installed yet: add `--with-dependencies` only if the user wants it"
		}
		fmt.Fprintf(b, "| %s | `%s` | %s |\n", task.task, task.command, after)
	}
	b.WriteString("\n")

	b.WriteString("Workflow for every scaffold:\n\n")
	b.WriteString("1. Run the command with `--dry-run` first and read the plan: the files it\n")
	b.WriteString("   writes and any capability it would install.\n")
	b.WriteString("2. Run it for real. A missing requirement is an error naming it; re-run with\n")
	b.WriteString("   `--with-dependencies` only when installing that capability is what the user\n")
	b.WriteString("   asked for. Never install a capability silently.\n")
	b.WriteString("3. Apply the follow-up the command prints (the wiring line for a middleware,\n")
	b.WriteString("   listener or job). weld does not edit project-owned seams for you.\n")
	b.WriteString("4. Customize only inside what was generated, keeping its shape (a module's\n")
	b.WriteString("   `module.go` stays a route table; handler bodies live in `handler.go`).\n")
	b.WriteString("5. Run `make generate` after changing a DI constructor's signature, then\n")
	b.WriteString("   `go build ./...` and `go test ./...`. `weld routes` shows the HTTP surface\n")
	b.WriteString("   from source and `weld config check` catches mistyped `config.yml` keys.\n")
	b.WriteString("6. After `weld add <capability>`, re-run `weld skills` so this file matches.\n\n")

	b.WriteString("For a new capability (database, Redis, mail, storage, JWT, users, cron,\n")
	b.WriteString("events, rate limiting, web frontend), run `weld explain <capability>` to see what\n")
	b.WriteString("it writes and requires, then `weld add <capability>`; `weld list` shows what\n")
	b.WriteString("is installed. Never copy a capability's files in by hand or add its\n")
	b.WriteString("`config.yml` section yourself.\n\n")

	b.WriteString("Never hand-write or hand-edit:\n\n")
	b.WriteString("- `weld.json` — do not hand-edit the manifest to register anything, and never\n")
	b.WriteString("  change a recorded hash to get past a conflict;\n")
	b.WriteString("- a migration filename — `weld db create` allocates the version;\n")
	b.WriteString("- generated code: `*_gen.go` (`weld gen enum`), `internal/di/di.go`,\n")
	b.WriteString("  `di_test.go` and `loom_gen.go` (`weld gen loom` / `make generate`),\n")
	b.WriteString("  `internal/data/sqlc` (`weld gen sqlc`), and `internal/clients/<name>`\n")
	b.WriteString("  (`weld client add`). Edit the source they are generated from and regenerate.\n\n")

	b.WriteString("Write code by hand only where no command fits, and then inside an existing\n")
	b.WriteString("generated package or seam. To bring existing handwritten code in, back it up,\n")
	b.WriteString("scaffold the weld skeleton and migrate the code into it; do not overwrite it\n")
	b.WriteString("or assume `weld add` adopts files it did not write.\n\n")
}

// writeWiringRules says where business wiring goes once the scaffold exists: the
// stable seams, never the regenerated DI sources.
func writeWiringRules(b *strings.Builder) {
	b.WriteString("## Wiring rules\n\n")
	b.WriteString("Never wire business behavior by editing `internal/di/di.go`, `di_test.go`\n")
	b.WriteString("or `loom_gen.go`. Declare custom providers in `providers.go`; declarations\n")
	b.WriteString("alone are pruned unless consumed. With HTTP installed, custom route dependencies\n")
	b.WriteString("belong in `NewProjectRoutes` (`http_routes.go`), startup dependencies in\n")
	b.WriteString("`NewAppDependencies` (`app_provider.go`), and policies in `NewHTTPPolicies`.\n")
	b.WriteString("On older projects upgrade weld's loom payload first to obtain the new seams.\n")
	b.WriteString("Validate business configuration in its consuming provider. Run `make generate`\n")
	b.WriteString("after signature changes and test routes and startup behavior, not just compilation.\n")
	b.WriteString("If add/upgrade reports modified generated DI source, migrate your wiring to\n")
	b.WriteString("seams and restore the source; never change hashes to bypass the conflict.\n\n")
}

// writeModules describes the business modules installed with `weld add module`.
// They are intentionally not capabilities: weld writes them once and never
// regenerates them, so the generated guide must say so rather than imply a later
// add may rewrite them.
func writeModules(b *strings.Builder, manifest *project.Manifest) {
	if len(manifest.Modules) == 0 {
		return
	}
	b.WriteString("## Business modules\n\n")
	b.WriteString("Added with `weld add module <name>`. Unlike generated capability files,\n")
	b.WriteString("weld writes them once and never regenerates them, so they are yours to edit;\n")
	b.WriteString("the default service is an in-memory example, not persistence. Adding another\n")
	b.WriteString("capability re-renders the Loom graph around them but leaves the module\n")
	b.WriteString("packages untouched. Each module keeps `module.go` a route table — one statement\n")
	b.WriteString("per route, the handler named rather than inlined — and the handler bodies in\n")
	b.WriteString("`handler.go`; follow that split when you add a route, so the module's surface\n")
	b.WriteString("stays legible and each handler stays testable on its own. A route that needs a\n")
	b.WriteString("named policy references the project's constant (`httpserver.PolicyAdmin`)\n")
	b.WriteString("rather than repeating the string, so a rename is a compile error rather than an\n")
	b.WriteString("undefined policy at startup.\n\n")
	for _, module := range manifest.Modules {
		fmt.Fprintf(b, "- **%s** v%s — `internal/modules/%s`, mounted by the server graph under `httpserver.APIPrefix` (so `/api/%s` by default).\n", module.Name, module.Version, module.Name, module.Name)
	}
	b.WriteString("\n")
}

// writeCommands describes the root command groups installed with `weld add
// command` and `weld add module --command`. Like modules they are written once
// and never regenerated, so the generated guide says so.
func writeCommands(b *strings.Builder, manifest *project.Manifest) {
	if len(manifest.Commands) == 0 {
		return
	}
	b.WriteString("## Root command groups\n\n")
	b.WriteString("Added with `weld add command <name>` or `weld add module <name> --command`.\n")
	b.WriteString("Each lives in `internal/commands/<name>` and is registered through\n")
	b.WriteString("`app.RegisterCommand` in `internal/app/<name>_command.go`. weld writes them\n")
	b.WriteString("once and never regenerates them, so they are yours to edit. Each group has\n")
	b.WriteString("an editable `internal/di/<name>_graph.go` and resolves its dependencies only\n")
	b.WriteString("when a real subcommand runs; help and unrelated commands start no server. It\n")
	b.WriteString("resolves the graph lazily through the shared `internal/commandkit` seam, so a\n")
	b.WriteString("subcommand injects a dependency by adding a field to `Deps` and a parameter to\n")
	b.WriteString("`NewDeps` (a provider already in `commonModule` needs no graph edit).\n\n")
	for _, command := range manifest.Commands {
		if manifest.HasModule(command.Name) {
			fmt.Fprintf(b, "- **%s** v%s — `internal/commands/%s`, backed by the `%s` module's Service.\n", command.Name, command.Version, command.Name, command.Name)
		} else {
			fmt.Fprintf(b, "- **%s** v%s — `internal/commands/%s`, an independent command group.\n", command.Name, command.Version, command.Name)
		}
	}
	b.WriteString("\n")
}

func writeEditing(b *strings.Builder, set map[string]bool) {
	b.WriteString("## Generated and editable files\n\n")
	b.WriteString("weld records capability files in `weld.json` with their owner, role and baseline.\n")
	b.WriteString("A `seam` is written once and belongs to the project; `payload` and `patched`\n")
	b.WriteString("files follow the upgrade ownership rules. Generated DI sources are re-rendered,\n")
	b.WriteString("but edited sources block replacement with a conflict. `weld list` reports drift\n")
	b.WriteString("as `modified`. Respect generated headers even on files outside the manifest;\n")
	b.WriteString("absence from `weld.json` does not make generator output safe to hand-edit.\n\n")
	if set["loom"] {
		b.WriteString("- Regenerated on every capability change, so do not edit:\n")
		b.WriteString("  `internal/di/di.go`, `internal/di/di_test.go` and `internal/di/loom_gen.go`.\n")
	}
	var seams []string
	if set["loom"] {
		seams = append(seams, "`internal/di/providers.go`")
	}
	if set["http"] {
		seams = append(seams, "`internal/httpserver/middleware.go`", "`internal/httpserver/mounts.go`", "`internal/httpserver/policy.go`", "`internal/httpserver/errors.go`", "`internal/di/http_policies.go`", "`internal/di/http_routes.go`", "`internal/di/app_provider.go`")
	}
	if set["redis"] && set["loom"] {
		seams = append(seams, "`internal/di/redis_provider.go`")
	}
	if set["mail"] && set["loom"] {
		seams = append(seams, "`internal/di/mail_provider.go`")
	}
	if set["storage"] && set["loom"] {
		seams = append(seams, "`internal/di/storage_provider.go`")
	}
	if set["cron"] && set["loom"] {
		seams = append(seams, "`internal/di/cron_provider.go`")
	}
	if set["cron"] {
		seams = append(seams, "`internal/di/cron_jobs.go`")
	}
	if set["jwt"] && set["loom"] {
		seams = append(seams, "`internal/di/jwt_provider.go`")
	}
	if set["user"] && set["loom"] {
		seams = append(seams, "`internal/di/user_provider.go`")
	}
	if set["seed"] && set["loom"] {
		seams = append(seams, "`internal/di/seed_providers.go`")
	}
	if set["seed"] {
		seams = append(seams, "`internal/data/seeder/register.go`")
	}

	if len(seams) > 0 {
		fmt.Fprintf(b, "- Stable seams, written once and never regenerated, so your edits\n  survive later `weld add`: %s.\n", strings.Join(seams, ", "))
	}
	if set["config"] {
		b.WriteString("- `config.yml` is managed but local and git-ignored; weld appends a new\n")
		b.WriteString("  capability's section through its marker region, so your values and comments\n")
		b.WriteString("  are preserved. `environment` (overridden by `APP_ENV`) is `local`,\n")
		b.WriteString("  `development`, `test` or `production`; it gates development conveniences\n")
		b.WriteString("  only — currently logging the composed route table at startup — and carries\n")
		b.WriteString("  no security weight.\n")
	}
	b.WriteString("- This skill file is generated by `weld skills`, separately from the manifest,\n")
	b.WriteString("  and is not recorded in `weld.json`.\n\n")
}

func writeFileIndex(b *strings.Builder, manifest *project.Manifest) {
	if len(manifest.Files) == 0 {
		return
	}
	paths := make([]string, 0, len(manifest.Files))
	for _, file := range manifest.Files {
		paths = append(paths, file.Path)
	}
	sort.Strings(paths)

	b.WriteString("### Files weld manages in this project\n\n")
	b.WriteString("Every path recorded in `weld.json` (weld.json records which capability\n")
	b.WriteString("last wrote each one):\n\n")
	for _, path := range paths {
		fmt.Fprintf(b, "- `%s`\n", path)
	}
	b.WriteString("\n")
}

func writeRegenerate(b *strings.Builder, caps map[string]bool) {
	b.WriteString("## Regenerating this skill\n\n")
	b.WriteString("This file is produced by `weld skills` from `weld.json`; re-run it after\n")
	b.WriteString("adding a capability:\n\n")
	b.WriteString("    weld skills [--dir .] [--dry-run]\n\n")
	b.WriteString("weld refuses to overwrite the file once it differs from the generated content,\n")
	b.WriteString("so your edits are safe. Delete or rename the file to regenerate from scratch.\n\n")
	b.WriteString("Moving an installed capability to a newer scaffold payload is `weld upgrade`.\n")
	b.WriteString("It decides per file from the file's declared role: a file weld owns is rewritten\n")
	b.WriteString("while it still matches `weld.json`; a shared file such as\n")
	b.WriteString("`internal/config/config.go` gets the newer payload skeleton with the live marker\n")
	b.WriteString("regions spliced in; the generated Loom graph is re-rendered; and `go.mod` and\n")
	b.WriteString("`go.sum` are left to the Go toolchain. A project-owned seam is never rewritten,\n")
	b.WriteString("because the project owns it after weld writes it once: weld reports the diff\n")
	b.WriteString("instead")
	if caps["http"] {
		b.WriteString(", which is how a change to `internal/httpserver/middleware.go` or\n`internal/httpserver/policy.go` reaches you")
	}
	b.WriteString(". Stable DI provider/wiring files and seeder `register.go`, the `Makefile` and `config.yml`\n")
	b.WriteString("are seams too. A file you changed is reported with a unified diff, and a file you\n")
	b.WriteString("deleted is never resurrected. After writing, `weld upgrade` runs `go vet ./...`\n")
	b.WriteString("and rolls the whole change back if the project no longer builds, so an upgrade\n")
	b.WriteString("never leaves the project broken; `--verify=build|none` selects the check.\n")
	b.WriteString("`weld upgrade --check` reports the same thing for CI.\n\n")
	writeUpgradeWorkflow(b)
}

// writeUpgradeWorkflow tells an agent how to carry a project forward to what the
// running weld generates: which report to read, which diff to apply so a project
// edit is never undone, and how to close each change.
func writeUpgradeWorkflow(b *strings.Builder) {
	b.WriteString("### Upgrading with an agent\n\n")
	b.WriteString("The goal is a project that matches what this weld generates today for the same\n")
	b.WriteString("capabilities, with the project's own work kept. Work through it in order:\n\n")
	b.WriteString("1. `weld upgrade --dry-run --json > upgrade.json` and read it: `upgraded`,\n")
	b.WriteString("   `operations`, `changelog` (its `actions` are manual steps a diff cannot\n")
	b.WriteString("   express) and `advisories`.\n")
	b.WriteString("2. `weld upgrade` writes every file weld owns and records each advisory as\n")
	b.WriteString("   pending in `weld.json`.\n")
	b.WriteString("3. For each advisory, apply `upstreamDiff` (weld's change) and keep `localDiff`\n")
	b.WriteString("   (the project's change). When `conflicts` is 0, `merged` is exactly that file;\n")
	b.WriteString("   otherwise settle the blocks between the conflict markers. Never apply `diff`\n")
	b.WriteString("   blindly: it runs from the project's file to `target`, so the project's edits\n")
	b.WriteString("   appear as removals. Without `base`, compare the file with `target` by hand.\n")
	b.WriteString("   A `patch` advisory lists snippet lines its marker region lacks; add them\n")
	b.WriteString("   inside the region.\n")
	b.WriteString("4. Carry out the changelog actions, then `go build ./... && go test ./...`.\n")
	b.WriteString("5. `weld upgrade resolve <path>...` (or `--all`) accepts the folded files; it\n")
	b.WriteString("   refuses a file that still has conflict markers. Commit `.weld/baseline/`:\n")
	b.WriteString("   it is the base the next upgrade's three-way merge needs.\n")
	b.WriteString("6. `weld upgrade --check` must pass.\n\n")
}
