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

## Build tag

Every seeder — and the `seed` command itself — lives behind the `seed` build
tag (`//go:build seed`), so the production binary `go build` / `weld build`
produce has no `seed` command, no seed data and none of the faker or fixture
packages a seeder imports. Only a seed-tagged build carries them:

```sh
make seed                 # runs: go run -tags seed . seed
weld seed                 # compiles the project with -tags seed and runs it
weld seed items           # a single seeder
go run -tags seed . seed  # the underlying command make/weld seed use
```

An untagged `go run . seed` reports an unknown command, so a production binary
never suggests it can seed. The tag defaults to `seed`; override it for `weld
seed` with `weld.yml`'s `seed.tag` or `weld seed --tag <tag>`. The generated
`make seed` target uses the literal `seed` tag — change it there too if you
change `seed.tag`.

## Adding a seeder

Seeds are split by file. Each seeder is a `seed_<name>.go` file in this package,
and `register.go` is the short index that lists them in run order, so adding one
never touches the others.

Generate the file and its registration with:

```sh
weld add seeder <name>                     # writes seed_<name>.go and registers it
weld add seeder <name> --with-dependencies # also installs db/loom/config/seed
```

The generated `seed_<name>.go` carries the `//go:build seed` tag too, so keep
that first line when you edit it.

The command validates the name and is idempotent (a repeated add is a no-op).
Then wire the repository it needs and write the body:

1. Add a repository field to `Deps` (`seeder.go`) if the shipped `Items` is not
   the repository you need, and construct it in `internal/di/seed_providers.go`,
   the stable seam:

   ```go
   func NewSeedDeps(pool *pgxpool.Pool) *seeder.Deps {
       return &seeder.Deps{
           Items:   data.NewRepository(pool),
           APIKeys: data.NewAPIKeyRepo(pool),
       }
   }
   ```

2. Edit the generated `seed_<name>.go` to write the data. Registering it is
   already done; reorder the lines in `register.go` if the run order matters.

To add one without the generator, copy `seed_items.go` to `seed_<name>.go` and
add the `r.Register(<name>, seed<Name>)` line to `register.go` yourself.

If a seeder grows enough to want its own package, put it under
`internal/data/seeder/<name>/` and register a thin wrapper from `register.go`
(`func seedUsers(ctx context.Context, deps *Deps) error { return users.Seed(ctx, deps) }`);
the subpackage imports this one for `Deps` and stays testable on its own.

Seeders **must be idempotent**: there is no `seed_runs` ledger, and running seed
twice re-runs every seeder. Check for an existing row before inserting, or make
your repository's insert an upsert (`INSERT ... ON CONFLICT DO NOTHING`). The
shipped `items` seeder uses the check-then-insert form.

weld ships no faker dependency. For generated fake data, add one yourself — for
example `github.com/brianvoe/gofakeit/v7` — and call it inside a seeder. It is
linked only into the seed-tagged build, never into the production binary.

## Stable files

`internal/data/seeder/register.go` and `internal/di/seed_providers.go` are
written once and never regenerated, so your edits survive every later
`weld add`. `seeder.go` and the example `seed_items.go` are also written once;
add your own `seed_<name>.go` files beside them. `register_stub.go` is the
untagged `!seed` stand-in that keeps the package compiling without the tag.
`internal/di/di.go` and `internal/di/loom_gen.go` are regenerated and must not be
edited.
