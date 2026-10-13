# Typed HTTP client preview

`weld-api.json` was exported from a generated `widget` module. The
`producerclient/` package is actual weld-generated output, not a handwritten SDK.
It uses only the standard library and has no imports from the producer.

## Try two projects

Build weld from the repository root:

```sh
go build -o /tmp/weld ./cmd/weld

/tmp/weld new producer --module example.com/producer --dir /tmp
/tmp/weld add module widget --with-dependencies --dir /tmp/producer
/tmp/weld http export --dir /tmp/producer

/tmp/weld new consumer --module example.com/consumer --dir /tmp
/tmp/weld client add producer --dir /tmp/consumer --from ../producer/weld-api.json
```

In the consumer:

```go
import (
	"context"
	"example.com/consumer/internal/clients/producer"
)

client, err := producer.New("http://localhost:8080")
if err != nil {
	return err
}
response, err := client.WidgetGreet(context.Background(), producer.WidgetRequest{
	Name: "Ada",
})
```

Start the producer in another terminal:

```sh
cd /tmp/producer
go run . serve
```

The greeting is public. `WidgetCreateItem` is protected: the default admin policy
**denies every request** until you wire real authentication and authorization.
`WithBearerToken` only sends a header; it does not bypass the server's policy.

## What the output looks like

```go
func (c *Client) WidgetGreet(ctx context.Context, request WidgetRequest) (WidgetResponse, error)
func (c *Client) WidgetCreateItem(ctx context.Context, request WidgetCreateRequest) (WidgetItem, error)
func (c *Client) WidgetIndex(ctx context.Context) (WidgetResponse, error)
```

Wire DTOs are copied with their JSON/query/path tags; service dependencies and
validation methods are not copied. Non-success responses return `*APIError`,
which supports `errors.As`. The client unwraps `{code,msg,data}` automatically.

## Regenerate this checked-in sample

From the weld repository root:

```sh
go run ./cmd/weld client add producerclient \
  --from examples/http-client/weld-api.json \
  --out examples/http-client/producerclient
```

## V1 boundaries

- Only typed routes declared directly in installed modules' `Register` functions.
- Static paths, options, Group/Require and simple ModulePrefix expressions.
- Scalars, structs, pointers, slices, string-keyed maps and `time.Time`.
- Raw endpoints are reported as skipped. Dynamic route helpers/control flow,
  wildcard paths, embedded/interface/generic DTOs and custom serialization are
  rejected instead of guessed.
- This is a snapshot. Re-export with `http export --force` after API changes,
  then repeat `client add`. No OpenAPI, service discovery, automatic retry,
  version negotiation or API compatibility analysis.

The test suite also verifies generated clients against an actual generated
producer's typed handlers, with the client in a separate Go module.
