package scaffold

import (
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Xwudao/weld/internal/project"
)

const middlewarePath = "internal/httpserver/middleware.go"

// olderSeam stands in for a project an older weld scaffolded with the http
// capability: the middleware seam weld wrote then lacked the Recover entry the
// current payload ships, and the project has since added its own middleware. It
// returns what the older weld wrote, what the project has now and what this weld
// ships.
func olderSeam(t *testing.T, dir string) (older, mine, shipped string) {
	t.Helper()
	shipped = readFile(t, filepath.Join(dir, middlewarePath))
	older = strings.Replace(shipped, "\t\thttpx.Recover(logger),\n", "", 1)
	if older == shipped {
		t.Fatalf("fixture: the payload has no Recover entry:\n%s", shipped)
	}
	writeProjectFile(t, dir, middlewarePath, older)
	manifest := mustLoad(t, dir)
	manifest.SetFile(middlewarePath, "http", project.RoleSeam, []byte(older))
	writeManifestFile(t, dir, manifest)

	mine = strings.Replace(older, "\t\thttpx.RequestID(),\n", "\t\thttpx.RequestID(),\n\t\thttpx.Timeout(0),\n", 1)
	edited(t, dir, middlewarePath, mine)
	downgrade(t, dir, "http", "0.0.1")
	return older, mine, shipped
}

// historyOf is a History that knows one earlier version of one file.
func historyOf(file string, versions ...string) History {
	return func(_, requested string) ([][]byte, error) {
		if requested != file {
			return nil, nil
		}
		var contents [][]byte
		for _, version := range versions {
			contents = append(contents, []byte(version))
		}
		return contents, nil
	}
}

func findAdvisory(t *testing.T, advisories []Advisory, file, reason string) Advisory {
	t.Helper()
	for _, advisory := range advisories {
		if advisory.Path == file && advisory.Reason == reason {
			return advisory
		}
	}
	t.Fatalf("no %s advisory for %s in %+v", reason, file, advisories)
	return Advisory{}
}

// TestUpgradeSeamAdvisoryCarriesAThreeWayView proves the advisory separates
// weld's change from the project's: the upstream diff holds only the payload's
// new line, the local diff only the project's, and the merge keeps both. A
// two-way diff alone shows the project's line as a removal, which is how an
// agent following it would undo the project's edit.
func TestUpgradeSeamAdvisoryCarriesAThreeWayView(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	dir := create(t, root)
	add(t, dir, "http")
	older, mine, shipped := olderSeam(t, dir)

	result, err := Upgrade(Request{Dir: dir, Catalog: newCatalog(), History: historyOf(middlewarePath, mine, older)}, nil, false)
	if err != nil {
		t.Fatalf("Upgrade: %v", err)
	}
	advisory := findAdvisory(t, result.Advisories, middlewarePath, ReasonSeam)
	if advisory.Base != BaseFromGit || !advisory.BaseTrusted {
		t.Fatalf("base = %q (trusted %v), want a trusted git base", advisory.Base, advisory.BaseTrusted)
	}
	if !strings.Contains(advisory.UpstreamDiff, "+\t\thttpx.Recover(logger),") || strings.Contains(advisory.UpstreamDiff, "Timeout") {
		t.Errorf("upstream diff is not weld's change alone:\n%s", advisory.UpstreamDiff)
	}
	if !strings.Contains(advisory.LocalDiff, "+\t\thttpx.Timeout(0),") || strings.Contains(advisory.LocalDiff, "Recover") {
		t.Errorf("local diff is not the project's change alone:\n%s", advisory.LocalDiff)
	}
	if !strings.Contains(advisory.Diff, "-\t\thttpx.Timeout(0),") {
		t.Errorf("two-way diff should still run from the project's file to the target:\n%s", advisory.Diff)
	}
	want := strings.Replace(shipped, "\t\thttpx.RequestID(),\n", "\t\thttpx.RequestID(),\n\t\thttpx.Timeout(0),\n", 1)
	if advisory.Conflicts != 0 || advisory.Merged != want {
		t.Errorf("merged (%d conflicts) =\n%s\nwant\n%s", advisory.Conflicts, advisory.Merged, want)
	}
	if advisory.Target != shipped {
		t.Errorf("target is not what this weld ships:\n%s", advisory.Target)
	}
	if advisory.From != "0.0.1" || advisory.To != capabilityVersion(t, "http") || advisory.Pending {
		t.Errorf("version range = %s..%s (pending %v)", advisory.From, advisory.To, advisory.Pending)
	}
}

// TestUpgradeSeamAdvisoryWithoutHistoryKeepsTheTwoWayDiff proves a project with
// no recoverable base still gets a correct, if coarser, advisory.
func TestUpgradeSeamAdvisoryWithoutHistoryKeepsTheTwoWayDiff(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	dir := create(t, root)
	add(t, dir, "http")
	olderSeam(t, dir)

	advisory := findAdvisory(t, upgrade(t, dir, nil, false).Advisories, middlewarePath, ReasonSeam)
	if advisory.Base != "" || advisory.UpstreamDiff != "" || advisory.Merged != "" {
		t.Fatalf("advisory invents a base: %+v", advisory)
	}
	if advisory.Diff == "" || advisory.Target == "" {
		t.Fatalf("advisory lost its diff or target: %+v", advisory)
	}
}

// TestUpgradeKeepsReportingAPendingChangeUntilResolved is the regression guard
// for the one-shot advisory: the upgrade lands, the capability becomes current,
// and the seam change must still be reported — and fail --check — until the
// project resolves it.
func TestUpgradeKeepsReportingAPendingChangeUntilResolved(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	dir := create(t, root)
	add(t, dir, "http")
	_, _, shipped := olderSeam(t, dir)

	if err := upgrade(t, dir, nil, false).Apply(); err != nil {
		t.Fatalf("apply: %v", err)
	}
	pending := mustLoad(t, dir).Pending
	if len(pending) != 1 || pending[0].Path != middlewarePath || pending[0].Reason != ReasonSeam || pending[0].From != "0.0.1" {
		t.Fatalf("pending = %+v", pending)
	}

	again := upgrade(t, dir, nil, false)
	if len(again.Upgraded) != 0 {
		t.Fatalf("a current project upgraded again: %+v", again.Upgraded)
	}
	advisory := findAdvisory(t, again.Advisories, middlewarePath, ReasonSeam)
	if !advisory.Pending || advisory.Settled || advisory.From != "0.0.1" {
		t.Fatalf("re-reported advisory = %+v", advisory)
	}
	if again.UpToDate() {
		t.Fatal("a project with a pending change reports up to date")
	}

	// The project folds the change in by taking what weld ships.
	edited(t, dir, middlewarePath, shipped)
	advisory = findAdvisory(t, upgrade(t, dir, nil, false).Advisories, middlewarePath, ReasonSeam)
	if !advisory.Settled {
		t.Fatalf("a file equal to the target is not settled: %+v", advisory)
	}
}

// TestResolveAcceptsAFoldedSeamAndKeepsItsBaseline proves resolve moves the
// recorded baseline to the shipped version, keeps that version for the next
// three-way view when the project file differs from it, and clears the record.
func TestResolveAcceptsAFoldedSeamAndKeepsItsBaseline(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	dir := create(t, root)
	add(t, dir, "http")
	older, mine, shipped := olderSeam(t, dir)

	result, err := Upgrade(Request{Dir: dir, Catalog: newCatalog(), History: historyOf(middlewarePath, older)}, nil, false)
	if err != nil {
		t.Fatalf("Upgrade: %v", err)
	}
	merged := findAdvisory(t, result.Advisories, middlewarePath, ReasonSeam).Merged
	if err := result.Apply(); err != nil {
		t.Fatalf("apply: %v", err)
	}
	edited(t, dir, middlewarePath, merged)

	resolved, err := Resolve(Request{Dir: dir, Catalog: newCatalog()}, []string{middlewarePath}, false)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(resolved.Resolved) != 1 {
		t.Fatalf("resolved = %+v", resolved.Resolved)
	}
	kept := path.Join(BaselineDir, middlewarePath)
	plannedOperation(t, resolved.Result, kept)
	if _, err := os.Stat(filepath.Join(dir, kept)); err == nil {
		t.Fatal("planning resolve wrote the baseline copy")
	}
	if err := resolved.Apply(); err != nil {
		t.Fatalf("apply resolve: %v", err)
	}

	manifest := mustLoad(t, dir)
	if len(manifest.Pending) != 0 {
		t.Fatalf("pending after resolve = %+v", manifest.Pending)
	}
	entry, _ := manifest.FileEntry(middlewarePath)
	if entry.SHA256 != project.HashContent([]byte(shipped)) || entry.Role != project.RoleSeam {
		t.Fatalf("seam record = %+v, want the shipped version as a seam", entry)
	}
	if got := readFile(t, filepath.Join(dir, kept)); got != shipped {
		t.Fatalf("baseline copy =\n%s\nwant the shipped version", got)
	}
	if got := readFile(t, filepath.Join(dir, middlewarePath)); got != merged || got == mine {
		t.Fatalf("resolve touched the project file:\n%s", got)
	}
	// The next upgrade's base comes from the kept copy, not from history.
	base, source := recordedContent(dir, entry, []byte(merged), true, nil)
	if source != BaseFromBaseline || string(base) != shipped {
		t.Fatalf("recorded content source = %q", source)
	}
	if after := upgrade(t, dir, nil, false); !after.UpToDate() {
		t.Fatalf("project not up to date after resolve: %+v / %+v", after.Advisories, plannedPaths(after.Result))
	}
}

func TestResolveRefusesUnfinishedOrUnknownChanges(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	dir := create(t, root)
	add(t, dir, "http")
	olderSeam(t, dir)
	req := Request{Dir: dir, Catalog: newCatalog()}

	if err := upgrade(t, dir, nil, false).Apply(); err != nil {
		t.Fatalf("apply: %v", err)
	}
	// A newer weld arrives while the change is still pending: the capability
	// has to move before anything of it is accepted.
	downgrade(t, dir, "http", "0.0.2")
	if _, err := Resolve(req, []string{middlewarePath}, false); err == nil || !strings.Contains(err.Error(), "behind this weld") {
		t.Fatalf("resolve of a capability that is behind: %v", err)
	}
	downgrade(t, dir, "http", capabilityVersion(t, "http"))
	if _, err := Resolve(req, []string{"internal/httpserver/server.go"}, false); err == nil || !strings.Contains(err.Error(), "no pending upgrade change") {
		t.Fatalf("resolve of a file with nothing pending: %v", err)
	}
	if _, err := Resolve(req, nil, false); err == nil {
		t.Fatal("resolve with no paths and no --all succeeded")
	}

	conflicted, _ := project.Merge3([]byte("a\n"), []byte("mine\n"), []byte("weld\n"))
	edited(t, dir, middlewarePath, string(conflicted.Content))
	if _, err := Resolve(req, []string{middlewarePath}, false); err == nil || !strings.Contains(err.Error(), "conflict markers") {
		t.Fatalf("resolve of a file with conflict markers: %v", err)
	}
	if len(mustLoad(t, dir).Pending) != 1 {
		t.Fatal("a refused resolve dropped the pending record")
	}
}

// TestUpgradeReportsAMissingSnippetLine proves a marker region that lacks lines
// of the newer snippet is reported — snippets are never re-applied, so this is
// how a new configuration key reaches an existing project — while a value the
// project changed in a YAML region is not.
func TestUpgradeReportsAMissingSnippetLine(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	dir := create(t, root)
	add(t, dir, "http")

	const example = "config.example.yml"
	content := readFile(t, filepath.Join(dir, example))
	var kept []string
	for _, line := range strings.Split(content, "\n") {
		switch {
		case strings.Contains(line, "max_age_seconds:"):
			continue
		case strings.Contains(line, "allow_credentials:"):
			line = strings.Replace(line, "false", "true", 1)
		}
		kept = append(kept, line)
	}
	edited(t, dir, example, strings.Join(kept, "\n"))
	downgrade(t, dir, "http", "0.0.1")

	result := upgrade(t, dir, nil, false)
	advisory := findAdvisory(t, result.Advisories, example, ReasonPatch)
	if advisory.Marker != "config" || advisory.Snippet == "" {
		t.Fatalf("patch advisory = %+v", advisory)
	}
	if !strings.Contains(advisory.Detail, "max_age_seconds") || strings.Contains(advisory.Detail, "allow_credentials") {
		t.Fatalf("patch advisory detail:\n%s", advisory.Detail)
	}
	if err := result.Apply(); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if pending := mustLoad(t, dir).Pending; len(pending) != 1 || pending[0].Marker != "config" {
		t.Fatalf("pending = %+v", pending)
	}
	edited(t, dir, example, content)
	if again := findAdvisory(t, upgrade(t, dir, nil, false).Advisories, example, ReasonPatch); !again.Settled {
		t.Fatalf("a restored region is not settled: %+v", again)
	}
}

// TestUpgradeLeavesAProjectEditWhenThePayloadDidNotMove proves an edited payload
// file is not reported when this weld ships what the older one did: there is no
// change of weld's to fold in, and an advisory would only invite reverting the
// project's edit.
func TestUpgradeLeavesAProjectEditWhenThePayloadDidNotMove(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	dir := create(t, root)
	add(t, dir, "mail")

	const file = "internal/mailsender/README.md"
	edited(t, dir, file, "my own notes\n")
	downgrade(t, dir, "mail", "0.0.1")

	result := upgrade(t, dir, nil, false)
	for _, advisory := range result.Advisories {
		if advisory.Path == file {
			t.Fatalf("unchanged payload reported: %+v", advisory)
		}
	}
	for _, operation := range result.Operations {
		if operation.Path == file {
			t.Fatalf("an edited file is planned for writing: %+v", operation)
		}
	}
}
