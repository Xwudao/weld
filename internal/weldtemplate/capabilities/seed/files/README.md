# internal/data/seeder

Database seeding for __name__. A **seeder** is a named function that writes
development or reference data through the repositories the Loom seed graph
injects. Seeders run from the short `seed` command (`make seed`) and stop at the
first error.

## Running

```sh
make seed              # every registered seeder, in registration order
make seed ARGS=items   # a single seeder (same as: go run . seed items)
```

`seed` is a short command: it resolves the seed graph, runs the seeders and
exits. It reads the same `config.yml` (and `DATABASE_URL`) as the rest of the
application. The seed graph is the only root that consumes `*pgxpool.Pool`, so
serving still needs no database credential.

## Adding a seeder

1. Add a repository field to `Deps` (`seeder.go`) if the shipped `Items` is not
   the repository you need.
2. Construct it in `internal/di/seed_providers.go`, the stable seam:

   ```go
   func NewSeedDeps(pool *pgxpool.Pool) *seeder.Deps {
       return &seeder.Deps{
           Items:   data.NewRepository(pool),
           APIKeys: data.NewAPIKeyRepo(pool),
       }
   }
   ```

3. Register the seeder in `register.go` and write it.

Seeders **must be idempotent**: there is no `seed_runs` ledger, and running seed
twice re-runs every seeder. Check for an existing row before inserting, or make
your repository's insert an upsert (`INSERT ... ON CONFLICT DO NOTHING`). The
shipped `items` seeder uses the check-then-insert form.

weld ships no faker dependency. For generated fake data, add one yourself — for
example `github.com/brianvoe/gofakeit/v7` — and call it inside a seeder.

## Stable files

`internal/data/seeder/register.go` and `internal/di/seed_providers.go` are
written once and never regenerated, so your edits survive every later
`weld add`. `seeder.go` is also written once. `internal/di/di.go` and
`internal/di/loom_gen.go` are regenerated and must not be edited.
