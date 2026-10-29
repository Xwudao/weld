# go-sqlcx (sqlcx capability)

`weld add sqlcx` adds [go-sqlcx](https://github.com/Xwudao/go-sqlcx), type-safe
dynamic SQL for the models sqlc already generates. It is not an ORM: static
statements stay in `db/query` as sqlc queries, and go-sqlcx covers the
statements whose shape depends on input — optional filters, sort orders chosen
at run time, pagination with a total count, partial updates.

## What it adds

| Path | Role |
|---|---|
| `tools/sqlcx/` | nested module pinning `sqlc-dynamic` and the sqlc release it drives |
| `sqlc.yaml` (`weld:sqlcplugins`, `weld:sqlccodegen`) | the `sqlc-dynamic` process plugin and its codegen output |
| `internal/data/sqlc/dynamic/` | generated: `sqlcx.manifest.json` (plugin output) and `sqlcx_gen.go` (one descriptor per table) |
| `internal/data/items_search.go` | `SearchItems`, an example over the `items` table |
| `Makefile` (`weld:db`) | `SQLC_GENERATE` now runs `sqlc-dynamic generate` |
| `go.mod` | `github.com/Xwudao/go-sqlcx`; the Go floor rises to 1.27 and pgx to v5.9.2 |

The install runs the generator once, so `internal/data/sqlc/dynamic` covers every
table the project already has (including those added by `weld add user` or
`weld add crud`). Like `internal/data/sqlc`, the generated directory is not
recorded in `weld.json`; commit it.

## Regenerating

sqlc.yaml now names a process plugin, so a bare `sqlc generate` fails unless
`sqlc-dynamic` is on PATH. Always regenerate through one of:

```sh
make sqlc
weld gen sqlc            # the same command, without make
weld gen sqlc --check    # CI: sqlc diff + link check, writes nothing
cd tools/sqlcx && go tool sqlc-dynamic generate -f ../../sqlc.yaml
```

`sqlc-dynamic generate` runs the sqlc pinned in `tools/sqlcx` with itself on
sqlc's PATH, then links the plugin manifest with the Go code sqlc wrote.
`tools/sqlcx` pins the same sqlc version as `db/tools`; bump both together.

## Using it

```go
t := dynamic.Items
rows, total, err := t.Query().
	Where(
		t.Status.Eq(status).If(status != ""),
		t.Name.Contains(q).If(q != ""), // LIKE wildcards in q match literally
	).
	OrderBy(t.CreatedAt.Sort(newestFirst), t.ID.Sort(newestFirst)).
	Page(page, size).
	AllAndCount(ctx, pool) // []sqlc.Item, the model sqlc generated
```

Every column is typed with the Go type of the sqlc model field, so
`t.Quantity.Eq("3")` does not compile. The executor accepts the pool or a
`pgx.Tx`. Updates refuse to run without a WHERE clause:

```go
_, err := t.Update().Set(t.Status, "archived").Where(t.ID.Eq(id)).Exec(ctx, pool)
```

`internal/data/items_search.go` shows the pattern a repository method follows:
build the statement in one function (unit-tested with `Build()` and no
database, see `items_search_test.go`) and convert the sqlc rows to the domain
model. `items_search_integration_test.go` runs it against PostgreSQL when a test
database is configured. The `items` table is the db capability's demo; delete
the example together with it.

See the go-sqlcx README for the full API: joins, subqueries, CTEs, JSONB and
array operators, keyset pagination, upserts and projections into your own
structs.
