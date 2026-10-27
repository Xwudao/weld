package project

import "strings"

// Conflict markers a three-way merge writes around a block both sides changed.
// They follow git's diff3 layout, so an editor or an agent recognises them, and
// the labels say which side is the project's and which is weld's.
const (
	ConflictOurs   = "<<<<<<< yours"
	ConflictBase   = "||||||| weld baseline"
	ConflictSplit  = "======="
	ConflictTheirs = ">>>>>>> weld"
)

// MergeResult is a line-based three-way merge of a project file.
type MergeResult struct {
	// Content is the merged file. A block both sides changed differently is
	// written between conflict markers holding all three versions.
	Content []byte
	// Conflicts counts the conflicting blocks; zero is a clean merge.
	Conflicts int
}

// Merge3 merges the change from base to theirs into ours, line by line.
//
// It is how `weld upgrade` hands a project-owned file forward: base is what weld
// wrote (or last accepted), ours is the project's file, theirs is what this weld
// ships. A block only one side changed takes that side; a block both changed
// identically takes it once; anything else is a conflict. The result is advisory
// — weld never writes a seam — so it only has to be a faithful starting point.
//
// It reports false when the inputs are too large to diff cheaply, matching
// UnifiedDiff's bound.
func Merge3(base, ours, theirs []byte) (MergeResult, bool) {
	o, a, b := splitLines(base), splitLines(ours), splitLines(theirs)
	if len(o)*len(a) > maxDiffCells || len(o)*len(b) > maxDiffCells {
		return MergeResult{}, false
	}
	toA, toB := matches(o, a), matches(o, b)

	var out []string
	conflicts := 0
	io, ia, ib := 0, 0, 0
	for {
		// A stable line is one base, ours and theirs all keep at the current
		// position; copy the run of them.
		for io < len(o) && toA[io] == ia && toB[io] == ib {
			out = append(out, o[io])
			io, ia, ib = io+1, ia+1, ib+1
		}
		// The next stable line ends an unstable block: base lines between here
		// and there were changed by at least one side.
		next := io
		for next < len(o) && (toA[next] < 0 || toB[next] < 0) {
			next++
		}
		endA, endB := len(a), len(b)
		if next < len(o) {
			endA, endB = toA[next], toB[next]
		}
		chunkO, chunkA, chunkB := o[io:next], a[ia:endA], b[ib:endB]
		if len(chunkO) > 0 || len(chunkA) > 0 || len(chunkB) > 0 {
			switch {
			case equalLines(chunkA, chunkO):
				out = append(out, chunkB...)
			case equalLines(chunkB, chunkO), equalLines(chunkA, chunkB):
				out = append(out, chunkA...)
			default:
				conflicts++
				out = append(out, ConflictOurs)
				out = append(out, chunkA...)
				out = append(out, ConflictBase)
				out = append(out, chunkO...)
				out = append(out, ConflictSplit)
				out = append(out, chunkB...)
				out = append(out, ConflictTheirs)
			}
		}
		if next >= len(o) {
			break
		}
		io, ia, ib = next, endA, endB
	}
	content := ""
	if len(out) > 0 {
		content = strings.Join(out, "\n") + "\n"
	}
	return MergeResult{Content: []byte(content), Conflicts: conflicts}, true
}

// HasConflictMarkers reports whether content still carries a merge conflict, so
// weld can refuse to accept a file an agent has not finished merging.
func HasConflictMarkers(content []byte) bool {
	for _, line := range splitLines(content) {
		if strings.HasPrefix(line, ConflictOurs) || strings.HasPrefix(line, ConflictTheirs) {
			return true
		}
	}
	return false
}

// matches maps every line of before to the line of after the longest common
// subsequence pairs it with, or -1 when the line was removed. The mapping is
// strictly increasing, which is what lets Merge3 walk both sides in step.
func matches(before, after []string) []int {
	pairs := make([]int, len(before))
	i, j := 0, 0
	for _, op := range diffOps(before, after) {
		switch op.kind {
		case ' ':
			pairs[i] = j
			i++
			j++
		case '-':
			pairs[i] = -1
			i++
		case '+':
			j++
		}
	}
	return pairs
}

func equalLines(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
