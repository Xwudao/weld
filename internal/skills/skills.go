// Package skills renders the per-project agent skill file and plans its safe,
// idempotent write.
//
// `weld skills` explains a generated project to a coding agent: which
// capabilities are installed, what "installed" does and does not mean, and
// which files weld generates. The document is derived from weld.json and the
// capability catalog only, never from project source, so a local credential can
// never reach it.
package skills

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Xwudao/weld/internal/project"
	"github.com/Xwudao/weld/internal/template"
)

// Path is the project-relative path of the generated skill file.
const Path = ".agents/skills/weld/SKILL.md"

// The generated file ends with a trailer line recording the fingerprint of the
// body above it. The fingerprint lets a later run tell weld output from a file
// the user edited, so an edit is never overwritten silently.
const (
	trailerPrefix = "<!-- weld:skills sha256="
	trailerSuffix = " -->"
)

// Catalog supplies the catalog description of an installed capability. It is
// satisfied by *template.Catalog; tests inject a fake so a future capability
// can be exercised without shipping it in the embedded template.
type Catalog interface {
	Get(name string) (*template.Capability, error)
}

// Result is a planned skill file.
type Result struct {
	// Path is the project-relative skill path.
	Path string
	// Content is the bytes to write.
	Content []byte
	// Existing reports that the file already existed and was not user-edited.
	Existing bool
	// Changed reports that applying the plan would change the file.
	Changed bool

	// original is the file content read when the plan was built, and
	// originalExisted reports whether it existed. Apply re-reads the file and
	// refuses when either changed, so a user edit made between planning and
	// application is never overwritten silently.
	original        []byte
	originalExisted bool
}

// Generate plans the skill file for a project rooted at root.
//
// A file weld did not write, or one the user edited after it was generated, is
// a conflict rather than something to overwrite, so a user's own skill notes
// are never lost.
func Generate(root string, manifest *project.Manifest, catalog Catalog) (*Result, error) {
	content, err := Render(manifest, catalog)
	if err != nil {
		return nil, err
	}
	existing, readErr := os.ReadFile(filepath.Join(root, Path))
	switch {
	case errors.Is(readErr, os.ErrNotExist):
		return &Result{Path: Path, Content: content, Changed: true}, nil
	case readErr != nil:
		return nil, readErr
	}
	if Edited(existing) {
		return nil, &project.ConflictError{
			Path:   Path,
			Reason: "was edited since it was generated; refusing to overwrite (delete or rename it to regenerate)",
		}
	}
	return &Result{
		Path:            Path,
		Content:         content,
		Existing:        true,
		Changed:         !bytes.Equal(existing, content),
		original:        existing,
		originalExisted: true,
	}, nil
}

// Apply writes the planned file when it changed and does nothing otherwise, so
// a repeat `weld skills` leaves the file untouched.
//
// It re-reads the file first and refuses when it no longer matches the state the
// plan was built against, so a user edit made between planning and application
// is a conflict rather than a silent overwrite.
func (r *Result) Apply(root string) error {
	if !r.Changed {
		return nil
	}
	if err := r.recheck(root); err != nil {
		return err
	}
	return project.Apply(root, []project.Operation{{Path: r.Path, Content: r.Content, Overwrite: true}})
}

// recheck re-reads the on-disk file and reports a conflict when it differs from
// the state stored in the plan, so a concurrent edit is never overwritten.
func (r *Result) recheck(root string) error {
	current, err := os.ReadFile(filepath.Join(root, r.Path))
	switch {
	case errors.Is(err, os.ErrNotExist):
		if r.originalExisted {
			return &project.ConflictError{Path: r.Path, Reason: "was removed since the plan was generated; refusing to overwrite"}
		}
		return nil
	case err != nil:
		return err
	}
	if !r.originalExisted {
		return &project.ConflictError{Path: r.Path, Reason: "appeared since the plan was generated; refusing to overwrite"}
	}
	if !bytes.Equal(current, r.original) {
		return &project.ConflictError{Path: r.Path, Reason: "was edited since the plan was generated; refusing to overwrite (delete or rename it to regenerate)"}
	}
	return nil
}

// Edited reports whether content is not a weld-generated document, or is one
// that was modified after generation. An unrecognized file is treated as edited
// so it is never overwritten.
func Edited(content []byte) bool {
	body, hash, ok := splitTrailer(content)
	if !ok {
		return true
	}
	return fingerprint(body) != hash
}

// splitTrailer separates the generated body from its trailer fingerprint. It
// reports false when content does not end with a well-formed trailer.
func splitTrailer(content []byte) (body []byte, hash string, ok bool) {
	s := string(content)
	index := strings.LastIndex(s, trailerPrefix)
	if index < 0 {
		return nil, "", false
	}
	rest := s[index+len(trailerPrefix):]
	end := strings.Index(rest, trailerSuffix)
	if end < 0 || rest[end+len(trailerSuffix):] != "\n" {
		return nil, "", false
	}
	return []byte(s[:index]), rest[:end], true
}

func fingerprint(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

// stamp appends the trailer recording the fingerprint of body.
func stamp(body []byte) []byte {
	out := make([]byte, 0, len(body)+len(trailerPrefix)+64+len(trailerSuffix)+1)
	out = append(out, body...)
	out = append(out, trailerPrefix...)
	out = append(out, fingerprint(body)...)
	out = append(out, trailerSuffix...)
	out = append(out, '\n')
	return out
}

// installedCapability is one capability recorded in the manifest.
type installedCapability struct {
	name    string
	version string
}

// installedCapabilities returns the base and added capabilities in lexical
// order, so the document is independent of install order and deterministic.
func installedCapabilities(manifest *project.Manifest) []installedCapability {
	versions := map[string]string{}
	if manifest.Base.Name != "" {
		versions[manifest.Base.Name] = manifest.Base.Version
	}
	for _, capability := range manifest.Capabilities {
		versions[capability.Name] = capability.Version
	}
	caps := make([]installedCapability, 0, len(versions))
	for name, version := range versions {
		caps = append(caps, installedCapability{name: name, version: version})
	}
	sort.Slice(caps, func(i, j int) bool { return caps[i].name < caps[j].name })
	return caps
}

// Render builds the skill document for the capabilities recorded in manifest.
func Render(manifest *project.Manifest, catalog Catalog) ([]byte, error) {
	if manifest == nil {
		return nil, errors.New("skills: manifest is required")
	}
	caps := installedCapabilities(manifest)
	set := make(map[string]bool, len(caps))
	for _, capability := range caps {
		set[capability.name] = true
	}

	var b strings.Builder
	writeFrontmatter(&b)
	writeHeader(&b, manifest)
	writeCapabilities(&b, caps, catalog)
	writeWiring(&b, set)
	writeModules(&b, manifest)
	writeCommands(&b, manifest)
	writeEditing(&b, set)
	writeFileIndex(&b, manifest)
	writeRegenerate(&b)
	return stamp([]byte(b.String())), nil
}

func writeFrontmatter(b *strings.Builder) {
	b.WriteString("---\n")
	b.WriteString("name: weld\n")
	b.WriteString("description: Project-specific guide to the weld capabilities installed in this repository.\n")
	b.WriteString("---\n\n")
}

func writeHeader(b *strings.Builder, manifest *project.Manifest) {
	fmt.Fprintf(b, "# weld project skill: %s\n\n", manifest.Name)
	b.WriteString("This project is a Go application scaffolded with [weld](https://github.com/Xwudao/weld).\n")
	if manifest.CreatedWith != "" {
		fmt.Fprintf(b, "It was created with weld %s and its Go module is `%s`.\n\n", manifest.CreatedWith, manifest.Module)
	} else {
		fmt.Fprintf(b, "Its Go module is `%s`.\n\n", manifest.Module)
	}
	b.WriteString("`weld new` creates a minimal Go CLI and `weld add <capability>` extends that\n")
	b.WriteString("same project one capability at a time. Capabilities are additive, resolved by\n")
	b.WriteString("dependency, and never removed.\n\n")
}

// writeCapabilities lists every installed capability with a description. A
// capability this weld build does not curate falls back to its catalog summary,
// and one the catalog does not carry at all is described generically rather than
// invented.
func writeCapabilities(b *strings.Builder, caps []installedCapability, catalog Catalog) {
	b.WriteString("## Installed capabilities\n\n")
	for _, capability := range caps {
		version := capability.version
		if version == "" {
			version = "unknown"
		}
		fmt.Fprintf(b, "- **%s** v%s — %s\n", capability.name, version, describe(capability.name, catalog))
	}
	b.WriteString("\n")
}

func describe(name string, catalog Catalog) string {
	if note, ok := curatedNotes[name]; ok {
		return note
	}
	if catalog != nil {
		if capability, err := catalog.Get(name); err == nil && capability.Summary != "" {
			return capability.Summary
		}
	}
	return "capability recorded in `weld.json`; this weld build has no description for it."
}

// curatedNotes is the accurate, project-level description of the bundled
// capabilities. It is a map rather than a switch so a future capability falls
// through to its catalog summary instead of silently vanishing.
var curatedNotes = map[string]string{
	"base":    "minimal Go CLI on a Cobra command tree: a root-command contribution seam in `internal/app` (`RegisterCommand`, `ConfigureRoot`, `SetDefaultRun`) and a `log/slog` factory in `internal/logging`. A bare invocation prints help until a capability installs a long-running root action. It ships no configuration and no server.",
	"config":  "shared typed configuration in `internal/config`: `config.yml` from the working directory with flag > environment > file > defaults precedence. `config.yml` is local and git-ignored and `config.example.yml` is committed. `config.Secret` redacts itself in `fmt`, `log/slog`, JSON and YAML.",
	"http":    "HTTP server lifecycle in `internal/httpserver` and the `serve` command in `internal/app/serve.go`. Routes are composed explicitly with `httpx.NewRouter(mux)` and its typed helpers (`Get`, `Post`, `NoInput`, ...) or a raw `Router.Get`/`Router.HandleFunc`; there is no global route registry. The shared HTTP toolkit is `internal/httpx`: the `{code,msg,data}` JSON response envelope with raw writers for responses that must stay unwrapped, a router with grouping, per-group middleware and named route policies, typed handlers, query/path and JSON binding with a body limit, automatic `Validate() error` on a request DTO, `*HTTPError` status mapping, and the composable middleware (`RequestID`, `AccessLog`, `Recover`). The global middleware stack and the named policies are declared in the stable, project-owned `internal/httpserver/middleware.go` and `internal/httpserver/policy.go`; `internal/httpserver.APIPrefix` is the mount prefix, so versioning is one line. The router records every route it registers (`Routes`, `LogRoutes`, `PrintRoutes`), and the generated composition logs the table with each route's policies at startup when `environment` is local or development, so the surface and its coverage are visible without a request. A route policy is only a name: the composition root resolves it into middleware with `httpx.Router.WithPolicy`, and the generated `AdminGuard` is a replaceable placeholder, so the project's real check (a JWT, a session or a database role lookup) is implemented there or in a package it delegates to. Once installed, a bare invocation runs the same serve lifecycle as `serve`, and `--config`/`--addr` are shared persistent root flags so both parse one definition.",
	"web":     "React + TypeScript + Vite frontend under `web/`, built into `internal/web` and served by **http** on `/`. `/api/` stays reserved: an unknown API path returns 404 instead of the HTML shell.",
	"api":     "JSON HTTP API in `internal/api`: typed, self-validating DTOs and routes declared once with the `internal/httpx` typed helpers, writing the `{code,msg,data}` envelope. The default `Service` is an in-memory development demo whose items are lost on restart; it is not persistence.",
	"db":      "PostgreSQL persistence in `internal/data`: a hand-written `Repository`/`Item` wrapper, `NewPool` and `WithTx`, over sqlc-generated `internal/data/sqlc`. Installing it connects nothing and needs no credential until you wire the repository yourself.",
	"loom":    "compile-time dependency injection in `internal/di`, automatically installed for every capability and command beyond the bare CLI. All command graphs share available config/logger and optional infrastructure providers through `commonModule`; the HTTP server graph exists only after **http** is installed. Graphs reuse each provider once per invocation and prune unused DB/Redis/mail/storage bindings. A command group resolves its graph lazily through the shared `internal/commandkit` seam, so a subcommand injects a dependency by adding a field to its `Deps` and a parameter to `NewDeps` (a provider already in `commonModule` needs no graph edit; a new one needs `loom.Provide(...)` in `internal/di/<name>_graph.go`). Loom raises the project's Go directive to 1.25.",
	"redis":   "opt-in Redis client in `internal/redisclient` built from typed configuration. Installing it connects nothing: `New` never dials or pings and nothing generated imports it, so you own the client lifecycle.",
	"cron":    "opt-in in-process scheduler in `internal/cron`: five-field specs, unique names, overlap skipping, panic recovery and graceful stop. Cron installs HTTP and follows only the Loom `serve` lifecycle, never a short command. `internal/cron/register.go` is the stable job-registration file; installing cron schedules nothing.",
	"mail":    "opt-in SMTP sender in `internal/mailsender`: one connection per `Send` with an explicit TLS policy and header-injection guards. Nothing generated imports it, so serving sends no mail; `internal/di/mail_provider.go` is the stable Loom seam.",
	"storage": "opt-in S3-compatible object storage in `internal/objectstore`: a streaming client with bounded presigned URLs. It never creates the bucket, and nothing generated imports it; `internal/di/storage_provider.go` is the stable Loom seam.",
}

func writeWiring(b *strings.Builder, set map[string]bool) {
	b.WriteString("## Installed is not the same as wired\n\n")
	b.WriteString("A capability is **installed** when weld wrote its files and recorded them in\n")
	b.WriteString("`weld.json`. Installed does not mean **wired**: the generated application only\n")
	b.WriteString("uses a capability where project code explicitly references it.\n\n")
	wrote := false
	if set["api"] {
		b.WriteString("- **api** serves an in-memory development demo (`api.NewService()`). Items are\n")
		b.WriteString("  lost on restart and no database is required; installing **db** does not switch\n")
		b.WriteString("  the service to PostgreSQL. Wire persistence in explicitly (see\n")
		b.WriteString("  `internal/api/README.md`).\n")
		wrote = true
	}
	if set["db"] {
		b.WriteString("- **db** installs a pool and repository but connects nothing. No pool is\n")
		b.WriteString("  constructed and no database credential is needed until you inject\n")
		b.WriteString("  `data.Repository` (see `internal/data/README.md`).\n")
		wrote = true
	}
	if set["redis"] {
		b.WriteString("- **redis** installs a client but never dials or pings; nothing generated\n")
		b.WriteString("  imports it, so an ordinary serve needs no Redis setting (see\n")
		b.WriteString("  `internal/redisclient/README.md`).\n")
		wrote = true
	}
	if set["mail"] {
		b.WriteString("- **mail** installs an SMTP sender but sends nothing; constructing it opens\n")
		b.WriteString("  no connection and nothing generated imports it (see\n")
		b.WriteString("  `internal/mailsender/README.md`).\n")
		wrote = true
	}
	if set["storage"] {
		b.WriteString("- **storage** installs an S3-compatible client but connects nothing and\n")
		b.WriteString("  creates no bucket; nothing generated imports it (see\n")
		b.WriteString("  `internal/objectstore/README.md`).\n")
		wrote = true
	}
	if set["cron"] {
		b.WriteString("- **cron** installs a scheduler but schedules nothing by itself: it starts and\n")
		b.WriteString("  stops with `serve` and runs only the jobs you add in\n")
		b.WriteString("  `internal/cron/register.go` (see `internal/cron/README.md`).\n")
		wrote = true
	}
	if set["loom"] {
		b.WriteString("- **loom** is installed with every non-base addition. Command graphs share\n")
		b.WriteString("  available bindings through `commonModule`; the HTTP server graph exists only\n")
		b.WriteString("  with **http**. Unused DB, Redis, mail and storage providers are pruned, and\n")
		b.WriteString("  short commands never start HTTP or cron.\n")
		wrote = true
	}
	if wrote {
		b.WriteString("\n")
	}
}

// writeModules describes the business modules installed with `weld add module`.
// They are intentionally not capabilities: weld writes them once and never
// regenerates them, so the generated guide must say so rather than imply a later
// add may rewrite them.
func writeModules(b *strings.Builder, manifest *project.Manifest) {
	if len(manifest.Modules) == 0 {
		return
	}
	b.WriteString("## Business modules\n\n")
	b.WriteString("Added with `weld add module <name>`. Unlike generated capability files,\n")
	b.WriteString("weld writes them once and never regenerates them, so they are yours to edit;\n")
	b.WriteString("the default service is an in-memory example, not persistence. Adding another\n")
	b.WriteString("capability re-renders the Loom graph around them but leaves the module\n")
	b.WriteString("packages untouched.\n\n")
	for _, module := range manifest.Modules {
		fmt.Fprintf(b, "- **%s** v%s — `internal/modules/%s`, mounted by the server graph under `httpserver.APIPrefix` (so `/api/%s` by default).\n", module.Name, module.Version, module.Name, module.Name)
	}
	b.WriteString("\n")
}

// writeCommands describes the root command groups installed with `weld add
// command` and `weld add module --command`. Like modules they are written once
// and never regenerated, so the generated guide says so.
func writeCommands(b *strings.Builder, manifest *project.Manifest) {
	if len(manifest.Commands) == 0 {
		return
	}
	b.WriteString("## Root command groups\n\n")
	b.WriteString("Added with `weld add command <name>` or `weld add module <name> --command`.\n")
	b.WriteString("Each lives in `internal/commands/<name>` and is registered through\n")
	b.WriteString("`app.RegisterCommand` in `internal/app/<name>_command.go`. weld writes them\n")
	b.WriteString("once and never regenerates them, so they are yours to edit. Each group has\n")
	b.WriteString("an editable `internal/di/<name>_graph.go` and resolves its dependencies only\n")
	b.WriteString("when a real subcommand runs; help and unrelated commands start no server. It\n")
	b.WriteString("resolves the graph lazily through the shared `internal/commandkit` seam, so a\n")
	b.WriteString("subcommand injects a dependency by adding a field to `Deps` and a parameter to\n")
	b.WriteString("`NewDeps` (a provider already in `commonModule` needs no graph edit).\n\n")
	for _, command := range manifest.Commands {
		if manifest.HasModule(command.Name) {
			fmt.Fprintf(b, "- **%s** v%s — `internal/commands/%s`, backed by the `%s` module's Service.\n", command.Name, command.Version, command.Name, command.Name)
		} else {
			fmt.Fprintf(b, "- **%s** v%s — `internal/commands/%s`, an independent command group.\n", command.Name, command.Version, command.Name)
		}
	}
	b.WriteString("\n")
}

func writeEditing(b *strings.Builder, set map[string]bool) {
	b.WriteString("## Generated and editable files\n\n")
	b.WriteString("weld records every file a capability writes in `weld.json`, with the capability\n")
	b.WriteString("and version that produced it. Those files are **generated**: re-running\n")
	b.WriteString("`weld add` can rewrite them, and `weld list` reports an edited one as\n")
	b.WriteString("`modified`. Everything not recorded in `weld.json` is yours to edit.\n\n")
	if set["loom"] {
		b.WriteString("- Regenerated on every capability change, so do not edit:\n")
		b.WriteString("  `internal/di/di.go` and `internal/di/loom_gen.go`.\n")
	}
	var seams []string
	if set["http"] {
		seams = append(seams, "`internal/httpserver/middleware.go`", "`internal/httpserver/policy.go`")
	}
	if set["api"] && set["loom"] {
		seams = append(seams, "`internal/di/api_provider.go`")
	}
	if set["redis"] && set["loom"] {
		seams = append(seams, "`internal/di/redis_provider.go`")
	}
	if set["mail"] && set["loom"] {
		seams = append(seams, "`internal/di/mail_provider.go`")
	}
	if set["storage"] && set["loom"] {
		seams = append(seams, "`internal/di/storage_provider.go`")
	}
	if set["cron"] && set["loom"] {
		seams = append(seams, "`internal/di/cron_provider.go`")
	}
	if set["cron"] {
		seams = append(seams, "`internal/cron/register.go`")
	}

	if len(seams) > 0 {
		fmt.Fprintf(b, "- Stable seams, written once and never regenerated, so your edits\n  survive later `weld add`: %s.\n", strings.Join(seams, ", "))
	}
	if set["config"] {
		b.WriteString("- `config.yml` is managed but local and git-ignored; weld appends a new\n")
		b.WriteString("  capability's section through its marker region, so your values and comments\n")
		b.WriteString("  are preserved. `environment` (overridden by `APP_ENV`) is `local`,\n")
		b.WriteString("  `development`, `test` or `production`; it gates development conveniences\n")
		b.WriteString("  only — currently logging the composed route table at startup — and carries\n")
		b.WriteString("  no security weight.\n")
	}
	b.WriteString("- This skill file is generated by `weld skills`, separately from the manifest,\n")
	b.WriteString("  and is not recorded in `weld.json`.\n\n")
}

func writeFileIndex(b *strings.Builder, manifest *project.Manifest) {
	if len(manifest.Files) == 0 {
		return
	}
	paths := make([]string, 0, len(manifest.Files))
	for _, file := range manifest.Files {
		paths = append(paths, file.Path)
	}
	sort.Strings(paths)

	b.WriteString("### Files weld manages in this project\n\n")
	b.WriteString("Every path recorded in `weld.json` (weld.json records which capability\n")
	b.WriteString("last wrote each one):\n\n")
	for _, path := range paths {
		fmt.Fprintf(b, "- `%s`\n", path)
	}
	b.WriteString("\n")
}

func writeRegenerate(b *strings.Builder) {
	b.WriteString("## Regenerating this skill\n\n")
	b.WriteString("This file is produced by `weld skills` from `weld.json`; re-run it after\n")
	b.WriteString("adding a capability:\n\n")
	b.WriteString("    weld skills [--dir .] [--dry-run]\n\n")
	b.WriteString("weld refuses to overwrite the file once it differs from the generated content,\n")
	b.WriteString("so your edits are safe. Delete or rename the file to regenerate from scratch.\n\n")
}
