// Package db embeds the project's SQL migrations so the application can apply
// them at startup.
//
// db/migrations remains the source of truth: the same files feed sqlc, the
// goose command-line tool and this binary. The embed pattern is deliberately
// limited to *.sql, so a helper file or the migration directory's own README
// never reaches goose's migration scanner. A new file written by `weld db
// create` is picked up automatically; no generated list has to be updated.
package db

import (
	"embed"
	"io/fs"
)

//go:embed migrations/*.sql
var embedded embed.FS

// Migrations is the embedded db/migrations directory, rooted at the directory
// itself so a migration file is addressed exactly as it is on disk
// (for example "20250101000000_create_items.sql").
var Migrations = mustSub(embedded, "migrations")

// mustSub roots an embedded tree at dir. The directory is fixed at build time,
// so a failure here is a broken payload rather than a runtime condition.
func mustSub(fsys fs.FS, dir string) fs.FS {
	sub, err := fs.Sub(fsys, dir)
	if err != nil {
		panic("db: embed migrations: " + err.Error())
	}
	return sub
}
