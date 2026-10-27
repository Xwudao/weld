package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Xwudao/weld/internal/scaffold"
	"github.com/Xwudao/weld/internal/template"
)

// newUpgradeCommand builds `weld upgrade`.
func newUpgradeCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "upgrade [capability...]",
		Short: "move installed capabilities to this weld's template versions",
		Long:  upgradeHelp(),
		RunE:  runUpgrade,
	}
	cmd.Flags().Bool("check", false, "report what is out of date and fail instead of writing")
	cmd.Flags().Bool("prune", false, "delete unmodified files a newer payload no longer ships")
	cmd.Flags().String("verify", "vet", "check the project after writing: vet, build or none")
	cmd.Flags().Bool("json", false, "print the report as JSON, with each advisory's target content, diffs and merge")
	cmd.AddCommand(newUpgradeResolveCommand())
	return cmd
}

// upgradeHelp explains what `weld upgrade` does and, just as importantly, what
// it refuses to do.
func upgradeHelp() string {
	return `Move installed capabilities to the template versions this weld ships.

A capability records the version that wrote its files in weld.json, and every
managed file records the sha256 of what weld last wrote. Every file also has a
role, and the role decides what an upgrade may do with it:

  - a file weld owns outright is rewritten while the project has not changed it,
    and reported with a unified diff when it has;
  - a shared file (one that carries marker regions another capability extends,
    for example internal/config/config.go) is moved forward by rendering the
    newer payload and splicing the live marker regions into it, so a capability's
    snippet and your own settings inside a region both survive;
  - a project-owned seam is never written. internal/httpserver/middleware.go,
    internal/httpserver/policy.go, the *_providers.go and register.go files, the
    Makefile and config.yml are yours after weld writes them once, so a payload
    change there is reported with a diff for you to fold in;
  - the generated Loom graph is re-rendered from the installed capability set;
  - go.mod and go.sum belong to the Go toolchain and are never rewritten.

A file you deleted is reported, never resurrected. A file a newer payload no
longer ships is reported, and deleted only with --prune, only when it is a plain
payload file and only when it is unmodified. A marker region that lacks lines of
a newer snippet (a new config key, Makefile target or ignore rule) is reported
too: snippets are never re-applied.

Every reported file is an advisory, and an advisory carries what is needed to
fold it in without losing an edit: the content this weld would generate today
(target) and, when weld can recover what it last wrote — from the file itself, a
copy weld upgrade resolve kept, or the project's git history — weld's own change
(upstreamDiff), the project's change (localDiff) and a three-way merge of both
(merged, with conflict markers where both changed a block). --json prints all of
it; the text report shows weld's change. The capabilities' changelog entries
since the recorded version are printed too, with the manual steps they imply.

Advisories are recorded in weld.json and reported by every later upgrade until
they are accepted, so fold each change in and then run:

  ` + progName + ` upgrade resolve <path>...    accept the files you updated
  ` + progName + ` upgrade resolve --all        accept every pending change

With no arguments every installed capability is considered. Naming capabilities
limits the run, and a name that is not installed is an error.

After writing the plan, ` + progName + ` runs a check inside the project and rolls
the whole upgrade back if it fails, so an upgrade can never leave a project that
does not build. --verify selects the check:

  --verify=vet     go vet ./... (the default: it compiles the tests too)
  --verify=build   go build ./...
  --verify=none    write without checking

The check compares diagnostics, not just the outcome, so a project that is already
broken stays upgradable: only a diagnostic the upgrade introduces is a failure.
That is what lets a manual seam migration happen while the payload has not moved.

  ` + progName + ` upgrade                       upgrade everything that is behind
  ` + progName + ` upgrade http                  upgrade one capability
  ` + progName + ` upgrade --dry-run             print the plan, write nothing
  ` + progName + ` upgrade --dry-run --json      the plan as JSON, for an agent to act on
  ` + progName + ` upgrade --check               CI: fail when anything is out of date or pending
  ` + progName + ` upgrade --prune               also delete retired, unmodified files
  ` + progName + ` upgrade --verify=build        check with a build instead of vet`
}

// upgradeVerify turns the --verify flag into the go command Apply runs. An empty
// result writes the plan unchecked.
func upgradeVerify(mode string) ([]string, error) {
	switch mode {
	case "vet":
		return []string{"vet", "./..."}, nil
	case "build":
		return []string{"build", "./..."}, nil
	case "none", "":
		return nil, nil
	default:
		return nil, fmt.Errorf("unknown --verify mode %q: use vet, build or none", mode)
	}
}

// runUpgrade plans and, unless it is a check or a dry run, applies an upgrade.
func runUpgrade(cmd *cobra.Command, args []string) error {
	stdout := cmd.OutOrStdout()
	dir, err := cmd.Flags().GetString("dir")
	if err != nil {
		return err
	}
	dryRun, err := cmd.Flags().GetBool("dry-run")
	if err != nil {
		return err
	}
	check, err := cmd.Flags().GetBool("check")
	if err != nil {
		return err
	}
	prune, err := cmd.Flags().GetBool("prune")
	if err != nil {
		return err
	}
	asJSON, err := cmd.Flags().GetBool("json")
	if err != nil {
		return err
	}
	verifyMode, err := cmd.Flags().GetString("verify")
	if err != nil {
		return err
	}
	verify, err := upgradeVerify(verifyMode)
	if err != nil {
		return err
	}

	req := scaffold.Request{Dir: dir, Catalog: template.Load(), Verify: verify, History: scaffold.GitHistory}
	result, err := scaffold.Upgrade(req, args, prune)
	if err != nil {
		return err
	}

	mode := upgradeApplied
	switch {
	case check:
		mode = upgradeCheck
	case dryRun:
		mode = upgradeDryRun
	}
	// A check never writes: it fails when weld would change anything at all,
	// including a generated file that drifted or a change still pending, so CI
	// catches staleness rather than only a version gap.
	if mode == upgradeApplied {
		if err := result.Apply(); err != nil {
			return err
		}
	}
	switch {
	case asJSON:
		if err := printUpgradeJSON(stdout, result, mode); err != nil {
			return err
		}
	case mode == upgradeCheck && result.UpToDate():
		fmt.Fprintln(stdout, "Every installed capability is already at this weld's version.")
	default:
		printUpgradeResult(stdout, result, mode)
	}
	if mode == upgradeCheck && !result.UpToDate() {
		if len(result.Upgraded) == 0 && len(result.Operations) == 0 {
			return fmt.Errorf("the project has pending upgrade changes; fold them in and run: %s upgrade resolve", progName)
		}
		return fmt.Errorf("the project is not up to date with this weld; run: %s upgrade", progName)
	}
	return nil
}

// upgradeMode is how an upgrade reports itself: it applied the plan, or it is
// previewing it as a dry run or a check.
type upgradeMode string

const (
	upgradeApplied upgradeMode = "applied"
	upgradeDryRun  upgradeMode = "dry run"
	upgradeCheck   upgradeMode = "check"
)

// upgradeReport is the shape `weld upgrade --json` prints. It is a stable
// contract for scripts and agents, so fields are only ever added.
type upgradeReport struct {
	// Mode is "applied", "dry-run" or "check".
	Mode string `json:"mode"`
	// UpToDate is true when nothing moves, nothing is written and nothing is
	// pending.
	UpToDate   bool                 `json:"upToDate"`
	Upgraded   []upgradedCapability `json:"upgraded"`
	Current    []string             `json:"current"`
	Unknown    []string             `json:"unknown"`
	Operations []plannedOperation   `json:"operations"`
	Notes      []string             `json:"notes"`
	Changelog  []scaffold.Changelog `json:"changelog"`
	Advisories []scaffold.Advisory  `json:"advisories"`
	// NextSteps says, in order, what to do with the report.
	NextSteps []string `json:"nextSteps"`
}

// upgradedCapability is one capability an upgrade moves.
type upgradedCapability struct {
	Name  string   `json:"name"`
	From  string   `json:"from"`
	To    string   `json:"to"`
	Files []string `json:"files"`
}

// plannedOperation is one file write an upgrade plans or performed.
type plannedOperation struct {
	Action string `json:"action"`
	Path   string `json:"path"`
}

// printUpgradeJSON prints the report as indented JSON. Empty lists are printed
// as [] rather than null, so a consumer never has to tell the two apart.
func printUpgradeJSON(stdout io.Writer, result *scaffold.UpgradeResult, mode upgradeMode) error {
	report := upgradeReport{
		Mode:       strings.ReplaceAll(string(mode), " ", "-"),
		UpToDate:   result.UpToDate(),
		Upgraded:   []upgradedCapability{},
		Current:    append([]string{}, result.Current...),
		Unknown:    append([]string{}, result.Unknown...),
		Operations: []plannedOperation{},
		Notes:      append([]string{}, result.Notes...),
		Changelog:  append([]scaffold.Changelog{}, result.Changelog...),
		Advisories: append([]scaffold.Advisory{}, result.Advisories...),
		NextSteps:  upgradeNextSteps(result, mode),
	}
	for _, delta := range result.Upgraded {
		report.Upgraded = append(report.Upgraded, upgradedCapability{
			Name: delta.Name, From: delta.From, To: delta.To, Files: append([]string{}, delta.Files...),
		})
	}
	for _, operation := range result.Operations {
		report.Operations = append(report.Operations, plannedOperation{Action: operationAction(operation.Delete, operation.Overwrite), Path: operation.Path})
	}
	encoded, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout, "%s\n", encoded)
	return err
}

// upgradeNextSteps turns a report into the ordered steps that finish it, so a
// reader — often an agent — knows what to do without re-deriving the workflow.
func upgradeNextSteps(result *scaffold.UpgradeResult, mode upgradeMode) []string {
	steps := []string{}
	if mode != upgradeApplied && (len(result.Upgraded) > 0 || len(result.Operations) > 0) {
		steps = append(steps, fmt.Sprintf("run `%s upgrade` to write the planned changes (an advisory's diffs stay valid: weld never writes those files)", progName))
	}
	var open, settled []string
	conflicts := false
	for _, advisory := range result.Advisories {
		if advisory.Settled {
			settled = append(settled, advisory.Path)
			continue
		}
		open = append(open, advisory.Path)
		if advisory.Conflicts > 0 {
			conflicts = true
		}
	}
	if len(result.Changelog) > 0 {
		steps = append(steps, "read the changelog: its actions are the manual steps a diff cannot express (a renamed key, a new call your code has to make)")
	}
	if len(open) > 0 {
		step := "fold each open advisory into its file: apply upstreamDiff (weld's change) while keeping localDiff (the project's change); `merged` is that result when conflicts is 0"
		if conflicts {
			step += ", and resolves the blocks between conflict markers by hand otherwise"
		}
		step += "; without a base, compare the file with target and keep the project's own edits. A patch advisory lists the snippet lines its marker region is missing"
		steps = append(steps, step)
		steps = append(steps, "run the project's checks (go build ./... && go test ./...)")
		steps = append(steps, fmt.Sprintf("accept the folded changes: %s upgrade resolve %s", progName, strings.Join(uniqueStrings(append(open, settled...)), " ")))
	} else if len(settled) > 0 {
		steps = append(steps, fmt.Sprintf("these files already match what this weld ships; accept them: %s upgrade resolve %s", progName, strings.Join(uniqueStrings(settled), " ")))
	}
	if !result.UpToDate() {
		steps = append(steps, fmt.Sprintf("confirm nothing is left: %s upgrade --check", progName))
	}
	return steps
}

// printUpgradeResult reports an upgrade: the capabilities that moved, the files
// weld writes, the changelog and the changes left for the project.
func printUpgradeResult(stdout io.Writer, result *scaffold.UpgradeResult, mode upgradeMode) {
	printNotes(stdout, result.Result)
	if len(result.Upgraded) > 0 {
		verb := "Upgraded"
		noun := "capabilities"
		if mode != upgradeApplied {
			verb = "Would upgrade"
		}
		if len(result.Upgraded) == 1 {
			noun = "capability"
		}
		fmt.Fprintf(stdout, "%s %d %s:\n", verb, len(result.Upgraded), noun)
		for _, delta := range result.Upgraded {
			fmt.Fprintf(stdout, "  %-8s %s -> %s\n", delta.Name, delta.From, delta.To)
			for _, path := range delta.Files {
				fmt.Fprintf(stdout, "      %s\n", path)
			}
		}
	}
	switch mode {
	case upgradeApplied:
		printOperations(stdout, result.Result, false)
	default:
		printOperationsFor(stdout, result.Result, "Plan (%d change(s), "+string(mode)+"):")
	}
	if len(result.Upgraded) == 0 && len(result.Operations) == 0 && len(result.Advisories) == 0 {
		fmt.Fprintln(stdout, "Every installed capability is already at this weld's version.")
	}
	printChangelog(stdout, result.Changelog)
	printAdvisories(stdout, result.Advisories)
	if !result.UpToDate() && len(result.Advisories) > 0 {
		fmt.Fprintln(stdout, "\nNext steps:")
		for i, step := range upgradeNextSteps(result, mode) {
			fmt.Fprintf(stdout, "  %d. %s\n", i+1, step)
		}
		fmt.Fprintf(stdout, "  (`%s upgrade --dry-run --json` adds each file's target content and merge)\n", progName)
	}
}

// printOperationsFor prints the planned operations under a caller-supplied
// header format, so an upgrade can say "dry run" or "check" where an add says
// "Applied".
func printOperationsFor(stdout io.Writer, result *scaffold.Result, header string) {
	if len(result.Operations) == 0 {
		if len(result.Notes) == 0 {
			fmt.Fprintln(stdout, "No changes.")
		}
		return
	}
	fmt.Fprintf(stdout, header+"\n", len(result.Operations))
	for _, operation := range result.Operations {
		fmt.Fprintf(stdout, "  %s %s\n", operationAction(operation.Delete, operation.Overwrite), operation.Path)
	}
}

// operationAction names what an operation does to its file.
func operationAction(remove, overwrite bool) string {
	switch {
	case remove:
		return "remove"
	case overwrite:
		return "update"
	default:
		return "add"
	}
}

// printChangelog prints the changelog entries the upgrade spans.
func printChangelog(stdout io.Writer, changelog []scaffold.Changelog) {
	if len(changelog) == 0 {
		return
	}
	fmt.Fprintln(stdout, "\nChangelog:")
	for _, entry := range changelog {
		fmt.Fprintf(stdout, "  %s %s -> %s\n", entry.Capability, entry.From, entry.To)
		for _, change := range entry.Changes {
			fmt.Fprintf(stdout, "    %s  %s\n", change.Version, change.Summary)
			for _, action := range change.Actions {
				fmt.Fprintf(stdout, "      - %s\n", action)
			}
		}
	}
}

// printAdvisories prints the files weld refused to write on its own, each with
// the reason and the most useful diff it has: weld's own change when the base
// could be recovered, otherwise the diff from the project's file to the target.
func printAdvisories(stdout io.Writer, advisories []scaffold.Advisory) {
	if len(advisories) == 0 {
		return
	}
	noun := "files need"
	if len(advisories) == 1 {
		noun = "file needs"
	}
	fmt.Fprintf(stdout, "\n%d %s your attention (weld did not write these files):\n", len(advisories), noun)
	for _, advisory := range advisories {
		labels := []string{advisory.Capability, advisory.Reason}
		if advisory.Role != "" && string(advisory.Role) != advisory.Reason {
			labels = append(labels, string(advisory.Role))
		}
		if advisory.Marker != "" {
			labels = append(labels, "region weld:"+advisory.Marker)
		}
		if advisory.Pending && advisory.From != "" {
			labels = append(labels, "pending since "+advisory.From)
		}
		if advisory.Settled {
			labels = append(labels, "settled: run resolve")
		}
		fmt.Fprintf(stdout, "  %s (%s)\n", advisory.Path, strings.Join(labels, ", "))
		if advisory.Detail != "" {
			for _, line := range strings.Split(advisory.Detail, "\n") {
				fmt.Fprintf(stdout, "    %s\n", line)
			}
		}
		diff := advisory.Diff
		if advisory.UpstreamDiff != "" {
			trust := ""
			if !advisory.BaseTrusted {
				trust = ", from a legacy record"
			}
			merge := "a three-way merge with your file is clean"
			if advisory.Conflicts > 0 {
				merge = fmt.Sprintf("a three-way merge with your file has %d conflict(s)", advisory.Conflicts)
			}
			fmt.Fprintf(stdout, "    weld's change since it last wrote this file (base: %s%s); %s:\n", advisory.Base, trust, merge)
			diff = advisory.UpstreamDiff
		}
		if diff == "" || advisory.Settled {
			continue
		}
		for _, line := range strings.Split(strings.TrimSuffix(diff, "\n"), "\n") {
			fmt.Fprintf(stdout, "    %s\n", line)
		}
	}
}

// uniqueStrings returns values without repeats, in first-seen order.
func uniqueStrings(values []string) []string {
	seen := map[string]bool{}
	var unique []string
	for _, value := range values {
		if seen[value] {
			continue
		}
		seen[value] = true
		unique = append(unique, value)
	}
	return unique
}

// newUpgradeResolveCommand builds `weld upgrade resolve`.
func newUpgradeResolveCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "resolve [path...]",
		Short: "accept pending upgrade changes the project has folded in",
		Long: `Resolve records that the project has dealt with pending upgrade changes, so
weld upgrade stops reporting them.

Resolving a seam or a modified file moves its recorded baseline to the version
this weld ships, so the next upgrade reports only what changes after it. When
your file differs from that version, weld keeps a copy of it under
` + scaffold.BaselineDir + ` — the base the next upgrade's three-way merge needs.
Commit that directory with the project. A retired file you kept becomes yours and
leaves weld.json; a deleted file stays recorded, so it is still never
resurrected.

A file that still contains merge conflict markers is refused, and so is a change
whose capability is behind this weld (run weld upgrade first).

  ` + progName + ` upgrade resolve internal/httpserver/middleware.go
  ` + progName + ` upgrade resolve --all
  ` + progName + ` upgrade resolve --all --dry-run`,
		RunE: runUpgradeResolve,
	}
	cmd.Flags().Bool("all", false, "resolve every pending change")
	return cmd
}

// runUpgradeResolve plans and, unless it is a dry run, applies a resolve.
func runUpgradeResolve(cmd *cobra.Command, args []string) error {
	stdout := cmd.OutOrStdout()
	dir, err := cmd.Flags().GetString("dir")
	if err != nil {
		return err
	}
	dryRun, err := cmd.Flags().GetBool("dry-run")
	if err != nil {
		return err
	}
	all, err := cmd.Flags().GetBool("all")
	if err != nil {
		return err
	}
	if all && len(args) > 0 {
		return fmt.Errorf("pass either paths or --all, not both")
	}
	result, err := scaffold.Resolve(scaffold.Request{Dir: dir, Catalog: template.Load()}, args, all)
	if err != nil {
		return err
	}
	if !dryRun {
		if err := result.Apply(); err != nil {
			return err
		}
	}
	printNotes(stdout, result.Result)
	if len(result.Resolved) == 0 {
		return nil
	}
	verb := "Resolved"
	if dryRun {
		verb = "Would resolve"
	}
	fmt.Fprintf(stdout, "%s %d pending change(s):\n", verb, len(result.Resolved))
	for _, change := range result.Resolved {
		label := change.Reason
		if change.Marker != "" {
			label += ", region weld:" + change.Marker
		}
		fmt.Fprintf(stdout, "  %s (%s, %s)\n", change.Path, change.Capability, label)
	}
	printOperations(stdout, result.Result, dryRun)
	return nil
}
