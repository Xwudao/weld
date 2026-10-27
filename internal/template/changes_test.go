package template

import (
	"strings"
	"testing"
	"testing/fstest"
)

func changelogCatalog(changes string) *Catalog {
	descriptor := `{"name":"cap","version":"0.3.0","kind":"add","files":[],"changes":` + changes + `}`
	return &Catalog{root: fstest.MapFS{
		"capabilities/cap/capability.json": &fstest.MapFile{Data: []byte(descriptor)},
	}}
}

func TestChangesSinceReturnsNewerEntriesOldestFirst(t *testing.T) {
	t.Parallel()
	capability, err := changelogCatalog(`[
		{"version":"0.3.0","summary":"three","actions":["do it"]},
		{"version":"0.1.0","summary":"one"},
		{"version":"0.2.0","summary":"two"}
	]`).Get("cap")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	var got []string
	for _, change := range capability.ChangesSince("0.1.0") {
		got = append(got, change.Version)
	}
	if strings.Join(got, ",") != "0.2.0,0.3.0" {
		t.Fatalf("ChangesSince(0.1.0) = %v", got)
	}
	if all := capability.ChangesSince(""); len(all) != 3 || all[0].Version != "0.1.0" {
		t.Fatalf("ChangesSince(\"\") = %+v", all)
	}
	if none := capability.ChangesSince("0.3.0"); len(none) != 0 {
		t.Fatalf("ChangesSince(current) = %+v", none)
	}
}

func TestChangesEntriesAreValidated(t *testing.T) {
	t.Parallel()
	for name, changes := range map[string]string{
		"no summary":  `[{"version":"0.2.0"}]`,
		"bad version": `[{"version":"next","summary":"x"}]`,
		"from future": `[{"version":"0.4.0","summary":"x"}]`,
	} {
		if _, err := changelogCatalog(changes).Get("cap"); err == nil {
			t.Errorf("%s: descriptor accepted", name)
		}
	}
}

// TestEmbeddedChangelogsAreValid keeps every shipped changes entry loadable.
func TestEmbeddedChangelogsAreValid(t *testing.T) {
	t.Parallel()
	catalog := Load()
	names, err := catalog.Names()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		if _, err := catalog.Get(name); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}
