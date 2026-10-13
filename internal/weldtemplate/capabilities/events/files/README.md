# internal/events

The project's in-process publish/subscribe dispatcher. Installing it registers
no business listener. Add listeners in **`internal/di/events_listeners.go`**,
a file weld writes once and never overwrites.

## Publish in A, consume in B

Define the shared event in a domain package, not in the dispatcher:

```go
// internal/domain/item.go
package domain

type ItemCreated struct { ID string }
```

A depends on `events.Publisher` and publishes after its transaction commits:

```go
return s.publisher.Publish(ctx, domain.ItemCreated{ID: item.ID})
```

B exposes a normal service method, which other callers can also call directly:

```go
func (s *service) HandleItemCreated(ctx context.Context, event domain.ItemCreated) error {
    return s.DoSomething(ctx, event.ID)
}
```

Add that method to B's exported Service interface if its constructor returns an
interface. Then **add B as a parameter and one On call** in the wiring file:

```go
// internal/di/events_listeners.go
func NewEventListeners(d *events.Dispatcher, b bmodule.Service) (*EventListeners, error) {
    events.On(d, b.HandleItemCreated)
    return &EventListeners{}, nil
}
```

Add the domain/service imports as needed, then run `make generate`.
B's provider must be declared in the graph; `weld add module` already declares
HTTP module services. Do not edit generated `di.go` or `loom_gen.go`.

The HTTP application runs this registration automatically **before startup**.
B may itself depend on `events.Publisher`: creation and binding are separate,
so there is no construction cycle. Sync and async publishers use the same
dispatcher. Do not register listeners in each request or service constructor.

Registration is explicit, not scanned. The event's concrete Go type is the key:
`On[ItemCreated]` receives `ItemCreated`, not `*ItemCreated`.
The older `internal/events/register.go` remains supported for low-level
listeners; keep business service imports out of that package.

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

## Optional asynchronous delivery

`weld add events` also installs `*events.Async`, for when a listener is too slow
to run in the caller's goroutine. It is opt-in and lives in the same package; the
synchronous dispatcher stays the default. `Async` wraps a `*Dispatcher` and
enqueues events onto a bounded channel, and worker goroutines call
`Dispatcher.Publish`, so listeners, `On`, ordering within a worker, panic
isolation and error logging are unchanged.

- **`Publish` blocks and applies backpressure.** It returns once the event is
  buffered, or when the caller's context ends. When the buffer is full it waits,
  which slows the caller instead of growing memory; `TryPublish` never waits and
  returns `events.ErrQueueFull` so the caller can shed the event. A caller that
  depends only on `events.Publisher` reaches that path with
  `events.TryPublish(ctx, publisher, event)`, and a `Recorder` or the
  synchronous `*Dispatcher` falls back to `Publish`.
- **Delivery is best effort and in-process.** The queue is a channel, not a
  broker: a process that exits or crashes drops whatever it has not delivered,
  and `events.ErrStopped` reports a publish after `Stop`. There is no retry and
  no cross-process delivery. A durable queue is a different capability.
- **`Stop(ctx)` drains within a deadline.** It stops accepting events, delivers
  what is buffered and waits for the workers, up to `ctx`. If the deadline
  expires it cancels the listeners' context, drops what is left and returns
  `ctx.Err()`.
- **Listeners run with the worker's context, not the request's.** A request
  context is cancelled when the response is written, so reusing it would fail
  every listener. A listener that publishes should use `TryPublish` (through the
  method or `events.TryPublish`), because a worker that blocks on a full queue
  waits for itself.
- **One worker by default.** `events.WithWorkers(n)` raises throughput for
  I/O-bound listeners but drops the one-event-at-a-time guarantee: events may be
  handled out of order and a listener must be safe to run concurrently with
  itself. `events.Stats()` reports capacity, occupancy and counts.
- **The seam chooses.** `internal/di/events_provider.go` returns the
  `events.Publisher` interface and picks the mode from its `asyncEvents` switch
  (false by default). Set it to true to deliver asynchronously; the seam then
  builds the `*events.Async` and registers the Loom lifecycle hook that starts
  and drains the workers, so the switch alone is the opt-in. A component that
  depends only on `events.Publisher` can call `events.StatsOf(publisher)` to read
  the queue statistics, and `events.TryPublish(ctx, publisher, event)` to publish
  without waiting, so the mode stays invisible to the caller.

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
that matters. The in-process asynchronous mode above decouples the caller but is
still not durable, so it does not fix this. Moving slow or unreliable work behind
a durable queue is the job of a queue capability; because callers depend on the
`events.Publisher` interface rather than the concrete type, that change stays local
to the wiring.

## How it is wired

`NewEventDispatcher` creates the Publisher as before.
`NewEventListenerDispatcher` extracts that same in-process dispatcher (including
the inner dispatcher of Async), and `NewEventListeners` binds business services
to it. The HTTP graph root consumes the registration result automatically.
Registration failures fail graph construction before lifecycle.Start.

The HTTP graph therefore constructs the publisher even with no listeners.
Synchronous delivery is still the default and starts no background worker.
Async delivery remains opt-in through `asyncEvents`.

Command and seed graphs **do not automatically consume server business
listeners**: that would construct unrelated services for short commands.
They still prune an unused Publisher. If a command needs local event consumption,
declare its consumer providers and `loom.Provide(NewEventListeners)` in its
stable `internal/di/<name>_graph.go`, then have a DI root wrapper consume
`*EventListeners` before constructing the command's dependency root. Server
services needed by that seam must also be available to that command graph.

A custom Publisher without an in-process dispatcher fails local listener
wiring explicitly; Recorder is for isolated service tests, not listener wiring.

### Existing projects

Re-add/upgrade the events capability to install the new wiring files and
regenerate the HTTP graph. Existing `events_provider.go` and `register.go`
edits are preserved. Move a business listener from register.go to the new
DI seam rather than copying it, otherwise it will execute twice.

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
