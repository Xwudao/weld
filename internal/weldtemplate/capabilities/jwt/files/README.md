# internal/jwtauth

HS256 JSON Web Token issue/verify for the application. The package is ordinary
Go: it owns no route, starts no goroutine and stores no token. Installing the
`jwt` capability writes it; the application decides where a token is issued and
which routes require one.

## Configuration

`config.yml` gains a `jwt` section when the capability is installed:

```yaml
jwt:
  secret: ""              # required when a manager is constructed; use >= 32 random bytes
  issuer: "my-app"        # required; written into and verified from every token
  audience: ""            # optional; empty disables the audience check
  ttl_seconds: 900        # token lifetime
  leeway_seconds: 30      # clock-skew tolerance
```

Every value is overridable in the environment (`JWT_SECRET`, `JWT_ISSUER`,
`JWT_AUDIENCE`, `JWT_TTL_SECONDS`, `JWT_LEEWAY_SECONDS`), so production does not
need the secret in the image. `config.Secret` redacts it in `fmt`, logging, JSON
and YAML. The manager is constructed only when a provider consumes it: installing
`jwt` alone does not require a secret and does not guard a route.

Use a random 32-byte-or-longer secret, for example:

```sh
export JWT_SECRET="$(openssl rand -base64 48)"
```

A short secret is rejected rather than accepted with a warning. The manager
accepts **HS256 only**; `alg: none`, RS256 and other algorithms are rejected, so
the token cannot be reinterpreted by a verifier with a different policy.

## Wire a policy

The generated `internal/di/jwt_provider.go` is the stable provider seam. Inject
the manager into `NewHTTPPolicies` in `internal/di/http_policies.go`:

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

Then run `make generate`. A route that references `httpserver.PolicyUser` or
`httpserver.PolicyAdmin` now runs JWT verification. The generated
`AdminGuard` remains deny-all until you replace it as above; that is deliberate.

`httpserver.PolicyUser` is a new policy name, not an enforcement check. It exists
so a route can say "an authenticated caller" without repeating a string. The
policy still has to be declared in `NewHTTPPolicies`.

For a public route that issues a token, authenticate the credentials in the
handler and call `Issue`:

```go
func handleLogin(auth *jwtauth.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Verify the password (argon2id/bcrypt) or another credential here.
		token, err := auth.Issue("user-42", []string{"user"})
		if err != nil {
			httpx.WriteError(w, err)
			return
		}
		httpx.JSON(w, http.StatusOK, map[string]string{"token": token})
	}
}
```

Never issue a token before authenticating the subject. `Issue` does not check a
password, a database row or a role assignment; it only signs what the caller
gives it.

## Claims

`Claims` embeds `jwt.RegisteredClaims` and adds `Roles []string`. After
`Manager.Middleware`, the request context carries the claims:

```go
claims, ok := jwtauth.ClaimsFrom(r.Context())
if !ok {
	// The route should be guarded by a JWT policy; a missing value means the
	// policy was not installed or the handler was called directly.
	return httpx.Unauthorized("authentication required")
}
subject := claims.Subject
isAdmin := claims.HasRole("admin")
```

`RequireRole("admin")` returns a middleware for a single role, and
`RequireAnyRole("editor", "admin")` accepts any listed role. A missing or invalid
token produces a `401`; a valid token without the role produces a `403`. Both use
the shared `{code,msg,data}` envelope and set `WWW-Authenticate: Bearer` on the
401.

## Operational limits

- **Stateless by design.** There is no revocation list, refresh token or session
  table. A token stays valid until it expires. To revoke one immediately, add a
  store keyed by `claims.ID` (the `jti` claim) or lower the TTL and refresh more
  often.
- **One secret.** Rotating `jwt.secret` invalidates every token signed with the
  old value. Restart every instance with the new secret at the same time, or add
  a key ring and a `kid`-aware key function before rotating gradually.
- **Do not put secrets in claims.** A JWT is signed, not encrypted; anyone who
  receives it can read its payload. Put identifiers and authorization facts
  there, never passwords, API keys or private data.
- **Transport.** Bearer tokens are credentials. Serve the application over HTTPS
  and never accept a token from a query string or a log; `TokenFromRequest` reads
  only the `Authorization` header.
- **Clock skew.** Keep `leeway_seconds` small. It is a tolerance, not a security
  control.

For asymmetric signing, multiple issuers or a key ring, replace `Manager` with a
key-function-based verifier rather than weakening these checks.
