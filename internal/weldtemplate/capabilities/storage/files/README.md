# internal/objectstore

The S3-compatible object-storage client of this project, built from the typed
configuration. It is opt-in and explicit: `weld add storage` installs the
client, it does not create a bucket, does not upload anything and is not
imported by the generated application, so building, testing and serving need no
object storage.

The client speaks the S3 protocol through the AWS SDK for Go v2, so it works
against Amazon S3 and any S3-compatible server (MinIO, Ceph, ...). **The bucket
must already exist**: the client never creates it.

## You own the lifecycle

```go
store, err := objectstore.New(cfg)
if err != nil {
    return err
}
defer store.Close()

etag, err := store.Upload(ctx, "reports/2026.csv", file)
body, err := store.Download(ctx, "reports/2026.csv") // caller closes body
if err != nil {
    return err
}
defer body.Close()
if err := store.Delete(ctx, "reports/2026.csv"); err != nil {
    return err
}
url, err := store.PresignGet(ctx, "reports/2026.csv", 15*time.Minute)
```

`New` validates the `storage` section and maps it onto the S3 client options. It
never touches the network: no request is sent until the first operation, so
`New` succeeds even while the object store is unreachable. `Close` releases the
store's idle HTTP connections; it deletes nothing.

The operations are streaming: `Upload` takes an `io.Reader` and `Download`
returns an `io.ReadCloser`, so a large object never has to fit in RAM, and both
take a context to cancel the transfer. `Download`'s body must be closed by the
caller. `Delete` follows S3 semantics and succeeds for a missing key.
`PresignGet` and `PresignPut` return time-limited URLs whose expiry must be
positive and no greater than `objectstore.MaxPresignExpiry`, the S3 protocol's
seven-day maximum, so a URL can never be effectively permanent.

## Configuration

The configuration lives in `internal/config` (`config.Storage`) and is read from
`config.yml` (`storage:` section) and overridden by the environment
(`STORAGE_ENDPOINT`, `STORAGE_REGION`, `STORAGE_BUCKET`,
`STORAGE_ACCESS_KEY_ID`, `STORAGE_SECRET_ACCESS_KEY`, `STORAGE_PATH_STYLE`,
`STORAGE_TLS`).

| field | default | notes |
| --- | --- | --- |
| `endpoint` | none | scheme optional; blank uses Amazon S3's default endpoint |
| `region` | `us-east-1` | required after the default |
| `bucket` | none | required; must already exist |
| `access_key_id` / `secret_access_key` | none | required; held as `config.Secret` |
| `path_style` | `false` | set `true` for most S3-compatible servers |
| `tls` | `true` | a scheme-less `endpoint` follows this; an `http://` endpoint with credentials requires an explicit `false` |

The access key and secret are `config.Secret`, which redacts itself in `fmt`,
`log/slog`, JSON and YAML, so a credential cannot reach a log record by
accident. An endpoint without a scheme is given `https`, or `http` when `tls` is
explicitly disabled. An endpoint that names `http://` explicitly is rejected
when credentials are set and `tls` is not explicitly `false`, so the secure
default cannot silently send a credential in the clear; a local plaintext server
states `tls: false`.

## The provider seam

`Store` is built from a `Provider`, not from the configuration directly:

```go
type Provider interface {
    ObjectStorageSettings(ctx context.Context) (config.Storage, error)
}
```

`objectstore.New(cfg)` uses the built-in `ConfigProvider` over the typed
configuration. `objectstore.NewWithProvider(ctx, provider)` is the seam a future
DB-backed site-config runtime configuration implements to supply settings
resolved at startup, without changing the client. The provider is resolved once,
when the `Store` is built, so a `Store` is bound to the settings it was built
with.

## With Loom

`weld add storage` writes
`internal/di/storage_provider.go`, a stable seam that declares `NewObjectStore`
as an available binding. The generated graph declares it but nothing depends on
`*objectstore.Store`, so Loom prunes it: an ordinary serve constructs no store
and needs no storage setting.

Constructing the store never connects and never creates the bucket, so building
the graph, running tests and starting the process need no object store. The
lifecycle releases the store's idle connections when it was constructed.

To use the store, make a provider the graph already consumes depend on
`*objectstore.Store`. `storage_provider.go` is written once and never
regenerated, so those edits survive every later `weld add`.

## Tests

The tests use `net/http/httptest` as an in-process S3 protocol adapter and drive
the real AWS SDK client against it, so the upload, download, delete and presign
semantics are the real request and response semantics. No external object store
and no cloud account are needed:

```sh
go test ./internal/objectstore/...
```
