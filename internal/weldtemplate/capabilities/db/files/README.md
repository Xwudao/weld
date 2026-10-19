# internal/data

The PostgreSQL persistence layer. It imports `pgx/v5` only — no HTTP server and
no JSON DTO shape leaks in — so it can be used by a plain CLI project and does
not depend on the HTTP layer.

## The database is wired by hand

`weld add db` installs this package; it does not connect it. Nothing in the
generated application constructs a pool or a repository, and serving the HTTP
capability never requires database credentials. A generated business module's
default `Service` is an in-memory development demo whose data is lost on
restart.

To use the database, construct the pool and repository yourself and inject them
where your business code needs them:

```go
if err := cfg.ValidateDatabase(); err != nil {
	return err
}
pool, err := data.NewPool(ctx, cfg.DSN())
if err != nil {
	return err
}
defer pool.Close()
repo := data.NewRepository(pool)
```

`config.Database.ValidateDatabase` and `Config.DSN` exist for exactly this
explicit path; an unrelated startup never calls them.

The project's Loom graph declares `*pgxpool.Pool` as an available binding and
ships `NewRepository`, but it registers no `data.Repository` by default: the
generated graph claims no
production consumer. Register a repository in `internal/di/providers.go`,
the project-owned seam `commonModule` includes, rather than editing the
regenerated `internal/di/di.go`:

```go
// internal/di/providers.go
var projectModule = loom.Module(
	loom.As[data.Repository](NewRepository),
)
```

Then have a provider consume it — for example edit the `service.go` of one of
your business modules, whose `NewService` the server graph binds:

```go
// internal/modules/<name>/service.go
func NewService(repo data.Repository) Service {
	return &repositoryService{repo: repo} // your repository-backed Service
}
```

Loom then constructs the pool, the repository and your service together,
validating the database settings at that point, and weld never rewrites the
stable files, so the edits survive later capability installs. Do not add
providers to `internal/di/di.go`: it is regenerated. See
`internal/di/README.md` and `internal/modules/<name>/README.md`.

## The development query log

`di.NewPool` attaches `data.WithQueryLog` to the graph's logger when the
configuration marks a development environment, so every statement the pool runs
is one record with the statement, its arguments, the elapsed time and either the
row count or the error. Any other environment attaches nothing, so a production
pool logs no statement and no argument.

A pool built outside the graph passes the option itself:

```go
pool, err := data.NewPool(ctx, cfg.DSN(), data.WithQueryLog(logger))
```

The statement is the field the text logger paints (see `internal/logging`), so it
stands out among the request records. The record carries the arguments verbatim,
so attach the option to a development pool only: a production statement's
arguments can hold a credential.

## SQL is the source of truth

```
db/migrations/   schema, one goose migration file per change
                (-- +goose Up / -- +goose Down sections)
db/query/        named sqlc queries
sqlc.yaml        sqlc config: reads the two above, writes internal/data/sqlc
db/tools/        a nested Go module that pins the sqlc generator and goose
internal/data/   Repository + Item, the pool, and the generated sqlc package
```

`internal/data/sqlc` is **generated**; `internal/data/data.go` is the hand-written
wrapper that turns generated rows into the domain `Item`. Do not edit anything
under `internal/data/sqlc` by hand.

`internal/data/data.go` defines a small `Repository` interface and its
`PostgresRepository` implementation, plus `NewPool` for the connection pool and
`WithTx` for a narrow transaction. The pool is injected, so the application owns
its lifecycle.

## Regenerating the queries

The sqlc version is pinned in `db/tools/go.mod` (`tool
github.com/sqlc-dev/sqlc/cmd/sqlc`), alongside the goose migration tool (see
below). That module is separate on purpose: the generators' large dependency
graphs never enter the application `go.mod` or its `go test ./...`. Its `go`
directive tracks sqlc's own requirement (currently Go 1.26), which is newer than
this project's floor; the application still builds and tests on the older
toolchain, and Go's automatic toolchain download fetches the newer one for
`make sqlc` when needed. Regenerate from the project root:

```sh
make sqlc
```

From a weld-managed checkout the same generator is available as `weld gen sqlc`
(and `weld gen sqlc --check` in CI, which runs `sqlc diff` and fails without
writing when the committed files are stale). It invokes the pinned tool from
`db/tools` exactly like the Makefile does:

```sh
weld gen sqlc
weld gen sqlc --check
```

Either path ultimately runs the pinned tool directly (note the working
directory: the tool must run inside its module, while the `-f` flag points back
at `sqlc.yaml`):

```sh
cd db/tools && go tool sqlc generate -f ../../sqlc.yaml
```

`sqlc.yaml` resolves `schema`, `queries` and `out` relative to the `sqlc.yaml`
file, so this writes `internal/data/sqlc` at the project root either way. A
regeneration with the pinned version must reproduce the committed files exactly;
if it does not, the inputs changed.

## Migrations

Migrations are goose migration files under `db/migrations`, one file per
change, each with a `-- +goose Up` and a `-- +goose Down` section and a version
prefix chosen at install or creation time. goose tracks applied versions in a
`goose_db_version` table and compares that prefix, so a new migration always
sorts after the existing ones: `weld add db`, `weld add user` and `weld db create`
all pick the later of the current UTC timestamp and one past the highest version
already here. That is why applying a capability's migration needs no
`--allow-missing`. The goose binary is pinned in `db/tools`, the same nested module
as sqlc, so no global install is needed.

The Makefile targets read the dsn from `DATABASE_URL` and refuse to run without
it; without the guard goose (and psql) would silently fall back to a local
database. The dsn is supplied at run time and never stored in the repository.

```sh
DATABASE_URL='postgres://user:pw@localhost:5432/app?sslmode=disable' make migrate-up
DATABASE_URL='postgres://user:pw@localhost:5432/app?sslmode=disable' make migrate-status
```

`migrate-down` reverts exactly one migration and refuses to run without an
explicit confirmation, so it can never roll the whole schema back by accident:

```sh
DATABASE_URL='postgres://user:pw@localhost:5432/app?sslmode=disable' \
  CONFIRM=1 make migrate-down
```

Without the pinned tool you can run goose directly from `db/tools`; `-dir` is
resolved from that directory:

```sh
cd db/tools && go tool goose -dir ../migrations postgres "$DATABASE_URL" up
```

## Tests

The unit tests run with no database:

```sh
make test            # or: go test ./...
```

They cover the row-to-domain conversion, dsn validation and the test-database
resolver. The SQL itself is exercised only by a focused integration test
against real PostgreSQL. Configure a dedicated test database in `config.yml`:

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

Then run:

```sh
go test ./internal/data/... -run TestPostgresRepositoryIntegration -v
# or, to fail rather than skip when the test database is missing:
make test-integration
```

A test never constructs a dsn. It asks `internal/testdb` for a pool:

```go
pool := testdb.Pool(t)
```

`testdb.Pool(t)` resolves the test database from `PG_TEST_DSN` (or
`TEST_DATABASE_URL`) or from `config.yml`'s `database_test` section, then:

- **never falls back to the application database**: `database.dsn` and
  `DATABASE_URL` are not used to name the test database, so a stray environment
  variable cannot point the tests at a live database. The one exception is
  `database_test.schema_only: true`, which reuses the application database in a
  throwaway schema and is refused outside a local, development or test
  environment;
- **refuses the application database**: it compares host, port and database and
  fails if the test target is the application target;
- **isolates itself**: it creates a unique throwaway schema, pins the pool's
  `search_path` to it, applies every migration under `db/migrations` there, and
  drops only that schema with `CASCADE` (so the role needs `CREATE` on the
  database). It never runs a bare `DROP TABLE` against the database's real
  tables, and it never logs a credential;
- **skips visibly** when no test database is configured, unless
  `WELD_REQUIRE_DB_TESTS=1` turns the skip into a failure. Set that in CI so a
  missing test database cannot look like a pass.

In CI, where `config.yml` is git-ignored, set the full dsn instead:

```sh
PG_TEST_DSN='postgres://user:pw@localhost:5432/app_test?sslmode=disable' \
  WELD_REQUIRE_DB_TESTS=1 \
  go test ./internal/data/... -run TestPostgresRepositoryIntegration -v
```

`go test ./...` from the project root does not descend into `db/tools`, because
that is a separate module.
