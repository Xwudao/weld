package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestAddCrudDryRunWritesNothing proves the dry run prints the planned files and
// the post-write steps, and leaves the project untouched.
func TestAddCrudDryRunWritesNothing(t *testing.T) {
	root := t.TempDir()
	if _, err := run(t, "new", "demo", "--dir", root); err != nil {
		t.Fatalf("new: %v", err)
	}
	dir := filepath.Join(root, "demo")
	manifestBefore, err := os.ReadFile(filepath.Join(dir, "weld.json"))
	if err != nil {
		t.Fatal(err)
	}

	out, err := run(t, "add", "crud", "widget", "title:string", "stock:int", "--with-dependencies", "--dir", dir, "--dry-run")
	if err != nil {
		t.Fatalf("add crud --dry-run: %v", err)
	}
	for _, want := range []string{
		`installing required capability "db"`,
		"add db/query/widgets.sql",
		"add internal/data/widget_repo.go",
		"add internal/modules/widget/module.go",
		"dry run",
		"run the pinned sqlc generator",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("dry run output is missing %q:\n%s", want, out)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "internal", "modules", "widget")); !os.IsNotExist(err) {
		t.Fatal("dry run wrote the module")
	}
	manifestAfter, err := os.ReadFile(filepath.Join(dir, "weld.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(manifestAfter) != string(manifestBefore) {
		t.Fatal("dry run rewrote weld.json")
	}
}

// TestAddCrudValidation proves the usage, a missing requirement and a bad field
// are reported before anything is planned.
func TestAddCrudValidation(t *testing.T) {
	root := t.TempDir()
	if _, err := run(t, "new", "demo", "--dir", root); err != nil {
		t.Fatalf("new: %v", err)
	}
	dir := filepath.Join(root, "demo")
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"add", "crud"}, "usage: weld add crud"},
		{[]string{"add", "crud", "widget", "--dir", dir}, "--with-dependencies"},
		{[]string{"add", "crud", "widget", "price:money", "--with-dependencies", "--dir", dir}, `unknown type "money"`},
	} {
		if _, err := run(t, tc.args...); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%v = %v, want an error containing %q", tc.args, err, tc.want)
		}
	}
}
