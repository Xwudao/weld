# internal/data

The PostgreSQL persistence layer. It imports `pgx/v5` only — no HTTP server and
no JSON DTO shape leaks in — so it can be used by a plain CLI project and does
not depend on `internal/api`.

## The database is wired by hand

`weld add db` installs this package; it does not connect it. Nothing in the
generated application constructs a pool or a repository, and serving the HTTP or
JSON API capability never requires database credentials. The default JSON API is
an in-memory development demo (`api.NewService()`) whose data is lost on
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

The project's Loom graph already declares
`*pgxpool.Pool` and `data.Repository` as available bindings. Rather than
construct the pool yourself, edit `internal/di/api_provider.go`, the stable
provider seam, to have your service consume `data.Repository`:

```go
// internal/di/api_provider.go
func NewAPIService(repo data.Repository) api.Service {
	return myService{repo: repo} // your repository-backed api.Service
}
```

Loom then constructs the pool, the repository and your service together,
validating the database settings at that point, and weld never rewrites the file,
so the edit survives later capability installs. Do not add providers to
`internal/di/di.go`: it is regenerated. See
`internal/di/README.md` and `internal/api/README.md`.

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

or run the pinned tool directly (note the working directory: the tool must run
inside its module, while the `-f` flag points back at `sqlc.yaml`):

```sh
cd db/tools && go tool sqlc generate -f ../../sqlc.yaml
```

`sqlc.yaml` resolves `schema`, `queries` and `out` relative to the `sqlc.yaml`
file, so this writes `internal/data/sqlc` at the project root either way. A
regeneration with the pinned version must reproduce the committed files exactly;
if it does not, the inputs changed.

## Migrations

Migrations are goose migration files under `db/migrations`, one file per
change, each with a `-- +goose Up` and a `-- +goose Down` section. goose tracks
applied versions in a `goose_db_version` table, so `migrate-up` is idempotent:
running it again applies nothing. The goose binary is pinned in `db/tools`, the
same nested module as sqlc, so no global install is needed.

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
go test ./internal/data/...
```

They cover the row-to-domain conversion and dsn validation. The SQL itself is
exercised only by a focused integration test against real PostgreSQL:

```sh
PG_TEST_DSN='postgres://user:pw@localhost:5432/app_test?sslmode=disable' \
  go test ./internal/data/... -run TestPostgresRepositoryIntegration -v
```

`PG_TEST_DSN` must point at a **dedicated, disposable test database**. There is
no fallback to `DATABASE_URL`, so the test can never target the application
database by accident; when `DATABASE_URL` is set the test also refuses to run if
both name the same database. The test does not assume it owns the database
either: it creates a unique throwaway schema, pins the pool's `search_path` to
it, applies the migration there, and drops only that schema with `CASCADE` (so
the role needs `CREATE` on the database). It never runs a bare `DROP TABLE`
against the database's real tables, and it never logs a credential. With
`PG_TEST_DSN` unset the integration test skips, and `go test ./...` stays green
without PostgreSQL.

`go test ./...` from the project root does not descend into `db/tools`, because
that is a separate module.
