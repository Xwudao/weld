package cli

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Xwudao/weld/internal/httpcontract"
)

func TestRoutesCommand(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if _, err := run(t, "new", "demo", "--module", "example.com/demo", "--dir", root); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "demo")

	if _, err := run(t, "routes", "--dir", dir); err == nil || !strings.Contains(err.Error(), "http capability is not installed") {
		t.Fatalf("routes without http error = %v", err)
	}
	if _, err := run(t, "add", "http", "--with-dependencies", "--dir", dir); err != nil {
		t.Fatal(err)
	}
	if out, err := run(t, "routes", "--dir", dir); err != nil || !strings.Contains(out, "No HTTP routes found") {
		t.Fatalf("routes with no module: %q %v", out, err)
	}
	if _, err := run(t, "add", "module", "widget", "--dir", dir); err != nil {
		t.Fatal(err)
	}

	out, err := run(t, "routes", "--dir", dir)
	if err != nil {
		t.Fatalf("routes: %v", err)
	}
	for _, want := range []string{"METHOD", "/api/widget/admin/items", "admin", "widget_create_item", "/api/widget/health"} {
		if !strings.Contains(out, want) {
			t.Fatalf("routes output lacks %q:\n%s", want, out)
		}
	}

	out, err = run(t, "routes", "--json", "--dir", dir)
	if err != nil {
		t.Fatalf("routes --json: %v", err)
	}
	var table httpcontract.RouteTable
	if err := json.Unmarshal([]byte(out), &table); err != nil {
		t.Fatalf("routes --json is not JSON: %v\n%s", err, out)
	}
	if len(table.Routes) != 4 || len(table.Unread) != 0 {
		t.Fatalf("routes --json = %+v, want the module's four routes", table)
	}
}
