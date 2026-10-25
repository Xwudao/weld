package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAddJobSubcommand(t *testing.T) {
	root := t.TempDir()
	if _, err := run(t, "new", "demo", "--module", "example.com/demo", "--dir", root); err != nil {
		t.Fatalf("new: %v", err)
	}
	dir := filepath.Join(root, "demo")
	path := filepath.Join(dir, "internal", "jobs", "daily_report.go")

	if _, err := run(t, "add", "job", "daily_report", "--dir", dir); err == nil || !strings.Contains(err.Error(), "--with-dependencies") {
		t.Fatalf("add job without cron error = %v, want a missing-requirement error", err)
	}
	if _, err := run(t, "add", "job", "daily_report", "--schedule", "nightly", "--with-dependencies", "--dir", dir); err == nil || !strings.Contains(err.Error(), "invalid job schedule") {
		t.Fatalf("add job --schedule nightly error = %v, want a schedule error", err)
	}

	out, err := run(t, "add", "job", "daily_report", "--schedule", "0 3 * * *", "--with-dependencies", "--dir", dir, "--dry-run")
	if err != nil {
		t.Fatalf("add job --dry-run: %v", err)
	}
	if !strings.Contains(out, "dry run") || !strings.Contains(out, "add internal/jobs/daily_report.go") {
		t.Fatalf("dry-run output = %q", out)
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Fatal("a dry run wrote the job")
	}

	out, err = run(t, "add", "job", "daily_report", "--schedule", "0 3 * * *", "--with-dependencies", "--dir", dir)
	if err != nil {
		t.Fatalf("add job: %v", err)
	}
	if !strings.Contains(out, "add internal/jobs/daily_report_test.go") || !strings.Contains(out, "jobs.DailyReportSchedule") {
		t.Fatalf("add job output = %q", out)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read job: %v", err)
	}
	if !strings.Contains(string(body), `cron.Schedule("0 3 * * *")`) {
		t.Fatalf("job does not carry the requested schedule:\n%s", body)
	}

	out, err = run(t, "add", "job", "daily_report", "--dir", dir)
	if err != nil {
		t.Fatalf("repeat add job: %v", err)
	}
	if !strings.Contains(out, "already installed") {
		t.Fatalf("repeat add job output = %q", out)
	}
}
