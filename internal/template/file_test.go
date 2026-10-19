package template

import (
	"strings"
	"testing"
	"testing/fstest"
)

func TestFileProjectPath(t *testing.T) {
	t.Parallel()
	static := File{Path: "db/query/items.sql"}
	if got := static.ProjectPath("20240102030405"); got != "db/query/items.sql" {
		t.Errorf("static ProjectPath = %q, want the declared path", got)
	}
	versioned := File{Path: "db/migrations/create_users.sql", Versioned: true}
	if got, want := versioned.ProjectPath("20240102030405"), "db/migrations/20240102030405_create_users.sql"; got != want {
		t.Errorf("versioned ProjectPath = %q, want %q", got, want)
	}
}

func TestFileShipsPath(t *testing.T) {
	t.Parallel()
	versioned := File{Path: "db/migrations/create_users.sql", Versioned: true}
	for _, tc := range []struct {
		target string
		want   bool
	}{
		{"db/migrations/20240102030405_create_users.sql", true},
		{"db/migrations/000002_create_users.sql", true},
		{"db/migrations/20240102030405_create_orders.sql", false},
		{"db/query/20240102030405_create_users.sql", false},
		{"db/migrations/create_users.sql", false},
		{"db/migrations/notaversion_create_users.sql", false},
	} {
		if got := versioned.ShipsPath(tc.target); got != tc.want {
			t.Errorf("ShipsPath(%q) = %v, want %v", tc.target, got, tc.want)
		}
	}
	static := File{Path: "db/query/items.sql"}
	if !static.ShipsPath("db/query/items.sql") || static.ShipsPath("db/query/other.sql") {
		t.Error("a static file must match only its declared path")
	}
}

func TestVersionedFileValidationRejectsUnusableDeclarations(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name        string
		kind        string
		file        string
		wantMessage string
	}{
		{"base", "base", `{"path":"db/migrations/create_items.sql","source":"files/m.sql","versioned":true}`, "a base capability cannot ship a versioned file"},
		{"prefix", "add", `{"path":"db/migrations/000001_create_items.sql","source":"files/m.sql","versioned":true}`, "must not carry a version prefix"},
		{"not-sql", "add", `{"path":"db/migrations/create_items.txt","source":"files/m.sql","versioned":true}`, "must be a .sql file inside a directory"},
		{"no-directory", "add", `{"path":"create_items.sql","source":"files/m.sql","versioned":true}`, "must be a .sql file inside a directory"},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			descriptor := `{"name":"cap","version":"0.1.0","kind":"` + tc.kind + `","files":[` + tc.file + `]}`
			catalog := &Catalog{root: fstest.MapFS{
				"capabilities/cap/capability.json": &fstest.MapFile{Data: []byte(descriptor)},
				"capabilities/cap/files/m.sql":     &fstest.MapFile{Data: []byte("-- +goose Up\n")},
			}}
			_, err := catalog.Get("cap")
			if err == nil || !strings.Contains(err.Error(), tc.wantMessage) {
				t.Fatalf("Get error = %v, want one mentioning %q", err, tc.wantMessage)
			}
		})
	}
}
