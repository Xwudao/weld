package cli

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Xwudao/weld/internal/doctor"
)

// newDoctorCommand builds `weld doctor`.
func newDoctorCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "run read-only health checks on the project",
		Long: `Run read-only checks on the project in --dir.

weld doctor inspects the manifest, weld.yml, the generated application config,
managed-file drift, the Go module, required tools, the Loom graph, generated
enums and the db migration layout. It never writes, builds, connects to a
service or prints a credential.

Warnings are advisory (a user edit, a tool missing for an optional capability).
Failures make the command exit non-zero, so it can gate CI:

  weld doctor
  weld doctor --json`,
		Args: cobra.NoArgs,
		RunE: runDoctor,
	}
	cmd.Flags().Bool("json", false, "print the report as JSON")
	return cmd
}

// runDoctor prints the report and fails when a check failed.
func runDoctor(cmd *cobra.Command, _ []string) error {
	stdout := cmd.OutOrStdout()
	dir, err := cmd.Flags().GetString("dir")
	if err != nil {
		return err
	}
	asJSON, err := cmd.Flags().GetBool("json")
	if err != nil {
		return err
	}
	result := doctor.Run(doctor.Request{Dir: dir})
	ok, warnings, failures := result.Counts()

	if asJSON {
		payload := struct {
			Dir      string         `json:"dir"`
			Checks   []doctor.Check `json:"checks"`
			OK       int            `json:"ok"`
			Warnings int            `json:"warnings"`
			Failures int            `json:"failures"`
		}{Dir: result.Dir, Checks: result.Checks, OK: ok, Warnings: warnings, Failures: failures}
		encoded, err := json.MarshalIndent(payload, "", "  ")
		if err != nil {
			return err
		}
		fmt.Fprintln(stdout, string(encoded))
	} else {
		fmt.Fprintf(stdout, "weld doctor: %s\n\n", result.Dir)
		for _, check := range result.Checks {
			fmt.Fprintf(stdout, "%-5s %-16s %s\n", strings.ToUpper(string(check.Level)), check.Name, check.Message)
		}
		fmt.Fprintf(stdout, "\nSummary: %s\n", result.Summary())
	}

	if failures > 0 {
		return fmt.Errorf("weld doctor: %d check(s) failed", failures)
	}
	return nil
}
