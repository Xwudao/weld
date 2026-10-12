package project

import (
	"strings"
	"testing"
)

// TestSpliceMarkerRegionsKeepsLiveRegions proves the splice moves the skeleton
// forward while every live region body survives — the mechanism `weld upgrade`
// uses to update internal/config/config.go without dropping the capabilities and
// project settings that live in its marker regions.
func TestSpliceMarkerRegionsKeepsLiveRegions(t *testing.T) {
	skeleton := "package config\n\n// the newer payload's own text\ntype CORS struct{}\n\n// weld:fields:begin\n// empty in the payload\n// weld:fields:end\n"
	current := "package config\n\ntype Config struct{}\n\n// weld:fields:begin\n// weld:redis:installed 0.1.0\ntype Redis struct{}\n// the project's own field\ntype Mine struct{}\n// weld:fields:end\n"

	merged, orphans, err := SpliceMarkerRegions([]byte(skeleton), []byte(current))
	if err != nil {
		t.Fatalf("SpliceMarkerRegions: %v", err)
	}
	if len(orphans) != 0 {
		t.Fatalf("orphans = %v, want none", orphans)
	}
	got := string(merged)
	for _, want := range []string{"the newer payload's own text", "type CORS struct{}", "weld:redis:installed 0.1.0", "type Redis struct{}", "the project's own field", "type Mine struct{}"} {
		if !strings.Contains(got, want) {
			t.Errorf("spliced file is missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "empty in the payload") {
		t.Errorf("the live region body did not replace the payload's:\n%s", got)
	}
	if strings.Contains(got, "type Config struct{}") {
		t.Errorf("the splice kept a line the newer skeleton dropped:\n%s", got)
	}
}

// TestSpliceMarkerRegionsReportsAnOrphanRegion proves a region the live file
// fills but the newer skeleton no longer declares is reported, not silently
// dropped: only the payload's author knows whether removing the region was meant
// to discard the content inside it.
func TestSpliceMarkerRegionsReportsAnOrphanRegion(t *testing.T) {
	skeleton := "package config\n"
	current := "package config\n\n// weld:fields:begin\n// live content\n// weld:fields:end\n"

	merged, orphans, err := SpliceMarkerRegions([]byte(skeleton), []byte(current))
	if err != nil {
		t.Fatalf("SpliceMarkerRegions: %v", err)
	}
	if len(orphans) != 1 || orphans[0] != "fields" {
		t.Fatalf("orphans = %v, want [fields]", orphans)
	}
	if strings.Contains(string(merged), "live content") {
		t.Fatalf("the orphaned region was kept without being declared:\n%s", merged)
	}
}

// TestSpliceMarkerRegionsShipsANewRegion proves a region the newer payload
// introduces but the live file lacks keeps the payload's body, so a new extension
// point still reaches an older project.
func TestSpliceMarkerRegionsShipsANewRegion(t *testing.T) {
	skeleton := "package config\n\n// weld:fields:begin\n// the new extension point\n// weld:fields:end\n"
	current := "package config\n"

	merged, orphans, err := SpliceMarkerRegions([]byte(skeleton), []byte(current))
	if err != nil {
		t.Fatalf("SpliceMarkerRegions: %v", err)
	}
	if len(orphans) != 0 {
		t.Fatalf("orphans = %v, want none", orphans)
	}
	if !strings.Contains(string(merged), "the new extension point") {
		t.Fatalf("the new region did not ship:\n%s", merged)
	}
}

// TestMarkerRegionsParsesBothCommentStyles proves the parser accepts the comment
// syntax of every host file (Go, YAML, Makefile) and a marker name that contains a
// colon.
func TestMarkerRegionsParsesBothCommentStyles(t *testing.T) {
	content := "# weld:config:begin\nhttp:\n  addr: \":8080\"\n# weld:config:end\n// weld:seed:items:begin\n// one\n// weld:seed:items:end\n"
	regions, order, err := MarkerRegions([]byte(content))
	if err != nil {
		t.Fatalf("MarkerRegions: %v", err)
	}
	if len(regions) != 2 || strings.Join(order, ",") != "config,seed:items" {
		t.Fatalf("regions = %v (order %v)", regions, order)
	}
	if got := strings.Join(regions["config"], "|"); got != "http:|  addr: \":8080\"" {
		t.Fatalf("config body = %q", got)
	}
}

// TestSpliceMarkerRegionsRejectsAnUnclosedRegion proves a corrupt file is an
// error rather than a silent truncation.
func TestSpliceMarkerRegionsRejectsAnUnclosedRegion(t *testing.T) {
	if _, _, err := SpliceMarkerRegions([]byte("package config\n"), []byte("// weld:fields:begin\n// never closed\n")); err == nil {
		t.Fatal("an unclosed marker region was accepted")
	}
}

// TestSkeletonCoveredByIgnoresProjectAdditions proves the legacy-manifest
// fallback does not read a project's own addition outside a marker region as a
// payload change: the upgrade then stays quiet instead of printing a diff that
// looks like weld wants to delete the project's line.
func TestSkeletonCoveredByIgnoresProjectAdditions(t *testing.T) {
	skeleton := "*.log\n/data/\n# weld:config:begin\n# weld:config:end\n"
	live := "*.log\n/data/\n/local/\n# weld:config:begin\nconfig.yml\n# weld:config:end\n"
	if !SkeletonCoveredBy([]byte(skeleton), []byte(live)) {
		t.Fatal("a project addition outside a region was read as a payload change")
	}
}

// TestSkeletonCoveredByDetectsAChangedSkeleton proves a payload line the project's
// file does not contain is reported, which is how a legacy project learns that a
// project-owned seam's shipped version moved.
func TestSkeletonCoveredByDetectsAChangedSkeleton(t *testing.T) {
	skeleton := "RequestID\nClientIP\nAccessLog\n"
	live := "RequestID\nAccessLog\n"
	if SkeletonCoveredBy([]byte(skeleton), []byte(live)) {
		t.Fatal("a missing skeleton line was not detected")
	}
}
