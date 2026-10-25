# internal/ratelimit

A per-client request rate limit for the HTTP surface. Installing it limits
nothing: the middleware is built from `config.yml`'s `rate_limit` section by
`httpserver.RateLimitMiddleware`, and you add it to the stack yourself.

## Turn it on

`internal/httpserver/middleware.go` is yours, so weld does not edit it. Put the
limit after the default entries, so a refused request still gets the CORS
headers, a request ID and an access-log line, but never reaches a handler:

```go
func middlewareChain(cfg *config.Config, logger logging.Logger) []httpx.Middleware {
	chain := make([]httpx.Middleware, 0, 5)
	if cors := corsMiddleware(cfg); cors != nil {
		chain = append(chain, cors)
	}
	chain = append(chain,
		httpx.RequestID(),
		httpx.AccessLog(logger),
		httpx.Recover(logger),
	)
	limit, err := RateLimitMiddleware(cfg)
	if err != nil {
		panic(err) // an enabled but invalid rate_limit section fails at startup
	}
	if limit != nil {
		chain = append(chain, limit)
	}
	return chain
}
```

`rate_limit.enabled: false` (or `RATE_LIMIT_ENABLED=false`) then removes the
middleware without a code change.

## How it limits

- **Token bucket per client.** A client may send `burst` requests at once and
  earns `requests_per_second` back. A refused request is answered `429` with the
  shared `{code, msg, data}` envelope and a `Retry-After` header in whole
  seconds.
- **The client is its address.** `ClientIP` keys a request by the connection's
  peer address. An IPv6 client is grouped by its `/64`, so rotating addresses
  inside one allocation does not mint new buckets.
- **Proxies must be named.** Behind a reverse proxy or load balancer every
  request arrives from the proxy, so the whole site would share one bucket. List
  the proxy in `rate_limit.trusted_proxies` (CIDR or single address). Only then
  is `X-Forwarded-For` read, and the client is the right-most address that is
  not itself a trusted proxy: anything to its left was written by the client and
  could be forged. With an empty list the header is ignored.
- **Memory is bounded.** A bucket idle long enough to be full again is
  forgotten, and at most `max_clients` clients are tracked. A new client
  arriving at the cap is refused rather than admitted unlimited.
- **It is per process.** Each instance of a scaled-out service keeps its own
  buckets, so N instances allow roughly N times the configured rate. A shared
  limit needs a shared store (for example Redis), which this package does not
  provide.

## Other keys and per-route limits

`Middleware(limiter, key)` takes any `KeyFunc`. Key by a user ID from a validated
token, or by an API key, by writing your own; return `""` to exempt a request
(a health check, an internal caller). For a limit on some routes only, build a
`Limiter` with `New` and register `Middleware(limiter, key)` as a named policy in
`internal/di/http_policies.go`.

## Configuration

| Setting | Environment | Default |
|---|---|---|
| `rate_limit.enabled` | `RATE_LIMIT_ENABLED` | `true` in the shipped `config.yml` |
| `rate_limit.requests_per_second` | `RATE_LIMIT_REQUESTS_PER_SECOND` | `10` |
| `rate_limit.burst` | `RATE_LIMIT_BURST` | `20` |
| `rate_limit.max_clients` | `RATE_LIMIT_MAX_CLIENTS` | `100000` |
| `rate_limit.trusted_proxies` | `RATE_LIMIT_TRUSTED_PROXIES` (comma-separated) | empty |

An enabled section with a non-positive rate, a burst or client cap below one,
or an unparseable proxy entry is an error when the middleware is built.
