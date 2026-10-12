package project

import (
	"fmt"
	"strings"
	"testing"
)

func TestUnifiedDiffReplacesALine(t *testing.T) {
	t.Parallel()
	old := []byte("alpha\nbeta\ngamma\ndelta\n")
	new := []byte("alpha\nbeta\nGAMMA\ndelta\n")
	got := UnifiedDiff("internal/x.go", old, new)
	want := strings.Join([]string{
		"--- a/internal/x.go",
		"+++ b/internal/x.go",
		"@@ -1,4 +1,4 @@",
		" alpha",
		" beta",
		"-gamma",
		"+GAMMA",
		" delta",
		"",
	}, "\n")
	if got != want {
		t.Fatalf("UnifiedDiff =\n%s\nwant\n%s", got, want)
	}
}

func TestUnifiedDiffAddsIntoAnEmptyFile(t *testing.T) {
	t.Parallel()
	got := UnifiedDiff("new.go", nil, []byte("one\ntwo\n"))
	want := strings.Join([]string{
		"--- a/new.go",
		"+++ b/new.go",
		"@@ -0,0 +1,2 @@",
		"+one",
		"+two",
		"",
	}, "\n")
	if got != want {
		t.Fatalf("UnifiedDiff =\n%s\nwant\n%s", got, want)
	}
}

func TestUnifiedDiffSplitsDistantChangesIntoSeparateHunks(t *testing.T) {
	t.Parallel()
	lines := make([]string, 20)
	for i := range lines {
		lines[i] = "line"
	}
	// Rewrite the first and last lines, far enough apart to need two hunks.
	oldLines := append([]string(nil), lines...)
	newLines := append([]string(nil), lines...)
	oldLines[0], newLines[0] = "first", "FIRST"
	oldLines[len(lines)-1], newLines[len(lines)-1] = "last", "LAST"

	got := UnifiedDiff("big.txt", []byte(strings.Join(oldLines, "\n")+"\n"), []byte(strings.Join(newLines, "\n")+"\n"))
	if count := strings.Count(got, "@@ -"); count != 2 {
		t.Fatalf("got %d hunks, want 2:\n%s", count, got)
	}
	for _, want := range []string{"-first", "+FIRST", "-last", "+LAST"} {
		if !strings.Contains(got, want) {
			t.Errorf("diff is missing %q:\n%s", want, got)
		}
	}
}

func TestUnifiedDiffReportsAnUnchangedFileAsEmptyHunks(t *testing.T) {
	t.Parallel()
	got := UnifiedDiff("same.go", []byte("a\nb\n"), []byte("a\nb\n"))
	if strings.Contains(got, "@@") {
		t.Fatalf("identical content produced a hunk:\n%s", got)
	}
}

func TestUnifiedDiffBoundsHugeInputs(t *testing.T) {
	t.Parallel()
	// Two large files with nothing in common exceed the bounded table, so the
	// diff is reported as a replacement instead of computed.
	var before, after strings.Builder
	for i := 0; i < 1500; i++ {
		fmt.Fprintf(&before, "old-%d\n", i)
		fmt.Fprintf(&after, "new-%d\n", i)
	}
	got := UnifiedDiff("huge.go", []byte(before.String()), []byte(after.String()))
	if !strings.Contains(got, "too large to diff") {
		t.Fatalf("large input was not reported as a replacement:\n%s", got[:min(len(got), 200)])
	}
}
