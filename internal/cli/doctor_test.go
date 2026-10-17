package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestDoctorReportsAHealthyProject proves the JSON surface and the successful
// exit path.
func TestDoctorReportsAHealthyProject(t *testing.T) {
	root := t.TempDir()
	if _, err := run(t, "new", "demo", "--module", "example.com/demo", "--dir", root); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "demo")
	out, err := run(t, "doctor", "--json", "--dir", dir)
	if err != nil {
		t.Fatalf("doctor: %v\n%s", err, out)
	}
	var report struct {
		Checks []struct {
			Name  string `json:"name"`
			Level string `json:"level"`
		} `json:"checks"`
		Failures int `json:"failures"`
		Warnings int `json:"warnings"`
	}
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("doctor output is not JSON: %v\n%s", err, out)
	}
	if report.Failures != 0 || len(report.Checks) == 0 {
		t.Fatalf("report = %+v", report)
	}
}

// TestDoctorFailsOnMissingProject proves a failed check exits non-zero and is
// still reported as JSON.
func TestDoctorFailsOnMissingProject(t *testing.T) {
	dir := t.TempDir()
	out, err := run(t, "doctor", "--json", "--dir", dir)
	if err == nil {
		t.Fatal("doctor accepted a non-project")
	}
	var report struct {
		Failures int `json:"failures"`
		Checks   []struct {
			Level string `json:"level"`
		} `json:"checks"`
	}
	if jsonErr := json.Unmarshal([]byte(out), &report); jsonErr != nil {
		t.Fatalf("doctor output is not JSON: %v\n%s", jsonErr, out)
	}
	if report.Failures == 0 || len(report.Checks) == 0 || report.Checks[0].Level != "fail" {
		t.Fatalf("report = %+v", report)
	}
}

// TestDoctorWarnsOnDriftWithoutFailing proves a user edit is advisory.
func TestDoctorWarnsOnDriftWithoutFailing(t *testing.T) {
	root := t.TempDir()
	if _, err := run(t, "new", "demo", "--module", "example.com/demo", "--dir", root); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "demo")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("user notes\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := run(t, "doctor", "--json", "--dir", dir)
	if err != nil {
		t.Fatalf("doctor: %v\n%s", err, out)
	}
	var report struct {
		Warnings int `json:"warnings"`
	}
	if jsonErr := json.Unmarshal([]byte(out), &report); jsonErr != nil {
		t.Fatal(jsonErr)
	}
	if report.Warnings == 0 {
		t.Fatalf("drift was not reported: %s", out)
	}
}
