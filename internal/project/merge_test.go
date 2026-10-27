package project

import (
	"strings"
	"testing"
)

func lines(text ...string) []byte {
	return []byte(strings.Join(text, "\n") + "\n")
}

// TestMerge3KeepsBothSidesOfDisjointChanges is the case an upgrade exists for: the
// project added its own line, weld changed another, and both survive.
func TestMerge3KeepsBothSidesOfDisjointChanges(t *testing.T) {
	t.Parallel()
	base := lines("package x", "", "func chain() {", "\trequestID()", "\tlogger()", "}")
	ours := lines("package x", "", "func chain() {", "\trequestID()", "\trecover()", "\tlogger()", "}")
	theirs := lines("package x", "", "func chain() {", "\trequestID()", "\tlogger()", "\tcors()", "}")

	got, ok := Merge3(base, ours, theirs)
	if !ok {
		t.Fatal("Merge3 refused small inputs")
	}
	want := lines("package x", "", "func chain() {", "\trequestID()", "\trecover()", "\tlogger()", "\tcors()", "}")
	if got.Conflicts != 0 || string(got.Content) != string(want) {
		t.Fatalf("Merge3 = %d conflict(s)\n%s\nwant\n%s", got.Conflicts, got.Content, want)
	}
}

func TestMerge3TakesTheOnlyChangedSide(t *testing.T) {
	t.Parallel()
	base := lines("a", "b", "c")
	for name, tc := range map[string]struct{ ours, theirs, want []byte }{
		"theirs only": {base, lines("a", "B", "c"), lines("a", "B", "c")},
		"ours only":   {lines("a", "b", "c", "d"), base, lines("a", "b", "c", "d")},
		"same change": {lines("x", "b", "c"), lines("x", "b", "c"), lines("x", "b", "c")},
		"theirs drop": {base, lines("a", "c"), lines("a", "c")},
	} {
		got, _ := Merge3(base, tc.ours, tc.theirs)
		if got.Conflicts != 0 || string(got.Content) != string(tc.want) {
			t.Errorf("%s: Merge3 = %d conflict(s)\n%s\nwant\n%s", name, got.Conflicts, got.Content, tc.want)
		}
	}
}

func TestMerge3MarksAConflictWithAllThreeVersions(t *testing.T) {
	t.Parallel()
	base := lines("a", "b", "c")
	ours := lines("a", "mine", "c")
	theirs := lines("a", "weld", "c")

	got, _ := Merge3(base, ours, theirs)
	want := lines("a", ConflictOurs, "mine", ConflictBase, "b", ConflictSplit, "weld", ConflictTheirs, "c")
	if got.Conflicts != 1 || string(got.Content) != string(want) {
		t.Fatalf("Merge3 = %d conflict(s)\n%s\nwant\n%s", got.Conflicts, got.Content, want)
	}
	if !HasConflictMarkers(got.Content) {
		t.Fatal("HasConflictMarkers missed the conflict")
	}
	if HasConflictMarkers(ours) {
		t.Fatal("HasConflictMarkers flagged a clean file")
	}
}

func TestMerge3HandlesEmptySides(t *testing.T) {
	t.Parallel()
	got, _ := Merge3(nil, nil, lines("new"))
	if got.Conflicts != 0 || string(got.Content) != "new\n" {
		t.Fatalf("Merge3 into empty = %q (%d)", got.Content, got.Conflicts)
	}
	got, _ = Merge3(lines("a"), lines("a"), nil)
	if got.Conflicts != 0 || len(got.Content) != 0 {
		t.Fatalf("Merge3 delete = %q (%d)", got.Content, got.Conflicts)
	}
}
