# User accounts

Account persistence and authentication for __name__. The code follows the
project's data/auth split instead of adding a top-level `internal/user` package:

- `db/migrations/000002_create_users.sql` — the `users` table.
- `db/query/users.sql` — the named account statements. The existing
  `sqlc.yaml` already reads the whole `db/query` directory, so no config change
  is needed.
- `internal/data/sqlc` — the project's generated package. The capability does
  not ship `users.sql.go` or an edited `models.go`; `make sqlc` generates them
  together with the existing items output.
- `internal/data/user_repo.go` — `UserRepository` over the generated queries.
- `internal/auth/user.go` — bcrypt registration/login and JWT session issuance.
- `internal/di/user_provider.go` — the stable Loom seam that wires the
  repository and the JWT manager into the service.

The capability creates **no** default account, administrator or route. A new
account has no roles; an administrator grants roles explicitly with
`UserService.SetRoles`.

## Schema

| column | purpose |
| --- | --- |
| `id` | `bigint` identity, assigned by PostgreSQL (`GENERATED ALWAYS AS IDENTITY`) |
| `username` | normalized lower-case login name, unique |
| `password_hash` | bcrypt hash; the plaintext password is never stored |
| `roles` | PostgreSQL `text[]` of lower-case authorization names |
| `created_at` | `timestamptz`, set by the database |
| `updated_at` | `timestamptz`, updated when roles change |

The migration enforces `username = lower(username)`, a non-blank username and a
non-blank password hash. The service also normalizes the name before hitting the
database, so `Alice`, `alice` and ` ALICE ` are one account.

`id` is a database identity, not a secret: it is compact and index-friendly, but
sequential values reveal creation order and roughly how many accounts exist. Do
not use the ID alone as authorization; check ownership on every read or write.
If an API needs an opaque public identifier, add a separate random column (for
example a UUID `public_id`) instead of exposing the identity as a capability.

## Upgrading from 0.1.0

`user` 0.2.0 changes `users.id` from a random `text` value to
`bigint GENERATED ALWAYS AS IDENTITY`. A new project gets the new table from
`000002_create_users.sql`. If an existing database already applied the 0.1.0
migration, replacing the file does **not** change the live table; `weld upgrade
user` updates source files, not the database. For local development with no
account data, drop and recreate the table (or the database) and migrate again.
For a database with account data, write a deliberate migration that creates a
bigint identity column, maps or preserves the old IDs, updates every foreign
key, and only then replaces the primary key. Do not assume the schema changed
because the template did.

## Using the service

```go
accounts, err := auth.NewUserService(data.NewUserRepository(pool), tokens)
if err != nil {
	return err
}

account, err := accounts.Register(ctx, "alice", "correct horse battery staple")
if err != nil {
	return err
}

session, err := accounts.Login(ctx, "alice", "correct horse battery staple")
if err != nil {
	return err
}
// session.Account has no password hash; session.Token is the JWT.
```

`Login` is `Authenticate` followed by `IssueToken`. The token subject is the
decimal account ID and the claims carry the account's current roles. `Register`
grants no roles, and `IssueToken` must only be called after an identity check.

To grant the first administrator:

```go
account, err := accounts.SetRoles(ctx, account.ID, []string{"admin"})
```

Do that from a controlled one-off command, a seeder or an existing
administrator; never from an unauthenticated registration endpoint.

## Wiring

`internal/di/user_provider.go` declares `NewUserService(pool, jwtManager)` but
Loom prunes it until a root consumes it. Inject `*auth.UserService` into a
business module's `NewService` or into `NewHTTPPolicies` in
`internal/di/http_policies.go`:

```go
func NewHTTPPolicies(accounts *auth.UserService) ([]httpserver.NamedPolicy, error) {
	return []httpserver.NamedPolicy{
		{Name: httpserver.PolicyUser, Middleware: NewSessionGuard(accounts)},
	}, nil
}
```

The guard, the login/registration handlers and their DTOs are application code:
the capability deliberately does not mount an open registration endpoint. A
login handler should map `auth.ErrInvalidCredentials` to `401` without saying
whether the username exists, set HTTPS-only transport in production and rate
limit repeated failures. Never log the password or the token.

## Migrations and SQL generation

The table is a normal goose migration:

```sh
weld db migrate up
# If this database already has migrations newer than 000002:
weld db migrate up --allow-missing
```

Installing `user` writes only the migration, the query, the repository and the
auth service. The generated account code is produced by the project's existing
generator, not shipped as capability payload:

```sh
make sqlc
```

That single command reads the one `sqlc.yaml`, all migrations and all query
files, and writes `internal/data/sqlc/users.sql.go` plus the `User` model. Run it
before `make build` after installing `user`, and again after changing
`db/query/users.sql`. There is no `data/usersqlc` or second sqlc config.

## Tests

The unit tests use an in-memory `UserStore` and a fake token issuer, so they need
no PostgreSQL:

```sh
go test ./internal/data/... ./internal/auth/...
```

They cover username normalization, bcrypt hashing, duplicate registration,
credential failures, role normalization, token subject/roles and the generated
row projections. The data test needs the generated `internal/data/sqlc` package,
so run `make sqlc` once before `go test ./internal/data/...`.
