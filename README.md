# weld

`weld` is a progressive Go scaffold. Start with a minimal Go CLI, then add
capabilities to the *same* project over time — `weld add web` modifies your
existing project to gain a web frontend instead of generating a second
template.

```
weld new demo          # minimal Go CLI: serve command, health endpoint, no web
cd demo
weld add web           # adds a React + TS + Vite frontend to the same project
make build             # builds the frontend, embeds it, builds the app
```

## Two tightly coupled repos

| repo | owns |
| --- | --- |
| [`../weld-template`](../weld-template) | declarative capability payloads, exposed as an embedded `io/fs.FS` |
| `weld` (this repo) | CLI: catalog loading, rendering, manifest, planning, safe apply |

`weld` imports `github.com/Xwudao/weld-template` and reads payloads through
`weldtemplate.FS()`. The embedded FS means a built `weld` binary carries its
whole scaffold — no checkout, network access, or environment variable at run
time. During development `go.mod` uses:

```
replace github.com/Xwudao/weld-template => ../weld-template
```

To release, publish `weld-template` and pin a real version, then drop the
`replace`. That module is the single, versioned seam between the two repos.

## Commands

```
weld new <name> [--module path] [--dir dir] [--dry-run]
weld add <capability> [--dir dir] [--dry-run]
weld list [--dir dir]
weld version
weld help
```

Capabilities:

- `base` (scaffold) — minimal Go CLI with an HTTP `serve` command.
- `web` (add) — React + TypeScript + Vite frontend embedded into the app.

## Staged architecture

```
new ──▶ base project ──▶ add capability ──▶ add capability ──▶ …
         (go.mod, main.go,   (web/, embedded    (future: api)
          internal/…, Makefile) assets, Makefile)
```

Every file `weld` writes is recorded in `weld.json` with the capability and
version that produced it. Capabilities are **one-way additive**: later
capabilities extend the project; nothing removes a capability.

## How `weld add web` integrates (owned extension points, not replacement)

`web` never performs arbitrary text substitution. It uses three explicit,
base-owned extension points:

1. **`Makefile` marker** — `build` already depends on an empty `web-build`
   target. `web` fills the `# weld:web:begin` / `# weld:web:end` region with
   the `npm` build commands, so the target definition is never edited.
2. **`.gitignore` marker** — a second marker region adds web-specific ignores.
3. **Go asset extension point** — `internal/server` exposes
   `RegisterAssets(func() fs.FS)`. `web` adds a purely additive
   `internal/server/assets_web.go` (plus `dist/.gitkeep`) that embeds the Vite
   output with `//go:embed all:dist` and registers it. The base app compiles and
   runs with no frontend at all.

## Safety model

- **Plan before write.** A plan is fully built and conflict-checked before any
  byte is written, which is what makes `--dry-run` truthful.
- **Conflict detection.** Refuses to clobber unmanaged files, missing extension
  points, or a non-project directory.
- **Idempotent.** Re-running `weld add web` is a no-op that reports
  `already installed` and flags any managed files that have drifted.
- **Safe failure.** `Apply` rolls back every created and patched file if any
  step fails, so a project is never left half-updated.
- **Preserves user files.** Existing files are only rewritten through a verified
  marker region; everything else is additive.

## Roadmap

The next capability is `weld add api`: HTTP contract, `go-validate` rule
metadata, and OpenAPI. It belongs to a later slice and is deliberately **not**
faked here — no arbitrary struct tags, no stub OpenAPI. The first slice proves
the minimal → web path end to end.

## Tests

```sh
go test ./... -count=1
```

Coverage includes create, add, repeat (idempotency), unmanaged-file conflicts,
dry-run, no unintended edits, and a real `go build` of the generated project
before and after `add web`.

## Layout

```
cmd/weld/            CLI entry point
internal/cli/        argument parsing and output
internal/template/   capability catalog + placeholder rendering
internal/project/    manifest, marker patching, crash-safe apply
internal/scaffold/   create/add plans
```
