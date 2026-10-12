package project

import (
	"fmt"
	"strings"
)

// markerName returns the marker a line opens or closes, when the line is a weld
// marker line for suffix ("begin" or "end").
//
// Markers use the host file's comment syntax, so "# weld:config:begin" and
// "// weld:config:begin" both parse, and a marker name may itself contain a
// colon (weld:seed:items:begin).
func markerName(line, suffix string) (string, bool) {
	trimmed := strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(line), "#/"))
	if !strings.HasPrefix(trimmed, "weld:") || !strings.HasSuffix(trimmed, ":"+suffix) {
		return "", false
	}
	name := strings.TrimSuffix(strings.TrimPrefix(trimmed, "weld:"), ":"+suffix)
	if name == "" {
		return "", false
	}
	return name, true
}

// MarkerRegions returns the body of every complete marker region in content,
// keyed by marker name, together with the markers in file order.
//
// The body is the lines strictly between the begin and end marker lines, which
// is exactly the text a splice has to carry over. A region whose begin line has
// no end line is an error rather than a silent truncation.
func MarkerRegions(content []byte) (map[string][]string, []string, error) {
	lines := strings.Split(string(content), "\n")
	regions := map[string][]string{}
	var order []string
	for i := 0; i < len(lines); i++ {
		marker, ok := markerName(lines[i], "begin")
		if !ok {
			continue
		}
		begin, end, found, closed := markerBounds(lines, marker)
		if !found {
			continue
		}
		if !closed {
			return nil, nil, fmt.Errorf("extension point %q is not closed", markerToken(marker, "begin"))
		}
		if _, seen := regions[marker]; !seen {
			order = append(order, marker)
		}
		regions[marker] = lines[begin+1 : end]
		i = end
	}
	return regions, order, nil
}

// SpliceMarkerRegions returns skeleton with the body of every marker region it
// declares replaced by current's body for the same marker.
//
// It is how `weld upgrade` moves a shared payload file forward. The newer
// payload supplies the skeleton — the part weld owns — and the live file
// supplies every region body, which is where weld's patch snippets, another
// capability's additions and the project's own settings live. A region the
// skeleton declares but current lacks keeps the skeleton's body, so a payload
// that introduces a new extension point still ships it.
//
// A region present in current but absent from skeleton is returned as an orphan
// and left out of the result: the caller decides whether dropping live content
// is safe, because only the payload's author knows whether the region was
// removed on purpose.
func SpliceMarkerRegions(skeleton, current []byte) (merged []byte, orphans []string, err error) {
	live, liveOrder, err := MarkerRegions(current)
	if err != nil {
		return nil, nil, err
	}
	lines := strings.Split(string(skeleton), "\n")
	out := make([]string, 0, len(lines))
	declared := map[string]bool{}
	for i := 0; i < len(lines); i++ {
		marker, ok := markerName(lines[i], "begin")
		if !ok {
			out = append(out, lines[i])
			continue
		}
		begin, end, found, closed := markerBounds(lines, marker)
		switch {
		case !found:
			out = append(out, lines[i])
			continue
		case !closed:
			return nil, nil, fmt.Errorf("extension point %q is not closed", markerToken(marker, "begin"))
		}
		if begin != i {
			// The line opens a marker whose begin line already appeared earlier;
			// keep it verbatim rather than splicing a region twice.
			out = append(out, lines[i])
			continue
		}
		declared[marker] = true
		out = append(out, lines[i])
		if body, ok := live[marker]; ok {
			out = append(out, body...)
		} else {
			out = append(out, lines[i+1:end]...)
		}
		out = append(out, lines[end])
		i = end
	}
	for _, marker := range liveOrder {
		if !declared[marker] {
			orphans = append(orphans, marker)
		}
	}
	return []byte(strings.Join(out, "\n")), orphans, nil
}

// SkeletonCoveredBy reports whether every non-blank line the skeleton declares
// outside a marker region already appears, trimmed, in live.
//
// It is the fallback `weld upgrade` uses for a project whose manifest predates
// trusted baselines. With no baseline, a payload skeleton the live file already
// contains means the shipped version has not moved, while a skeleton line the live
// file lacks means it has. Comparing the regions themselves would be wrong: a
// shared or project-owned file's regions hold the project's own content by design.
func SkeletonCoveredBy(skeleton, live []byte) bool {
	seen := map[string]bool{}
	for _, line := range strings.Split(string(live), "\n") {
		seen[strings.TrimSpace(line)] = true
	}
	for _, line := range outsideRegions(skeleton) {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if !seen[trimmed] {
			return false
		}
	}
	return true
}

// outsideRegions returns the skeleton's lines that are not inside a marker
// region, marker lines included.
func outsideRegions(skeleton []byte) []string {
	lines := strings.Split(string(skeleton), "\n")
	out := make([]string, 0, len(lines))
	for i := 0; i < len(lines); i++ {
		marker, ok := markerName(lines[i], "begin")
		if !ok {
			out = append(out, lines[i])
			continue
		}
		_, end, found, closed := markerBounds(lines, marker)
		if !found || !closed {
			out = append(out, lines[i])
			continue
		}
		out = append(out, lines[i], lines[end])
		i = end
	}
	return out
}
