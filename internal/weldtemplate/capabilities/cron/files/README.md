# internal/cron

The scheduler of this project. `weld add cron` installs an in-process scheduler
library and wires it to the `serve` command; it does **not** schedule any work.
Jobs are declared in `internal/cron/register.go`, a stable file weld writes once
and never regenerates, so the jobs you add there survive every later
`weld add`.

## Where jobs are declared

`Register` in `internal/cron/register.go` is the one place to add scheduled work:

```go
func Register(s *cron.Scheduler) error {
	if err := s.Register("nightly-report", cron.DailyAt(3, 0), func(ctx context.Context, deps *cron.Deps) {
		deps.Logger.Info("nightly report started")
		report(ctx) // ctx is canceled when the process stops
	}); err != nil {
		return err
	}
	// Every is the form for intervals cron syntax cannot say.
	return s.Register("poll", cron.Every(30*time.Second), func(ctx context.Context, deps *cron.Deps) {
		poll(ctx)
	})
}
```

The default implementation registers nothing, which is why installing the
capability schedules nothing by itself.

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

## Where dependencies come from

A job's second parameter is a `*cron.Deps` bundle, so a job reads the
application's dependencies instead of a global. The shipped bundle holds the
logger; add a field to `Deps` in `internal/cron/deps.go` (a stable seam) and
construct it in `NewCronDeps` in `internal/di/cron_provider.go` (also stable):

```go
// internal/cron/deps.go
type Deps struct {
	Logger    logging.Logger
	Publisher events.Publisher
}

// internal/di/cron_provider.go
func NewCronDeps(logger logging.Logger, publisher events.Publisher) *cron.Deps {
	return &cron.Deps{Logger: logger, Publisher: publisher}
}
```

Loom resolves each parameter of `NewCronDeps` only because the provider consumes
it, so making a job publish constructs the event dispatcher for the server graph
and nothing else. Add the field, add the parameter, and `scheduler.Register`'s
signature never changes — so adding a dependency touches neither the generated
graph nor any generated test.

## How it follows serve

The scheduler is not started by the capability and never by a short command.
`weld add cron` requires `http` (and therefore `loom`), so the server graph root
consumes `*cron.Scheduler`: `NewScheduler` in the stable
`internal/di/cron_provider.go` seam builds the scheduler and registers its
start/stop with the Loom lifecycle. `help`, `version` and every other short
command never reach it.

The provider takes the server, and Loom constructs providers in dependency order
and appends hooks as it constructs them, so the socket is bound before the
scheduler starts and the scheduler stops before the HTTP server. One process owns
one scheduler and it runs only while `serve` runs.

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
