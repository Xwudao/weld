# Loom integration for the storage capability

The Loom wiring for storage is one stable file and one pruned provider:

1. `internal/di/storage_provider.go` is written by the `storage` capability,
   whose `capabilities/storage/capability.json` declares it with
   `when: ["loom"]`. It is a stable seam weld writes once and never regenerates,
   so user wiring there survives later adds. The generated graph declares
   `NewObjectStore` as an available binding.

2. `capabilities/loom/files/di.go.tmpl` declares `loom.Provide(NewObjectStore)`
   inside a `{{- if .Caps.Has "storage"}}` block. Nothing in the default graph
   depends on `*objectstore.Store`, so Loom prunes the provider: an ordinary
   serve constructs no store and needs no storage setting. To use it, make a
   provider the graph already consumes depend on `*objectstore.Store`.

Because the graph is regenerated on every capability change, no manual edit to
`internal/di/di.go` is ever needed, and `storage_provider.go` is never
regenerated, so user wiring there survives later adds.
