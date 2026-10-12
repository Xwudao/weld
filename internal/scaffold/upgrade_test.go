package scaffold

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Xwudao/weld/internal/project"
)

// downgrade rewrites a recorded capability version, standing in for a project an
// older weld scaffolded.
func downgrade(t *testing.T, dir, name, version string) {
	t.Helper()
	manifest := mustLoad(t, dir)
	manifest.SetCapabilityVersion(name, version)
	writeManifestFile(t, dir, manifest)
}

func writeManifestFile(t *testing.T, dir string, manifest *project.Manifest) {
	t.Helper()
	encoded, err := manifest.Encode()
	if err != nil {
		t.Fatalf("encode manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, project.ManifestName), encoded, 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
}

// weldWrote rewrites a file and records its hash as weld's, standing in for the
// content an older payload wrote: the file differs from the newer payload while
// still matching what the manifest records.
func weldWrote(t *testing.T, dir, capability, path, content string) {
	t.Helper()
	writeProjectFile(t, dir, path, content)
	manifest := mustLoad(t, dir)
	manifest.SetFile(path, capability, project.RolePayload, []byte(content))
	writeManifestFile(t, dir, manifest)
}

// edited rewrites a file without touching the manifest, which is what a user
// edit looks like.
func edited(t *testing.T, dir, path, content string) {
	t.Helper()
	writeProjectFile(t, dir, path, content)
}

// forget drops a file's manifest record, standing in for a payload that gained
// the file after this project was scaffolded.
func forget(t *testing.T, dir, path string) {
	t.Helper()
	manifest := mustLoad(t, dir)
	manifest.RemoveFile(path)
	writeManifestFile(t, dir, manifest)
}

func writeProjectFile(t *testing.T, dir, path, content string) {
	t.Helper()
	full := filepath.Join(dir, path)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(full), err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func upgrade(t *testing.T, dir string, names []string, prune bool) *UpgradeResult {
	t.Helper()
	result, err := Upgrade(Request{Dir: dir, Catalog: newCatalog()}, names, prune)
	if err != nil {
		t.Fatalf("Upgrade: %v", err)
	}
	return result
}

func capabilityVersion(t *testing.T, name string) string {
	t.Helper()
	capability, err := newCatalog().Get(name)
	if err != nil {
		t.Fatalf("Get %s: %v", name, err)
	}
	return capability.Version
}

func plannedOperation(t *testing.T, result *Result, path string) project.Operation {
	t.Helper()
	for _, operation := range result.Operations {
		if operation.Path == path {
			return operation
		}
	}
	t.Fatalf("no planned operation for %s; planned %v", path, plannedPaths(result))
	return project.Operation{}
}

func plannedPaths(result *Result) []string {
	paths := make([]string, 0, len(result.Operations))
	for _, operation := range result.Operations {
		paths = append(paths, operation.Path)
	}
	return paths
}

// TestUpgradeIsNoOpWhenCurrent proves an upgrade of a project this weld build
// wrote plans nothing at all, so a repeat run is safe and silent.
func TestUpgradeIsNoOpWhenCurrent(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	dir := create(t, root)
	add(t, dir, "redis")

	result := upgrade(t, dir, nil, false)
	if result.Behind() {
		t.Fatalf("upgrade reported %v as behind", result.Upgraded)
	}
	if len(result.Operations) != 0 {
		t.Fatalf("upgrade planned %v", plannedPaths(result.Result))
	}
	if len(result.Advisories) != 0 {
		t.Fatalf("upgrade advised %v", result.Advisories)
	}
	if len(result.Unknown) != 0 {
		t.Fatalf("upgrade reported unknown %v", result.Unknown)
	}
	if err := result.Apply(); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if drift := mustLoad(t, dir).Drift(dir); len(drift) != 0 {
		t.Fatalf("drift after a no-op upgrade: %+v", drift)
	}
}

// TestUpgradeAddsAFileFromANewerPayload proves a file a newer payload ships that
// this project never had is written, and the recorded version moves with it.
func TestUpgradeAddsAFileFromANewerPayload(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	dir := create(t, root)
	add(t, dir, "mail")

	const path = "internal/mailsender/README.md"
	if err := os.Remove(filepath.Join(dir, path)); err != nil {
		t.Fatalf("remove %s: %v", path, err)
	}
	forget(t, dir, path)
	downgrade(t, dir, "mail", "0.0.1")

	result := upgrade(t, dir, nil, false)
	if !result.Behind() {
		t.Fatal("upgrade did not report mail as behind")
	}
	operation := plannedOperation(t, result.Result, path)
	if operation.Overwrite {
		t.Errorf("%s is planned as an update, want a new file", path)
	}
	if err := result.Apply(); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, path)); err != nil {
		t.Fatalf("payload file was not written: %v", err)
	}
	manifest := mustLoad(t, dir)
	if got, want := manifest.CapabilityVersion("mail"), capabilityVersion(t, "mail"); got != want {
		t.Fatalf("mail version = %q, want %q", got, want)
	}
	if drift := manifest.Drift(dir); len(drift) != 0 {
		t.Fatalf("drift after upgrade: %+v", drift)
	}
}

// TestUpgradeUpdatesAnUnmodifiedManagedFile proves a file weld wrote and the
// project never touched is safely rewritten.
func TestUpgradeUpdatesAnUnmodifiedManagedFile(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	dir := create(t, root)
	add(t, dir, "mail")

	const path = "internal/mailsender/README.md"
	weldWrote(t, dir, "mail", path, "the payload an older weld wrote\n")
	downgrade(t, dir, "mail", "0.0.1")

	result := upgrade(t, dir, nil, false)
	operation := plannedOperation(t, result.Result, path)
	if !operation.Overwrite {
		t.Errorf("%s is not planned as an update", path)
	}
	if err := result.Apply(); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if got := readFile(t, filepath.Join(dir, path)); strings.Contains(got, "an older weld wrote") {
		t.Fatalf("managed file was not updated:\n%s", got)
	}
	if drift := mustLoad(t, dir).Drift(dir); len(drift) != 0 {
		t.Fatalf("drift after upgrade: %+v", drift)
	}
}

// TestUpgradeReportsDriftedFileAndLeavesItAlone is the core safety property: a
// file the project changed is never overwritten, but the user is told exactly
// what a newer payload would change.
func TestUpgradeReportsDriftedFileAndLeavesItAlone(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	dir := create(t, root)
	add(t, dir, "mail")

	const path = "internal/mailsender/README.md"
	const mine = "my own notes, do not lose them\n"
	edited(t, dir, path, mine)
	downgrade(t, dir, "mail", "0.0.1")

	result := upgrade(t, dir, nil, false)
	if len(result.Advisories) != 1 {
		t.Fatalf("advisories = %+v, want one", result.Advisories)
	}
	advisory := result.Advisories[0]
	if advisory.Path != path || advisory.Capability != "mail" || advisory.Reason != "modified" {
		t.Fatalf("advisory = %+v", advisory)
	}
	if !strings.Contains(advisory.Diff, "-"+strings.TrimSpace(mine)) {
		t.Errorf("advisory diff does not show the project's line:\n%s", advisory.Diff)
	}
	for _, operation := range result.Operations {
		if operation.Path == path {
			t.Fatalf("%s is planned for writing despite drift", path)
		}
	}
	if err := result.Apply(); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if got := readFile(t, filepath.Join(dir, path)); got != mine {
		t.Fatalf("drifted file was rewritten:\n%s", got)
	}
	if drift := mustLoad(t, dir).Drift(dir); len(drift) != 1 {
		t.Fatalf("drift = %+v, want the edited file", drift)
	}
}

// TestUpgradeDoesNotResurrectADeletedFile proves a managed file the project
// deleted is reported, never restored behind the user's back.
func TestUpgradeDoesNotResurrectADeletedFile(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	dir := create(t, root)
	add(t, dir, "mail")

	const path = "internal/mailsender/README.md"
	if err := os.Remove(filepath.Join(dir, path)); err != nil {
		t.Fatalf("remove %s: %v", path, err)
	}
	downgrade(t, dir, "mail", "0.0.1")

	result := upgrade(t, dir, nil, false)
	if len(result.Advisories) != 1 || result.Advisories[0].Reason != "missing" {
		t.Fatalf("advisories = %+v, want one missing file", result.Advisories)
	}
	if err := result.Apply(); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, path)); !os.IsNotExist(err) {
		t.Fatalf("deleted file was resurrected: %v", err)
	}
}

// TestUpgradeRetiresAFileOnlyWithPrune proves a file a newer payload no longer
// ships is kept by default and deleted only on request, and only unmodified.
func TestUpgradeRetiresAFileOnlyWithPrune(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	dir := create(t, root)
	add(t, dir, "mail")

	const path = "internal/mailsender/legacy.go"
	content := "package mailsender\n\n// a file the payload retired\n"
	writeProjectFile(t, dir, path, content)
	manifest := mustLoad(t, dir)
	manifest.SetFile(path, "mail", project.RolePayload, []byte(content))
	writeManifestFile(t, dir, manifest)
	downgrade(t, dir, "mail", "0.0.1")

	kept := upgrade(t, dir, nil, false)
	if !strings.Contains(strings.Join(kept.Result.Notes, "\n"), "no longer ships") {
		t.Fatalf("notes do not mention the retired file: %v", kept.Result.Notes)
	}
	found := false
	for _, operation := range kept.Operations {
		found = found || operation.Path == path
	}
	if found {
		t.Fatal("a retired file was deleted without --prune")
	}

	pruned := upgrade(t, dir, nil, true)
	operation := plannedOperation(t, pruned.Result, path)
	if !operation.Delete {
		t.Fatalf("retired file is not planned for deletion: %+v", operation)
	}
	if err := pruned.Apply(); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, path)); !os.IsNotExist(err) {
		t.Fatalf("retired file survived --prune: %v", err)
	}
	if mustLoad(t, dir).Owns(path) {
		t.Fatal("retired file is still recorded in the manifest")
	}
}

// TestUpgradeNeverPrunesAModifiedRetiredFile proves --prune cannot delete a file
// the project changed, even when a newer payload no longer ships it.
func TestUpgradeNeverPrunesAModifiedRetiredFile(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	dir := create(t, root)
	add(t, dir, "mail")

	const path = "internal/mailsender/legacy.go"
	writeProjectFile(t, dir, path, "package mailsender\n\n// kept by hand\n")
	manifest := mustLoad(t, dir)
	manifest.SetFile(path, "mail", project.RolePayload, []byte("package mailsender\n"))
	writeManifestFile(t, dir, manifest)
	downgrade(t, dir, "mail", "0.0.1")

	result := upgrade(t, dir, nil, true)
	if len(result.Advisories) != 1 || result.Advisories[0].Reason != "retired" {
		t.Fatalf("advisories = %+v, want one retired file", result.Advisories)
	}
	for _, operation := range result.Operations {
		if operation.Path == path {
			t.Fatalf("a modified retired file is planned for writing: %+v", operation)
		}
	}
	if err := result.Apply(); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, path)); err != nil {
		t.Fatalf("modified retired file was deleted: %v", err)
	}
}

// TestUpgradeRestoresTheGeneratedGraph proves the regenerated Loom graph is
// reconciled too, so a project whose generated file drifted (or was written by an
// older weld) is repaired and the Loom initializer is regenerated.
func TestUpgradeRestoresTheGeneratedGraph(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	dir := create(t, root)
	add(t, dir, "http")

	const path = "internal/di/di.go"
	const provider = "loom.Provide(NewLogger),"
	original := readFile(t, filepath.Join(dir, path))
	if !strings.Contains(original, provider) {
		t.Fatalf("generated graph does not declare %s:\n%s", provider, original)
	}
	edited(t, dir, path, strings.Replace(original, "\t"+provider+"\n", "", 1))

	result := upgrade(t, dir, nil, false)
	operation := plannedOperation(t, result.Result, path)
	if !operation.Overwrite {
		t.Errorf("%s is not planned as an update", path)
	}
	if !result.GenerateLoom {
		t.Error("upgrade does not request a Loom regeneration")
	}
	if err := result.Apply(); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if got := readFile(t, filepath.Join(dir, path)); !strings.Contains(got, provider) {
		t.Fatalf("generated graph was not repaired:\n%s", got)
	}
}

// TestUpgradeRejectsAnUninstalledName proves an explicit request for a capability
// the project does not have is an error, not a silent no-op.
func TestUpgradeRejectsAnUninstalledName(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	dir := create(t, root)

	if _, err := Upgrade(Request{Dir: dir, Catalog: newCatalog()}, []string{"http"}, false); err == nil {
		t.Fatal("upgrade accepted an uninstalled capability")
	}
}

// TestUpgradeSkipsACapabilityTheCatalogNoLongerShips proves a recorded capability
// this weld build dropped is reported, not fatal.
func TestUpgradeSkipsACapabilityTheCatalogNoLongerShips(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	dir := create(t, root)
	add(t, dir, "redis")

	manifest := mustLoad(t, dir)
	manifest.AddCapability(project.CapabilityRef{Name: "gone", Version: "1.0.0"})
	writeManifestFile(t, dir, manifest)

	result := upgrade(t, dir, nil, false)
	if len(result.Unknown) != 1 || result.Unknown[0] != "gone" {
		t.Fatalf("unknown = %v, want [gone]", result.Unknown)
	}
	if !strings.Contains(strings.Join(result.Result.Notes, "\n"), "no longer ships") {
		t.Fatalf("notes do not mention the dropped capability: %v", result.Result.Notes)
	}
}

// TestUpgradeNeverRewritesAFileAnotherCapabilityOwns is the guard against the
// worst possible upgrade: a marker patch makes the patching capability the
// recorded owner of a shared file, so rewriting that file from the original
// capability's payload would drop the other capability's region — and, for
// go.mod, the project's dependencies with it.
func TestUpgradeNeverRewritesAFileAnotherCapabilityOwns(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	dir := create(t, root)
	add(t, dir, "http")

	shared := []string{"go.mod", "go.sum", "Makefile", ".gitignore", "config.yml", "config.example.yml"}
	before := map[string]string{}
	for _, path := range shared {
		before[path] = readFile(t, filepath.Join(dir, path))
	}
	// Pretend every capability, including the ones that own the base payload of
	// these shared files, is behind.
	downgrade(t, dir, "base", "0.0.1")
	downgrade(t, dir, "config", "0.0.1")
	downgrade(t, dir, "loom", "0.0.1")
	downgrade(t, dir, "http", "0.0.1")

	result := upgrade(t, dir, nil, false)
	for _, operation := range result.Operations {
		for _, path := range shared {
			if operation.Path == path {
				t.Errorf("upgrade plans to write the shared file %s", path)
			}
		}
	}
	if err := result.Apply(); err != nil {
		t.Fatalf("apply: %v", err)
	}
	for _, path := range shared {
		if got := readFile(t, filepath.Join(dir, path)); got != before[path] {
			t.Errorf("%s changed during the upgrade:\n%s", path, got)
		}
	}
	if got := readFile(t, filepath.Join(dir, "go.mod")); !strings.Contains(got, "github.com/spf13/cobra") {
		t.Fatalf("go.mod lost its dependencies:\n%s", got)
	}
}

// TestUpgradeNeverRetiresAMarkerPatchedSharedFile is the regression guard for the
// retire pass: a marker patch records the patching capability as the writer of
// go.mod, config.yml, config.example.yml and internal/config/config.go, so
// "the files recorded under this capability" also contains shared files. Treating
// them as retired payload files would let --prune delete the project's
// dependencies and its configuration.
func TestUpgradeNeverRetiresAMarkerPatchedSharedFile(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	dir := create(t, root)
	add(t, dir, "redis")

	shared := []string{"go.mod", "go.sum", "config.yml", "config.example.yml", "internal/config/config.go"}
	before := map[string]string{}
	for _, path := range shared {
		before[path] = readFile(t, filepath.Join(dir, path))
	}
	downgrade(t, dir, "redis", "0.0.1")

	result := upgrade(t, dir, nil, true)
	for _, operation := range result.Operations {
		if operation.Delete {
			t.Errorf("upgrade prunes the shared file %s", operation.Path)
		}
	}
	if notes := strings.Join(result.Result.Notes, "\n"); strings.Contains(notes, "no longer ships") {
		t.Fatalf("upgrade treats a shared file as retired:\n%s", notes)
	}
	if err := result.Apply(); err != nil {
		t.Fatalf("apply: %v", err)
	}
	for _, path := range shared {
		if got := readFile(t, filepath.Join(dir, path)); got != before[path] {
			t.Errorf("%s changed during the upgrade:\n%s", path, got)
		}
	}
}

// TestUpgradeRejectsAModuleOrCommandName proves the error says why a module is not
// upgradable rather than reporting it as an unknown capability.
func TestUpgradeRejectsAModuleOrCommandName(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	dir := create(t, root)
	add(t, dir, "http")
	result, err := AddModule(Request{Dir: dir, Catalog: newCatalog(), WithDependencies: true}, "items")
	if err != nil {
		t.Fatalf("AddModule: %v", err)
	}
	if err := result.Apply(); err != nil {
		t.Fatalf("apply module: %v", err)
	}

	_, err = Upgrade(Request{Dir: dir, Catalog: newCatalog()}, []string{"items"}, false)
	if err == nil {
		t.Fatal("upgrade accepted a business module")
	}
	if !strings.Contains(err.Error(), "never regenerates") {
		t.Fatalf("error = %v, want it to explain that a module is not upgradable", err)
	}
}

// TestUpgradeNeverRewritesAProjectOwnedSeam is the regression guard for the worst
// upgrade this tool shipped: internal/httpserver/middleware.go is project-owned,
// but a later `weld add` had re-hashed the project's edit into the manifest, so
// upgrade believed the file was unmodified and overwrote it — silently dropping
// the middleware the project had added to the chain.
func TestUpgradeNeverRewritesAProjectOwnedSeam(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	dir := create(t, root)
	add(t, dir, "http")

	const path = "internal/httpserver/middleware.go"
	weldWrote := readFile(t, filepath.Join(dir, path))
	editedContent := strings.Replace(weldWrote, "\t\thttpx.RequestID(),", "\t\thttpx.RequestID(),\n\t\thttpx.Recover(nil),", 1)
	if editedContent == weldWrote {
		t.Fatalf("fixture did not edit the chain:\n%s", weldWrote)
	}
	edited(t, dir, path, editedContent)
	// A later weld refreshed every hash, so the manifest records the project's
	// edit as weld's own baseline.
	manifest := mustLoad(t, dir)
	manifest.SetFile(path, "http", project.RoleSeam, []byte(editedContent))
	writeManifestFile(t, dir, manifest)
	downgrade(t, dir, "http", "0.0.1")

	result := upgrade(t, dir, []string{"http"}, false)
	for _, operation := range result.Operations {
		if operation.Path == path {
			t.Fatalf("upgrade plans to write a project-owned seam: %+v", operation)
		}
	}
	var advisory *Advisory
	for i := range result.Advisories {
		if result.Advisories[i].Path == path {
			advisory = &result.Advisories[i]
		}
	}
	if advisory == nil {
		t.Fatalf("no advisory for the changed seam: %+v", result.Advisories)
	}
	if advisory.Reason != "seam" {
		t.Fatalf("advisory reason = %q, want seam", advisory.Reason)
	}
	if advisory.Diff == "" {
		t.Fatal("the seam advisory carries no diff")
	}
	if err := result.Apply(); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if got := readFile(t, filepath.Join(dir, path)); got != editedContent {
		t.Fatalf("the seam was rewritten:\n%s", got)
	}
}

// TestUpgradeUpdatesASharedFileRecordedUnderAnotherCapability is the regression
// guard for the CORS failure. Two bugs combined there: a marker patch recorded
// the patching capability as the owner of internal/config/config.go, and upgrade
// trusted that record, so the config capability refused to move a file it ships —
// leaving the project referencing a configuration section that did not exist.
func TestUpgradeUpdatesASharedFileRecordedUnderAnotherCapability(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	dir := create(t, root)
	add(t, dir, "redis")

	const path = "internal/config/config.go"
	const field = "\tCORS        CORS        `yaml:\"cors\"`"
	live := readFile(t, filepath.Join(dir, path))
	older := strings.Replace(live, field+"\n", "", 1)
	if older == live {
		t.Fatalf("fixture did not drop the newer payload's field from:\n%s", live)
	}
	// The project's file is what an older payload wrote, and the manifest records
	// the patching capability as its owner with that content as the baseline.
	edited(t, dir, path, older)
	manifest := mustLoad(t, dir)
	manifest.SetFile(path, "redis", project.RolePatched, []byte(older))
	writeManifestFile(t, dir, manifest)
	downgrade(t, dir, "config", "0.0.1")

	result := upgrade(t, dir, []string{"config"}, false)
	operation := plannedOperation(t, result.Result, path)
	if !operation.Overwrite {
		t.Fatalf("the shared payload file is not planned as an update: %+v", operation)
	}
	got := string(operation.Content)
	if !strings.Contains(got, field) {
		t.Fatalf("the newer payload was not spliced into the shared file:\n%s", got)
	}
	if !strings.Contains(got, "weld:redis:installed") {
		t.Fatalf("the splice dropped the patching capability's region:\n%s", got)
	}
	// The rewritten manifest gives the file back to the capability that ships it.
	recorded := plannedOperation(t, result.Result, project.ManifestName)
	var written project.Manifest
	if err := json.Unmarshal(recorded.Content, &written); err != nil {
		t.Fatalf("decode the planned manifest: %v", err)
	}
	entry, ok := written.FileEntry(path)
	if !ok {
		t.Fatalf("the planned manifest has no record for %s", path)
	}
	if entry.Capability != "config" {
		t.Fatalf("recorded owner = %q, want config", entry.Capability)
	}
	if !entry.Baseline {
		t.Fatal("the updated file was not marked as a trusted baseline")
	}
}

// TestUpgradePruneKeepsTheGeneratedGraph is the regression guard for the retire
// pass: internal/di/di.go and di_test.go ship through the capability's di entry,
// not its file list, so a retire pass that only read the file list reported them
// as retired and --prune then failed with "unmanaged file already exists" after
// deleting the record the graph regeneration depends on.
func TestUpgradePruneKeepsTheGeneratedGraph(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	dir := create(t, root)
	add(t, dir, "redis")
	downgrade(t, dir, "loom", "0.0.1")

	result := upgrade(t, dir, nil, true)
	if notes := strings.Join(result.Result.Notes, "\n"); strings.Contains(notes, "no longer ships") {
		t.Fatalf("upgrade treats a generated file as retired:\n%s", notes)
	}
	for _, operation := range result.Operations {
		if operation.Delete && strings.HasPrefix(operation.Path, "internal/di/") {
			t.Fatalf("upgrade prunes a generated file: %+v", operation)
		}
	}
	if err := result.Apply(); err != nil {
		t.Fatalf("apply: %v", err)
	}
	for _, path := range []string{"internal/di/di.go", "internal/di/di_test.go"} {
		if _, err := os.Stat(filepath.Join(dir, path)); err != nil {
			t.Fatalf("%s is gone: %v", path, err)
		}
	}
}

// TestApplyRollsBackWhenThePostChangeCheckFails proves a plan that would leave the
// project unable to build is refused and undone, so an upgrade can never be the
// reason a project stops compiling.
func TestApplyRollsBackWhenThePostChangeCheckFails(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	dir := create(t, root)

	result := &Result{
		Dir:        dir,
		Operations: []project.Operation{{Path: "internal/broken/broken.go", Content: []byte("package broken\n\nfunc broken() { undefinedSymbol() }\n")}},
		Verify:     []string{"build", "./..."},
	}
	err := result.Apply()
	if err == nil {
		t.Fatal("Apply accepted a plan that does not build")
	}
	if !strings.Contains(err.Error(), "changes rolled back") {
		t.Fatalf("error = %v, want a rollback notice", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "internal/broken/broken.go")); !os.IsNotExist(statErr) {
		t.Fatalf("the broken file survived the rollback: %v", statErr)
	}
}

// TestApplyWritesWhenTheCheckAlreadyFails proves a project that does not build
// before the change is not held hostage by the check: the plan is written and the
// report says the check was skipped because it cannot attribute the failure.
func TestApplyWritesWhenTheCheckAlreadyFails(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	dir := create(t, root)
	writeProjectFile(t, dir, "internal/broken/broken.go", "package broken\n\nfunc broken() { undefinedSymbol() }\n")

	result := &Result{
		Dir:        dir,
		Operations: []project.Operation{{Path: "notes.txt", Content: []byte("written anyway\n")}},
		Verify:     []string{"build", "./..."},
	}
	if err := result.Apply(); err != nil {
		t.Fatalf("Apply refused a plan because the project already failed the check: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "notes.txt")); err != nil {
		t.Fatalf("the plan was not written: %v", err)
	}
	notes := strings.Join(result.Notes, "\n")
	if !strings.Contains(notes, "already fails") {
		t.Fatalf("notes = %v, want the pre-existing failure reported", result.Notes)
	}
	if !strings.Contains(notes, "same diagnostics") {
		t.Fatalf("notes = %v, want the repeated failure reported", result.Notes)
	}
}

// TestApplyRollsBackOnANewDiagnostic proves the check compares diagnostics rather
// than only asking whether the project builds: a project that is already broken
// stays upgradable, but a diagnostic this plan introduces is still fatal — which
// is the case that matters, because migrating a project-owned seam can leave an
// unrelated file referencing the old signature.
func TestApplyRollsBackOnANewDiagnostic(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	dir := create(t, root)
	writeProjectFile(t, dir, "internal/broken/broken.go", "package broken\n\nfunc broken() { undefinedSymbol() }\n")

	result := &Result{
		Dir:        dir,
		Operations: []project.Operation{{Path: "internal/worse/worse.go", Content: []byte("package worse\n\nfunc worse() { anotherUndefined() }\n")}},
		Verify:     []string{"build", "./..."},
	}
	err := result.Apply()
	if err == nil {
		t.Fatal("Apply kept a plan that introduced a new compile error")
	}
	if !strings.Contains(err.Error(), "changes rolled back") || !strings.Contains(err.Error(), "internal/worse/worse.go") {
		t.Fatalf("error = %v, want the introduced diagnostic and a rollback", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "internal/worse/worse.go")); !os.IsNotExist(statErr) {
		t.Fatalf("the new file survived the rollback: %v", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "internal/broken/broken.go")); statErr != nil {
		t.Fatalf("the rollback touched a file the plan did not write: %v", statErr)
	}
}
