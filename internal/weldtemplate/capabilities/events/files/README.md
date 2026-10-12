# internal/events

The event dispatcher of this project. `weld add events` installs an in-process
publish/subscribe seam; it does **not** register a single listener. Listeners are
declared in `internal/events/register.go`, a stable file weld writes once and
never regenerates, so the listeners you add there survive every later
`weld add`.

## Where listeners are declared

`Register` in `internal/events/register.go` is the one place to add them:

```go
func Register(d *events.Dispatcher) error {
	events.On(d, func(ctx context.Context, event ItemCreated) error {
		return mailer.SendWelcome(ctx, event.ID)
	})
	return nil
}
```

The default implementation registers nothing, which is why installing the
capability dispatches nothing by itself. Registration is explicit: nothing scans
the project, and the event's **concrete Go type** is the key, so `On[ItemCreated]`
receives exactly what is published as `ItemCreated`. A typo is a compile error,
not an event nobody handles.

## Delivery is synchronous, and that is the point

`Publish` runs every listener registered for the event's type, in registration
order, in the caller's goroutine, and returns only after the last one returned:

```go
if err := publisher.Publish(ctx, ItemCreated{ID: item.ID}); err != nil {
	// every listener has already run; err joins their failures
}
```

So the caller learns whether the listeners succeeded, and a test needs no worker,
no polling and no waiting. It also means:

- **A slow listener is a slow caller.** A listener that takes 300ms adds 300ms to
  the request that published the event. Keep listeners fast.
- **A listener failure does not stop the others.** Every listener runs; the
  failures are joined with `errors.Join` and returned to the publisher, which
  decides whether that is fatal.
- **A panicking listener is contained.** The panic is recovered, logged with the
  listener name, the panic value and a stack, and returned as an error. One
  broken listener never takes the process down and never wedges the rest.
- **The event value is never logged.** Log records name the event's type, not its
  value, which may carry personal data.

## What it is not

It is not a queue. There is no persistence, no retry, no backoff and no worker
process: a listener runs exactly once, in the process that published the event,
and an event published by a process that then crashes is gone. A bare
`go publish()` is not a substitute either — the request context is canceled as
soon as the response is written, so a listener that uses it fails immediately and
nothing retries.

It is also not a transaction. An event published inside a database transaction is
delivered **before** the commit, so a listener can observe a change that is later
rolled back, and a rollback cannot un-send an email. Publish after the commit when
that matters. Moving slow or unreliable work behind a durable queue is the job of
a queue capability; because callers depend on the `events.Publisher` interface
rather than the concrete type, that change stays local to the wiring.

## How it is wired

The dispatcher is an available Loom binding, not part of the default
composition. `NewEventDispatcher` in the stable `internal/di/events_provider.go`
seam builds it and runs `Register`, but nothing in the generated graph depends on
`*events.Dispatcher` or `events.Publisher`, so Loom prunes it: an application that
publishes nothing constructs no dispatcher.

To switch it on, make a provider the graph already consumes depend on
`events.Publisher` — for example edit a business module's `service.go` so its
`NewService` takes one:

```go
func NewService(publisher events.Publisher) *service { ... }
```

Loom then constructs the dispatcher, calls `Register` and injects it. A listener
that cannot register fails startup, not the first request.

## Tests

`Recorder` is a `Publisher` that records instead of delivering, so a test asserts
what a handler published with no listener and no graph:

```go
var recorder events.Recorder
service := NewService(&recorder)
// ... exercise the handler ...
if !recorder.Contains(ItemCreated{ID: "1"}) {
	t.Fatal("the handler published nothing")
}
```

```sh
go test -race ./internal/events/...
```
