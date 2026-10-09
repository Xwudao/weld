# internal/redisclient

The Redis client of this project, built from the typed configuration. It is
opt-in and explicit: `weld add redis` installs the client, it does not connect
Redis to anything. Nothing in the generated application imports this package, so
building, testing and serving need no Redis server.

## You own the lifecycle

```go
client, err := redisclient.New(cfg)
if err != nil {
    return err
}
defer client.Close()
```

`New` validates the `redis` section and maps it onto the `go-redis` client
options. It never dials and never pings: `go-redis` connects on the first
command, so `New` succeeds even while Redis is down. Call `Close` yourself when
the work is done.

The configuration lives in `internal/config` (`config.Redis`) and is read from
`config.yml` (`redis:` section) and overridden by the environment
(`REDIS_ADDR`, `REDIS_USERNAME`, `REDIS_PASSWORD`, `REDIS_DB`, `REDIS_TLS`).
The username and password are `config.Secret`, which redacts itself in `fmt`,
`log/slog`, JSON and YAML, so a credential cannot reach a log record by
accident. The address defaults to `localhost:6379`; the credentials never do.

## With Loom

`weld add redis` writes
`internal/di/redis_provider.go`, a stable seam that declares `NewRedisClient` as
an available binding. The generated graph declares it but nothing depends on
`*redis.Client`, so Loom prunes it: an ordinary serve constructs no client and
needs no Redis setting.

To use Redis, make a provider the graph already consumes depend on
`*redis.Client`. For an `api` + `loom` project, edit `internal/di/api_provider.go`
so `NewAPIService` takes `*redis.Client`; Loom then constructs `NewRedisClient`,
validates the Redis configuration and registers the cleanup that closes the
client when the lifecycle stops. `redis_provider.go` and `api_provider.go` are
written once and never regenerated, so those edits survive every later
`weld add`.

## Tests

The tests use `github.com/alicebob/miniredis/v2`, an in-process RESP server, with
the real `go-redis` client, so key, value and TTL semantics are the real command
semantics. No external Redis is needed:

```sh
go test ./internal/redisclient/...
```
