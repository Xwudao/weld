# AGENTS.md

Guidance for AI coding agents working in this repository. Read it before
changing code.

- `README.md` is the user-facing manual and the deepest description of generated
  projects and their safety model.
- `internal/weldtemplate/README.md` documents payload authoring, file roles and
  the upgrade ownership rules. Read it before touching a capability payload.

## What this project is

`weld` is a progressive Go scaffold. It is two things in one binary:

1. a generator and upgrader for generated projects (`weld new`, `weld add ...`,
   `weld upgrade`, `weld doctor`, `weld skills`); and
2. a driver for an assembled project (`weld dev`, `weld build`, `weld seed`,
   `weld deploy`, `weld db`, `weld test`, `weld docker`, `weld http`,
   `weld client`).

The tool is the `github.com/Xwudao/weld` module and builds with Go 1.23. The
template payloads live under `internal/weldtemplate` and are embedded with
`//go:embed`, so template loading requires neither network access nor a template
checkout. Dependency resolution and external tools may still require network
access.

Versions are independent:

- the CLI release version is in `internal/cli/cli.go`;
- the embedded payload release version is in `internal/weldtemplate/assets.go`;
- each capability and per-name template has its own version in its descriptor
  (`capability.json`, `module.json`, `command.json`).

Scaffold-managed files in a generated project are recorded in that project's
`weld.json` with their owner, role and sha256 baseline. `weld upgrade`
decides what it may rewrite from that record plus what is on disk. A few
commands write outside the manifest by design (`weld skills`, generated HTTP
clients, and the user-owned `weld.yml`); do not casually start recording them.

## Setup and checks

```sh
go build ./...
go run ./cmd/weld --help
go run ./cmd/weld version

go test ./... -count=1
go vet ./...
gofmt -l .            # format only Go files changed by this task
```

Useful narrower loops while iterating:

```sh
go test ./internal/cli ./internal/scaffold -count=1
go test ./internal/weldtemplate ./internal/scaffold ./internal/project -count=1
go test ./internal/scaffold -run TestAddWeb -count=1
```

Choose checks based on the change:

- Documentation-only: verify affected commands, paths and descriptions; Go
  build, vet and tests are not required unless executable behavior also changes.
- CLI or driver behavior: run the affected package tests and `go vet ./...`.
- Scaffold, template, project or payload behavior: run the relevant targeted
  tests, `go vet ./...` and the full suite before handoff.
- Check formatting for code changes, but do not reformat unrelated files.

The full suite is heavy: `internal/scaffold` creates real projects in temp
directories and runs `gofmt`, `go build`, `go vet`, `go test`, `make`, and
sometimes the pinned `sqlc`/Loom generators. Some tests skip when the module
cache/network or a pinned tool is unavailable. A skip is acceptable for an
environment limitation, but it is not evidence that new behavior works — make
sure the relevant test can actually run when you touch that area.

The only database tests are opt-in: set `PG_TEST_DSN` to a dedicated, disposable
PostgreSQL database. Generated integration tests create and drop their own
schema; never point them at a live database.

Use the repository's current CI configuration, if any, together with these
local checks; do not assume CI covers a check without inspecting it.

## Layout

| Path | Responsibility |
|---|---|
| `cmd/weld/` | Process entry point; reports the returned error once with the `weld:` prefix. |
| `internal/cli/` | Cobra command tree, flag parsing, plan output and command wiring. |
| `internal/scaffold/` | Planning for new/add/upgrade/module/command/enum/seeder, dependency resolution and post-apply verification. |
| `internal/template/` | Capability catalog, descriptor loading, placeholders, module/command templates and capability-aware DI rendering. |
| `internal/weldtemplate/` | The embedded payload tree (`capabilities/`, `modules/`, `commands/`) and its version. |
| `internal/project/` | `weld.json` manifest, marker regions and splicing, unified diff, snapshots and best-effort rollback apply. |
| `internal/projectcfg/` | Loading the user-owned `weld.yml`. Separate from the machine-owned manifest. |
| `internal/hook/` | Running user-declared lifecycle hooks around build/dev/seed/deploy. |
| `internal/build/`, `internal/dev/`, `internal/deploy/`, `internal/docker/` | Drivers for the assembled project. |
| `internal/db/`, `internal/seed/`, `internal/testrun/` | Migration commands, seeded test runs and test-database resolution. |
| `internal/doctor/`, `internal/skills/`, `internal/semver/` | Read-only health checks, project skill generation and version comparison. |
| `internal/genenum/`, `internal/loomgen/` | Enum-method generation and running the pinned Loom generator. |
| `internal/httpcontract/` | Contract export and generated typed HTTP clients. |
| `examples/http-client/` | Checked-in generated client sample; regenerate it, do not hand-edit it. |

## Mental model

### Plan fully, then apply

Planning and writing are separate phases:

- `scaffold.Request` describes the action; `Result` carries complete
  `project.Operation`s, notes and the post-apply verification setting.
- A plan is conflict-checked against the project before anything is written.
  That is what makes `--dry-run` truthful.
- `Result.Apply` snapshots the targets, writes each file atomically (or
  deletes), then runs Loom generation / manifest refresh / verification, and
  rolls the plan back on failure. Rollback is best effort, not transactional.
- Any plan that introduces a compile failure must be rolled back. An
  already-broken project stays writable: only a diagnostic introduced by this
  plan is a failure.

Do not write files or invoke project generators while building a plan. Add
preconditions to the plan instead. Do not weaken the before/after verification
behavior.

### Capabilities are declarative and one-way additive

- A capability is a directory `internal/weldtemplate/capabilities/<name>/` with
  a `capability.json` and `files/` payloads. `base` creates a project; `add`
  capabilities extend it.
- `requires` is a real dependency DAG. A missing requirement is an error unless
  the caller passes `--with-dependencies` (multiple requested capabilities share
  one plan and imply it). Never install a capability the caller did not ask for
  silently.
- Installation order must not matter. A requirement that two capabilities share
  is planned once. Later `weld add` calls must not rewrite a capability's
  project-owned seams.
- Capabilities are never removed by an add. `weld upgrade --prune` deletes only
  unmodified, retired plain payload files.

### Ownership and file roles

`weld.json` records one role per managed file; the role decides what an upgrade
may do:

| Role | Upgrade behavior |
|---|---|
| `payload` (default) | Rewrite while the project has not changed it; otherwise report a diff. |
| `patched` | Render the newer payload skeleton and splice the live marker-region bodies into it. |
| `seam` | Written once, then owned by the project: **never rewritten**, only reported with a diff. |
| `generated` | Re-rendered from the installed capability set; never merged, never pruned. |

A marker patch extends a file without taking ownership of it. `go.mod` and
`go.sum` are toolchain-owned and are never rewritten by upgrade. When adding or
changing a payload, choose the role deliberately — an over-broad role destroys
project edits, an over-narrow one means payload changes never arrive.

### Payload rendering

- `template.Render` performs literal token replacement, not `text/template`
  actions: `__name__`, `__module__`, `__version__`, `__modname__`,
  `__ModName__`.
- Capability-aware DI payloads are additionally rendered as `text/template`
  against the installed capability set, then token-rendered and gofmt'ed.
- Do not introduce action syntax into payloads that are not processed as
  templates: JSON, JSX, CSS, shell and most Go payloads contain literal braces.
- Generated Go must be gofmt-clean. Generated code that carries the
  `Code generated ... DO NOT EDIT.` header belongs to a generator.

### Extension points are narrow and owned

The supported composition mechanisms in generated projects are:

1. the Cobra command tree in `internal/app` (`RegisterCommand`,
   `ConfigureRoot`, `SetDefaultRun`);
2. the regenerated Loom server graph (`internal/di`) and the shared handler
   builder;
3. marker regions in shared files (`Makefile`, `.gitignore`, `go.mod`,
   `config.yml`, `internal/config/config.go`).

There is no arbitrary text substitution. A capability extends a shared file only
through a declared marker patch, and project additions belong inside the marker
region. Keep it that way.

### Configuration boundaries

- `weld.json` is machine-owned and rewritten by weld.
- `weld.yml` is the optional, user-owned build/dev/seed/deploy configuration. It
  is never recorded in the manifest and never rewritten by `weld add`.
- `config.yml` belongs to the generated project (local, usually git-ignored);
  `config.example.yml` is committed. Never put a real credential in a payload or
  committed example.

## Common changes

### Change or add a capability

1. Edit or create `internal/weldtemplate/capabilities/<name>/capability.json`
   and its `files/` payloads.
2. **Bump that capability's `version` whenever its payload changes.** Upgrade
   gates on the recorded version; without a bump the change never reaches an
   existing project and `weld upgrade --check` will not flag it.
3. Declare `requires`, and `role` for every file whose ownership is not plain
   `payload`. Use `seam` for files the project is meant to edit permanently.
4. Add file/patch conditions with `when`/`whenAbsent` instead of branching in
   Go. Keep the catalog discovery implicit — dropping a directory in is the
   registration.
5. Update the capability README and the relevant sections of `README.md`.
6. Add or extend scaffold tests that generate real projects and run
   gofmt/build/vet/test. Cover installing requirements first and installing the
   capability with `--with-dependencies`; verify missing requirements fail
   without that flag. For capabilities that can be installed independently,
   including those sharing requirements, test both installation orders. Add an
   upgrade test when existing projects are affected.
7. Run `go test ./internal/weldtemplate ./internal/scaffold ./internal/project -count=1`
   and the remaining checks required by Setup and checks.

### Change the CLI

- Register the command in `newRootCommand` in `internal/cli/cli.go`, and put the
  implementation in a focused `internal/cli/<command>.go` file.
- Commands return errors and write normal output through
  `cmd.OutOrStdout()`. Never print an error from a library or call `os.Exit`;
  `main` reports it once.
- `--dir`, `--dry-run` and `--no-hooks` are persistent root flags. Reuse them.
- Preserve `--dry-run` semantics: print the exact plan and write nothing.
- Add CLI tests for success, validation failure, dry-run and apply. Update the
  public command list in `README.md` when user-facing behavior changes.

### Change manifest, upgrade or patch behavior

- `internal/project/manifest.go` owns the v2 baseline semantics. Version 1
  manifests must keep loading conservatively (`LegacyBaseline`); never "fix" an
  old record by refreshing hashes it does not prove.
- Hash refresh must stay scoped to files weld itself rewrote.
- Seams are never rewritten, even when the recorded hash still matches.
- Add regression tests for ownership, drift, deletion, retirement and rollback
  whenever you touch these paths. The upgrade tests in `internal/scaffold` are
  the specification.

### Change generated-project code

- Prefer updating the payload and regenerating checked-in expected output over
  hand-editing generated files.
- Keep generated tests hermetic: `net/http/httptest` or loopback listeners,
  injected fakes, no external service, no real credential.
- Keep the root module dependency set small. Tooling that a generated project
  needs (sqlc, goose, Loom) belongs in a nested `tools/` module, not in the
  application's `go.mod`.
- Regenerate the sample client in `examples/http-client/` with the documented
  `weld client add` command rather than editing it.

## Coding conventions

- Write standard Go, gofmt'ed, with a doc comment on every package and exported
  identifier. Comments in this repository explain *why*, not just *what*; match
  that style.
- Keep packages single-purpose and new dependencies flowing inward through the
  existing direction (`cli` → `scaffold` → `template`/`project` →
  `weldtemplate`, with focused driver packages beside `cli`). Avoid import
  cycles and avoid making payloads or `project` depend on `cli`.
- Standard library first. Treat the root `go.mod` as the source of truth for
  dependencies; avoid adding a dependency unless it is clearly justified.
- Wrap errors with `%w` and context (`fmt.Errorf("read %s: %w", path, err)`).
  Error strings are lower-case, actionable and name the problem, not a secret
  value.
- Inject side effects: `Runner`, `LookupEnv`, `ReadFile` and similar seams are
  how tests avoid the operating system, the network and a database. Preserve and
  use them instead of reaching for `os/exec`, `os.Getenv` or a socket directly.
- Keep output deterministic: sort names before printing or planning, do not
  invent timestamps in generated content, and make repeat runs byte-stable
  (migration filenames and manifest `appliedAt` are deliberate exceptions).
- Write files atomically with `0644` and directories with `0755`, as
  `project.Apply` does. Preserve every byte outside a marker region.
- Never log or print a dsn, password, token or raw configuration content. Use
  the generated `Secret` type where applicable, and test with sentinel values to
  prove redaction.
- Tests use the standard `testing` package (no testify), `t.Helper()` and
  `t.TempDir()`. Assert behavior and error messages that matter; avoid brittle
  whole-file golden comparisons unless byte stability is the contract.
- Keep diffs scoped. Do not reformat, rename or delete unrelated code, and do
  not revert work in progress that you did not create.

## Definition of done

- [ ] Applicable checks from Setup and checks pass; changed Go files are
      gofmt-clean. Report unrelated pre-existing formatting issues without
      rewriting them.
- [ ] At handoff, list the checks actually run and any failures or skips,
      including environment limitations. Never describe skipped or unrun checks
      as passing.
- [ ] New behavior has tests, including the negative/conflict/dry-run path.
- [ ] A payload change bumps the owning capability/template version and, when it
      affects installed projects, has an upgrade test.
- [ ] User-facing behavior is documented in `README.md` (and the capability
      README, when applicable).
- [ ] No secret, local path or machine-specific value is committed.
- [ ] No generated file was hand-edited when regeneration was possible.

## Commit style

Recent history uses Conventional Commits with an imperative, lower-case subject,
for example:

```text
feat: add user capability with bigint identity accounts
docs(commands): clarify Deps-closure pruning and cooperative teardown
refactor(http): unify server lifecycle and handler composition
```

Keep a commit focused on one change, and describe the user-visible effect rather
than the file list.
