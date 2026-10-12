package project

import (
	"fmt"
	"strings"
)

// diffContext is how many unchanged lines a unified-diff hunk shows around a
// change, matching the conventional three.
const diffContext = 3

// maxDiffCells bounds the quadratic longest-common-subsequence table. A file
// pair beyond it is reported as a whole-file replacement rather than diffed, so
// a large generated file can never make an advisory expensive.
const maxDiffCells = 2_000_000

// diffOp is one line of a diff: kept (' '), removed ('-') or added ('+').
type diffOp struct {
	kind byte
	line string
}

// UnifiedDiff renders a unified diff from old to new, suitable for telling a
// user what a newer capability payload would change in a file weld refuses to
// rewrite.
//
// The diff is advisory text, not a patch weld applies: it is never parsed back,
// so it only has to be readable. Content that cannot be diffed cheaply is
// reported as a replacement.
func UnifiedDiff(path string, old, new []byte) string {
	var b strings.Builder
	fmt.Fprintf(&b, "--- a/%s\n+++ b/%s\n", path, path)
	before, after := splitLines(old), splitLines(new)
	if len(before)*len(after) > maxDiffCells {
		fmt.Fprintf(&b, "@@ file too large to diff (%d -> %d lines) @@\n", len(before), len(after))
		return b.String()
	}
	writeDiffs(&b, diffOps(before, after))
	return b.String()
}

// splitLines splits content into lines without their terminators. Empty content
// is no lines at all, not one empty line, so an added or removed file diffs as a
// whole-file change.
func splitLines(content []byte) []string {
	if len(content) == 0 {
		return nil
	}
	text := strings.TrimSuffix(string(content), "\n")
	return strings.Split(text, "\n")
}

// diffOps turns two line slices into a line-by-line diff.
//
// It computes the longest common subsequence with a suffix table, then walks the
// table to emit removals before additions, which is the conventional order for a
// replaced block.
func diffOps(before, after []string) []diffOp {
	n, m := len(before), len(after)
	width := m + 1
	// lcs[i*width+j] is the length of the longest common subsequence of
	// before[i:] and after[j:].
	lcs := make([]int32, (n+1)*width)
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if before[i] == after[j] {
				lcs[i*width+j] = lcs[(i+1)*width+j+1] + 1
				continue
			}
			if down, right := lcs[(i+1)*width+j], lcs[i*width+j+1]; down >= right {
				lcs[i*width+j] = down
			} else {
				lcs[i*width+j] = right
			}
		}
	}

	ops := make([]diffOp, 0, n+m)
	for i, j := 0, 0; i < n || j < m; {
		switch {
		case i < n && j < m && before[i] == after[j]:
			ops = append(ops, diffOp{' ', before[i]})
			i++
			j++
		case j < m && (i == n || lcs[i*width+j+1] > lcs[(i+1)*width+j]):
			ops = append(ops, diffOp{'+', after[j]})
			j++
		default:
			ops = append(ops, diffOp{'-', before[i]})
			i++
		}
	}
	return ops
}

// writeDiffs groups changed operations into hunks and renders them.
func writeDiffs(b *strings.Builder, ops []diffOp) {
	var changes []int
	for i, op := range ops {
		if op.kind != ' ' {
			changes = append(changes, i)
		}
	}
	for index := 0; index < len(changes); {
		start := changes[index] - diffContext
		if start < 0 {
			start = 0
		}
		index++
		end := changes[index-1] + 1
		// Absorb a later change whose leading context would overlap this hunk.
		for index < len(changes) && changes[index] <= end+2*diffContext {
			end = changes[index] + 1
			index++
		}
		if tail := end + diffContext; tail < len(ops) {
			end = tail
		} else {
			end = len(ops)
		}
		writeHunk(b, ops, start, end)
	}
}

// writeHunk renders ops[start:end] with its @@ header.
func writeHunk(b *strings.Builder, ops []diffOp, start, end int) {
	oldLine, newLine := 1, 1
	for _, op := range ops[:start] {
		switch op.kind {
		case ' ':
			oldLine++
			newLine++
		case '-':
			oldLine++
		case '+':
			newLine++
		}
	}
	oldCount, newCount := 0, 0
	for _, op := range ops[start:end] {
		switch op.kind {
		case ' ':
			oldCount++
			newCount++
		case '-':
			oldCount++
		case '+':
			newCount++
		}
	}
	// A hunk that adds into an empty range names the line before the insertion
	// point, which is what patch and git expect.
	if oldCount == 0 {
		oldLine--
	}
	if newCount == 0 {
		newLine--
	}
	fmt.Fprintf(b, "@@ -%d,%d +%d,%d @@\n", oldLine, oldCount, newLine, newCount)
	for _, op := range ops[start:end] {
		b.WriteByte(op.kind)
		b.WriteString(op.line)
		b.WriteByte('\n')
	}
}
