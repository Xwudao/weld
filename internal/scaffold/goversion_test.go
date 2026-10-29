package scaffold

import (
	"strings"
	"testing"
)

// TestKeepsGoFloorNeverLowersTheDirective proves a goversion replace patch only
// ever raises the project's Go directive: a capability with an older floor
// installed after one with a newer floor leaves the region alone.
func TestKeepsGoFloorNeverLowersTheDirective(t *testing.T) {
	t.Parallel()
	goMod := func(directive string) []byte {
		return []byte("module example.com/app\n\n// weld:goversion:begin\n// floor\n" + directive + "\n// weld:goversion:end\n")
	}
	cases := []struct {
		name     string
		original []byte
		snippet  string
		keep     bool
		err      string
	}{
		{name: "raise", original: goMod("go 1.23"), snippet: "go 1.25.0\n", keep: false},
		{name: "equal", original: goMod("go 1.25.0"), snippet: "// same\ngo 1.25.0\n", keep: true},
		{name: "never lower", original: goMod("go 1.27"), snippet: "go 1.25.0\n", keep: true},
		{name: "patch level", original: goMod("go 1.25.0"), snippet: "go 1.25.1\n", keep: false},
		{name: "region without directive", original: goMod("// none"), snippet: "go 1.25.0\n", keep: false},
		{name: "no region", original: []byte("module example.com/app\n\ngo 1.23\n"), snippet: "go 1.25.0\n", keep: false},
		{name: "snippet without directive", original: goMod("go 1.23"), snippet: "// nothing\n", err: "declares no go directive"},
		{name: "unparseable directive", original: goMod("go 1.27rc1"), snippet: "go 1.25.0\n", err: "compare go directives"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			keep, err := keepsGoFloor(c.original, []byte(c.snippet))
			if c.err != "" {
				if err == nil || !strings.Contains(err.Error(), c.err) {
					t.Fatalf("err = %v, want %q", err, c.err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if keep != c.keep {
				t.Fatalf("keep = %v, want %v", keep, c.keep)
			}
		})
	}
}
