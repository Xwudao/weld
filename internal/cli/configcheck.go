package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/Xwudao/weld/internal/configcheck"
	"github.com/Xwudao/weld/internal/project"
	"github.com/Xwudao/weld/internal/projectcfg"
)

// referenceConfig is the committed configuration every capability extends; the
// local config.yml is compared with it.
const referenceConfig = "config.example.yml"

// newConfigCheckCommand builds `weld config check`, which validates weld.yml
// and compares config.yml with config.example.yml without printing a value.
func newConfigCheckCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "check",
		Short: "check weld.yml and compare config.yml with config.example.yml",
		Long: `Check the project's configuration files without running the project.

weld.yml, when present, is loaded the way weld build, dev, seed and deploy load
it: an unknown key, an unsupported version or an invalid hook is a failure.

config.yml is compared key by key with the committed config.example.yml, the
file every capability extends when it is installed. The generated loader
ignores an unknown key, so a typo silently keeps the default; check reports it.
It reports:

  fail  a value the loader cannot decode into the shipped type (a list where a
        string belongs, a quoted "true" for a boolean, "5" for a number), and
        a key defined twice in one mapping
  warn  a key config.example.yml does not have (a typo, or a setting you added
        without adding it to the example), a key the file leaves out (its
        default or the environment applies), and a fractional number where the
        example has a whole one

A finding names the key and the YAML kind, never the value, because config.yml
holds local credentials. Rules the project enforces itself at startup (a
minimum secret length, a valid origin) are not repeated here.

It exits non-zero on a failure, or on any warning with --strict.`,
		Args: cobra.NoArgs,
		RunE: runConfigCheck,
	}
	cmd.Flags().String("config", "config.yml", "configuration file to check, relative to --dir")
	cmd.Flags().Bool("strict", false, "treat warnings as failures")
	cmd.Flags().Bool("json", false, "print the findings as JSON")
	return cmd
}

// configReport is the result of `weld config check`, in the shape --json prints.
type configReport struct {
	// WeldYML is "ok", "absent", or the reason weld.yml does not load.
	WeldYML string `json:"weldYml"`
	// Config is the checked file, relative to the project, and Reference the file
	// it was compared with. Both are empty when the project has no config
	// capability.
	Config    string                `json:"config,omitempty"`
	Reference string                `json:"reference,omitempty"`
	Findings  []configcheck.Finding `json:"findings"`
	Failures  int                   `json:"failures"`
	Warnings  int                   `json:"warnings"`
}

// runConfigCheck checks weld.yml and config.yml and reports the findings.
func runConfigCheck(cmd *cobra.Command, args []string) error {
	dir, err := cmd.Flags().GetString("dir")
	if err != nil {
		return err
	}
	configPath, err := cmd.Flags().GetString("config")
	if err != nil {
		return err
	}
	strict, err := cmd.Flags().GetBool("strict")
	if err != nil {
		return err
	}
	asJSON, err := cmd.Flags().GetBool("json")
	if err != nil {
		return err
	}
	manifest, err := project.Load(dir)
	if err != nil {
		return err
	}

	report := configReport{WeldYML: "ok", Findings: []configcheck.Finding{}}
	if _, err := projectcfg.Load(dir); err != nil {
		if errors.Is(err, projectcfg.ErrNotFound) {
			report.WeldYML = "absent"
		} else {
			report.WeldYML = err.Error()
			report.Failures++
		}
	}
	if manifest.HasCapability("config") {
		report.Config, report.Reference = configPath, referenceConfig
		findings, err := checkConfigFile(dir, configPath)
		if err != nil {
			return err
		}
		report.Findings = findings
		for _, finding := range findings {
			if finding.Level == configcheck.LevelFail {
				report.Failures++
			} else {
				report.Warnings++
			}
		}
	}

	stdout := cmd.OutOrStdout()
	if asJSON {
		data, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			return err
		}
		fmt.Fprintf(stdout, "%s\n", data)
	} else {
		printConfigReport(cmd, report)
	}
	if report.Failures > 0 || (strict && report.Warnings > 0) {
		return fmt.Errorf("configuration check failed: %d failure(s), %d warning(s)", report.Failures, report.Warnings)
	}
	return nil
}

// checkConfigFile compares the project's configuration file with the committed
// reference. A missing configuration file is a finding; a missing reference is
// an error, because there is nothing to compare with.
func checkConfigFile(dir, configPath string) ([]configcheck.Finding, error) {
	reference, err := os.ReadFile(filepath.Join(dir, referenceConfig))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", referenceConfig, err)
	}
	config, err := os.ReadFile(filepath.Join(dir, configPath))
	if errors.Is(err, os.ErrNotExist) {
		return []configcheck.Finding{{Level: configcheck.LevelFail, Key: "(document)", Message: fmt.Sprintf("%s is missing; copy %s to create it", configPath, referenceConfig)}}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", configPath, err)
	}
	findings, err := configcheck.Check(config, reference)
	if err != nil {
		return []configcheck.Finding{{Level: configcheck.LevelFail, Key: "(document)", Message: err.Error()}}, nil
	}
	return findings, nil
}

// printConfigReport writes the human-readable report.
func printConfigReport(cmd *cobra.Command, report configReport) {
	stdout := cmd.OutOrStdout()
	fmt.Fprintf(stdout, "%s: %s\n", projectcfg.FileName, report.WeldYML)
	if report.Config == "" {
		fmt.Fprintln(stdout, "config.yml: the config capability is not installed; nothing to compare")
		return
	}
	if len(report.Findings) == 0 {
		fmt.Fprintf(stdout, "%s: matches %s\n", report.Config, report.Reference)
		return
	}
	fmt.Fprintf(stdout, "%s compared with %s:\n", report.Config, report.Reference)
	w := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	for _, finding := range report.Findings {
		fmt.Fprintf(w, "  %s\t%s\t%s\n", finding.Level, finding.Key, finding.Message)
	}
	_ = w.Flush()
	fmt.Fprintf(stdout, "%d failure(s), %d warning(s)\n", report.Failures, report.Warnings)
}
