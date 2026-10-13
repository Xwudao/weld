# internal/cron

The project's in-process scheduler, started and stopped with `serve`.
Installing cron schedules no work. Add jobs in the stable
**`internal/di/cron_jobs.go`** file; weld never regenerates that file.

## Call a service on a schedule

A service exposes a normal method:

```go
func (s *service) Generate(ctx context.Context) error {
    // Use the service's injected repositories, publisher, clients, etc.
    return nil
}
```

Add that method to its Service interface if the constructor returns an interface.
Inject the service and register its method in the wiring function:

```go
func NewCronJobs(s *cron.Scheduler, report report.Service) (*CronJobs, error) {
    if err := s.Register("daily-report", cron.DailyAt(3, 0), report.Generate); err != nil {
        return nil, err
    }
    return &CronJobs{}, nil
}
```

Add imports as needed, then run `make generate`. The service provider must be
declared; HTTP module providers already are, and custom providers belong in
`internal/di/providers.go`.

The HTTP graph runs registration automatically before startup. There is no job
Deps bundle: dependencies belong to the service, or to parameters captured by
a closure in this wiring function. Other callers can invoke the method directly.

Jobs have the signature `func(context.Context) error`. A returned error is
logged automatically with the job name, but not retried; panics are recovered.
Return registration errors so invalid schedules fail before the server starts.

## Schedules

`Register` takes a `Schedule`. A five-field cron literal works directly
(`"0 3 * * *"`), and the helpers name the common cases:

- `DailyAt(3, 0)` — every day at 03:00
- `WeeklyOn(time.Monday, 9, 30)` — Mondays at 09:30
- `MonthlyOn(1, 0, 0)` — the first of the month at 00:00
- `Hourly()`, `Daily()`, `Weekly()`, `Monthly()` — the calendar defaults
- `Every(30*time.Second)` — a fixed interval, measured between runs

The cron helpers and expressions are wall-clock times in `config.Cron`'s
timezone; `Every` is a fixed delay measured from the previous run (the first
from `Start`), so it drifts away from clock boundaries on purpose. Use `Every`
for polling loops and the intervals cron cannot express — seconds, 90 minutes,
36 hours — and the calendar helpers, or a literal, for "at 03:00". `Every` must
be at least one second; anything smaller fails at `Register` rather than
busy-looping.

## How it follows serve

`NewScheduler` in `internal/di/cron_provider.go` builds the scheduler and
registers lifecycle hooks. `NewCronJobs` binds business methods separately.
The graph root consumes the registration result so it cannot be pruned.

The scheduler starts after the HTTP server and stops before it. Help, version
and short commands never construct or start it. Respect the job context, which
is canceled when the scheduler stops.

## What the scheduler guarantees

- **Two schedule forms.** A five-field cron expression (`minute hour day-of-month
  month day-of-week`) or a fixed interval (`Every`). The six-field seconds form
  and every `@descriptor` except the `@every` form `Every` produces are rejected,
  so a typo fails at `Register`, not silently at 3am.
- **One timezone.** Schedules are interpreted in the timezone given to
  `WithLocation`; the default is UTC, so a schedule means the same thing on every
  host. The value comes from `config.Cron` (`cron.timezone`, env
  `CRON_TIMEZONE`, default `UTC`).
- **Unique names.** `Register` refuses a duplicate name (`ErrJobExists`); change
  an existing job with `Reschedule`.
- **Dynamic registry.** `Register`, `Remove` and `Reschedule` are safe before or
  after `Start` and safe for concurrent use, so a running process can add,
  remove or reschedule jobs at run time.
- **Overlap is skipped.** If a job is still running when its next tick arrives,
  that tick is skipped and logged, never queued.
- **Panics are contained.** A panic inside a job is recovered and logged; the
  process keeps running and the job is not wedged for future ticks.
- **Graceful stop.** `Stop` cancels the jobs' context and waits for running jobs
  up to the deadline you pass, then returns.
- **Per-process only.** The scheduler holds no state outside the process and
  takes no database or Redis lock, so several replicas each run their own copy of
  every job. When a job must run on exactly one replica, elect the leader
  yourself with a database or Redis lock. The scheduler intentionally depends on
  neither, and it has no admin API and persists nothing.

## Tests

The tests never wait for a schedule to fire. A `Job` is a plain function invoked
directly, and the decorated runner is driven through the scheduler's own entry so
the overlap guard, panic recovery and graceful stop are all checked
deterministically, with no `time.Sleep`.

```sh
go test -race ./internal/cron/...
```
