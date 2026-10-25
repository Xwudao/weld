package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/Xwudao/weld/internal/project"
	"github.com/Xwudao/weld/internal/scaffold"
	"github.com/Xwudao/weld/internal/template"
)

// newExplainCommand builds `weld explain <capability>`.
func newExplainCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "explain <capability>",
		Short: "describe what a capability installs, requires and owns",
		Long: `Explain prints a capability's descriptor as weld will apply it: its version and
summary, its direct requirements and the capabilities that require it, every
file it writes with the role that decides how weld upgrade treats it, the marker
regions it patches in shared files, and its post-install steps.

Inside a project it also reports whether the capability is installed and, when
it is not, exactly which capabilities weld add would install for it. It is
read-only: it plans nothing and writes nothing.`,
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) != 1 {
				return fmt.Errorf("usage: %s explain <capability> [--json] [--dir dir]", progName)
			}
			return nil
		},
		RunE: runExplain,
	}
	cmd.Flags().Bool("json", false, "print the explanation as JSON")
	return cmd
}

// explanation is the read-only description `weld explain` prints. Its JSON form
// is a stable shape for scripts and agents, so fields are only ever added.
type explanation struct {
	Name        string            `json:"name"`
	Version     string            `json:"version"`
	Kind        string            `json:"kind"`
	Summary     string            `json:"summary"`
	Requires    []string          `json:"requires"`
	RequiredBy  []string          `json:"requiredBy"`
	Files       []explainedFile   `json:"files"`
	Patches     []explainedPatch  `json:"patches"`
	PostInstall []string          `json:"postInstall"`
	Project     *explainedProject `json:"project,omitempty"`
}

// explainedFile is one path the capability writes. A versioned migration is
// shown by its declared path, because its goose version is chosen at install
// time.
type explainedFile struct {
	Path       string   `json:"path"`
	Role       string   `json:"role"`
	Versioned  bool     `json:"versioned,omitempty"`
	When       []string `json:"when,omitempty"`
	WhenAbsent []string `json:"whenAbsent,omitempty"`
}

// explainedPatch is one marker region the capability extends in a shared file.
type explainedPatch struct {
	Path       string   `json:"path"`
	Marker     string   `json:"marker"`
	Mode       string   `json:"mode"`
	Bootstrap  string   `json:"bootstrap,omitempty"`
	When       []string `json:"when,omitempty"`
	WhenAbsent []string `json:"whenAbsent,omitempty"`
}

// explainedProject is the capability's state in the project --dir names.
type explainedProject struct {
	Name             string   `json:"name"`
	Installed        bool     `json:"installed"`
	InstalledVersion string   `json:"installedVersion,omitempty"`
	WouldInstall     []string `json:"wouldInstall,omitempty"`
}

// runExplain prints a capability's descriptor and, inside a project, its
// installation state.
func runExplain(cmd *cobra.Command, args []string) error {
	stdout := cmd.OutOrStdout()
	dir, err := cmd.Flags().GetString("dir")
	if err != nil {
		return err
	}
	asJSON, err := cmd.Flags().GetBool("json")
	if err != nil {
		return err
	}
	catalog := template.Load()
	capability, err := catalog.Get(args[0])
	if err != nil {
		names, namesErr := catalog.Names()
		if namesErr != nil {
			return err
		}
		return fmt.Errorf("%w (available: %s)", err, strings.Join(names, ", "))
	}
	explained, err := explainCapability(catalog, capability)
	if err != nil {
		return err
	}
	// Outside a project the explanation is still complete; the project section
	// is simply absent, so explain works before `weld new` too.
	if manifest, loadErr := project.Load(dir); loadErr == nil {
		state := &explainedProject{Name: manifest.Name, Installed: manifest.HasCapability(capability.Name)}
		if state.Installed {
			state.InstalledVersion = manifest.CapabilityVersion(capability.Name)
		} else if capability.Kind == template.KindAdd {
			if state.WouldInstall, err = scaffold.InstallOrder(catalog, manifest, capability.Name); err != nil {
				return err
			}
		}
		explained.Project = state
	}
	if asJSON {
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(explained)
	}
	printExplanation(stdout, explained)
	return nil
}

// explainCapability builds the description of capability from the catalog. The
// reverse requirements are derived from every descriptor rather than kept in a
// list, so they cannot go stale when a capability is added.
func explainCapability(catalog *template.Catalog, capability *template.Capability) (*explanation, error) {
	explained := &explanation{
		Name:        capability.Name,
		Version:     capability.Version,
		Kind:        string(capability.Kind),
		Summary:     capability.Summary,
		Requires:    append([]string{}, capability.Requires...),
		RequiredBy:  []string{},
		Files:       []explainedFile{},
		Patches:     []explainedPatch{},
		PostInstall: append([]string{}, capability.PostInstall...),
	}
	names, err := catalog.Names()
	if err != nil {
		return nil, err
	}
	for _, name := range names {
		other, err := catalog.Get(name)
		if err != nil {
			return nil, err
		}
		if slices.Contains(other.Requires, capability.Name) {
			explained.RequiredBy = append(explained.RequiredBy, other.Name)
		}
	}
	for _, file := range capability.Files {
		explained.Files = append(explained.Files, explainedFile{
			Path:       file.Path,
			Role:       string(capability.RoleOf(file.Path)),
			Versioned:  file.Versioned,
			When:       file.When,
			WhenAbsent: file.WhenAbsent,
		})
	}
	for _, path := range capability.DIPaths() {
		explained.Files = append(explained.Files, explainedFile{Path: path, Role: string(project.RoleGenerated)})
	}
	for _, patch := range capability.Patches {
		mode := patch.Mode
		if mode == "" {
			mode = "append"
		}
		explained.Patches = append(explained.Patches, explainedPatch{
			Path:       patch.Path,
			Marker:     patch.Marker,
			Mode:       mode,
			Bootstrap:  patch.Bootstrap,
			When:       patch.When,
			WhenAbsent: patch.WhenAbsent,
		})
	}
	return explained, nil
}

// printExplanation writes the human-readable form of explained.
func printExplanation(stdout io.Writer, explained *explanation) {
	fmt.Fprintf(stdout, "%s v%s (%s)\n", explained.Name, explained.Version, explained.Kind)
	for _, line := range wrapText(explained.Summary, 76) {
		fmt.Fprintf(stdout, "  %s\n", line)
	}

	fmt.Fprintf(stdout, "\nRequires:    %s\n", joinOrNone(explained.Requires))
	fmt.Fprintf(stdout, "Required by: %s\n", joinOrNone(explained.RequiredBy))
	if state := explained.Project; state != nil {
		switch {
		case state.Installed:
			fmt.Fprintf(stdout, "Project:     installed v%s in %s\n", state.InstalledVersion, state.Name)
		case len(state.WouldInstall) > 0:
			fmt.Fprintf(stdout, "Project:     not installed in %s; adding it installs %s\n",
				state.Name, strings.Join(state.WouldInstall, ", "))
			if len(state.WouldInstall) > 1 {
				fmt.Fprintf(stdout, "             (run: %s add %s --with-dependencies)\n", progName, explained.Name)
			}
		default:
			fmt.Fprintf(stdout, "Project:     not installed in %s\n", state.Name)
		}
	}

	if len(explained.Files) > 0 {
		fmt.Fprintln(stdout, "\nFiles:")
		table := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
		for _, file := range explained.Files {
			path := file.Path
			if file.Versioned {
				path = versionedDisplayPath(path)
			}
			// Only a conditional file gets a third cell, so an unconditional row
			// carries no trailing padding.
			if condition := conditionText(file.When, file.WhenAbsent); condition != "" {
				fmt.Fprintf(table, "  %s\t%s\t%s\n", path, file.Role, condition)
			} else {
				fmt.Fprintf(table, "  %s\t%s\n", path, file.Role)
			}
		}
		table.Flush()
	}
	if len(explained.Patches) > 0 {
		fmt.Fprintln(stdout, "\nPatches (marker regions in shared files):")
		table := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
		for _, patch := range explained.Patches {
			detail := patch.Mode
			if patch.Bootstrap != "" {
				detail += ", restored from " + patch.Bootstrap
			}
			if condition := conditionText(patch.When, patch.WhenAbsent); condition != "" {
				detail += ", " + condition
			}
			fmt.Fprintf(table, "  %s\tweld:%s\t%s\n", patch.Path, patch.Marker, detail)
		}
		table.Flush()
	}
	if len(explained.PostInstall) > 0 {
		fmt.Fprintln(stdout, "\nAfter installing:")
		for _, step := range explained.PostInstall {
			for i, line := range wrapText(step, 74) {
				prefix := "    "
				if i == 0 {
					prefix = "  - "
				}
				fmt.Fprintf(stdout, "%s%s\n", prefix, line)
			}
		}
	}
	fmt.Fprintln(stdout, "\nRoles: payload is rewritten by upgrade while unmodified; patched keeps its")
	fmt.Fprintln(stdout, "marker regions; seam is the project's after install; generated is re-rendered.")
}

// versionedDisplayPath shows where a versioned migration lands, since its goose
// version is chosen when the capability is installed.
func versionedDisplayPath(path string) string {
	slash := strings.LastIndex(path, "/")
	return path[:slash+1] + "<version>_" + path[slash+1:]
}

// conditionText renders a file or patch condition, or "" when it always applies.
func conditionText(when, whenAbsent []string) string {
	var parts []string
	if len(when) > 0 {
		parts = append(parts, "when "+strings.Join(when, "+"))
	}
	if len(whenAbsent) > 0 {
		parts = append(parts, "without "+strings.Join(whenAbsent, "+"))
	}
	return strings.Join(parts, ", ")
}

// joinOrNone renders a name list, or "(none)" for an empty one.
func joinOrNone(names []string) string {
	if len(names) == 0 {
		return "(none)"
	}
	return strings.Join(names, ", ")
}

// wrapText splits text into lines of at most width runes at word boundaries. A
// single word longer than width stays on its own line rather than being cut.
func wrapText(text string, width int) []string {
	var lines []string
	var line strings.Builder
	for _, word := range strings.Fields(text) {
		if line.Len() > 0 && line.Len()+1+len(word) > width {
			lines = append(lines, line.String())
			line.Reset()
		}
		if line.Len() > 0 {
			line.WriteByte(' ')
		}
		line.WriteString(word)
	}
	if line.Len() > 0 {
		lines = append(lines, line.String())
	}
	return lines
}
