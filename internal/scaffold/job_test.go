package scaffold

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Xwudao/weld/internal/project"
)

// addJob plans and applies `weld add job <name>` with schedule.
func addJob(t *testing.T, dir, name, schedule string) *Result {
	t.Helper()
	result, err := AddJob(Request{Dir: dir, Catalog: newCatalog()}, name, schedule)
	if err != nil {
		t.Fatalf("AddJob %s: %v", name, err)
	}
	if err := result.Apply(); err != nil {
		t.Fatalf("Apply add job %s: %v", name, err)
	}
	return result
}

func TestValidateJobName(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"daily_report", "cleanup", "sync2"} {
		if err := ValidateJobName(name); err != nil {
			t.Errorf("ValidateJobName(%q) = %v, want nil", name, err)
		}
	}
	for _, name := range []string{"", "DailyReport", "daily-report", "daily__report", "2fa", "report_test", "sync_windows"} {
		if err := ValidateJobName(name); err == nil {
			t.Errorf("ValidateJobName(%q) = nil, want an error", name)
		}
	}
}

func TestValidateJobSchedule(t *testing.T) {
	t.Parallel()
	for _, schedule := range []string{"0 * * * *", "*/5 9-17 * * 1-5", "@every 5m", "@every 1h30m"} {
		if err := validateJobSchedule(schedule); err != nil {
			t.Errorf("validateJobSchedule(%q) = %v, want nil", schedule, err)
		}
	}
	for _, schedule := range []string{"0 * * *", "0 0 * * * *", "@every", "@every soon", "@every 500ms", `0 "3" * * *`, "@daily"} {
		if err := validateJobSchedule(schedule); err == nil {
			t.Errorf("validateJobSchedule(%q) = nil, want an error", schedule)
		}
	}
}

func TestAddJobWritesSeamsAndNotesRegistration(t *testing.T) {
	t.Parallel()
	dir := create(t, t.TempDir())
	add(t, dir, "cron")
	wiringBefore := readFile(t, filepath.Join(dir, "internal/di/cron_jobs.go"))

	result := addJob(t, dir, "daily_report", "  0   3 * * * ")

	body := readFile(t, filepath.Join(dir, "internal/jobs/daily_report.go"))
	for _, want := range []string{
		`const DailyReportName = "daily_report"`,
		`const DailyReportSchedule = cron.Schedule("0 3 * * *")`,
		"func DailyReport(ctx context.Context) error",
		`"` + testModule + `/internal/cron"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("daily_report.go lacks %q:\n%s", want, body)
		}
	}
	test := readFile(t, filepath.Join(dir, "internal/jobs/daily_report_test.go"))
	if !strings.Contains(test, "scheduler.Register(DailyReportName, DailyReportSchedule, DailyReport)") {
		t.Fatalf("daily_report_test.go does not register the job:\n%s", test)
	}
	manifest := mustLoad(t, dir)
	for _, path := range []string{"internal/jobs/daily_report.go", "internal/jobs/daily_report_test.go"} {
		file, ok := manifest.FileEntry(path)
		if !ok {
			t.Fatalf("%s is not recorded in the manifest", path)
		}
		if file.Capability != "job:daily_report" || file.Role != project.RoleSeam {
			t.Fatalf("%s recorded as %s/%s, want job:daily_report/seam", path, file.Capability, file.Role)
		}
	}
	if readFile(t, filepath.Join(dir, "internal/di/cron_jobs.go")) != wiringBefore {
		t.Fatal("add job rewrote internal/di/cron_jobs.go")
	}
	if notes := strings.Join(result.Notes, "\n"); !strings.Contains(notes, "s.Register(jobs.DailyReportName, jobs.DailyReportSchedule, jobs.DailyReport)") || !strings.Contains(notes, testModule+"/internal/jobs") {
		t.Fatalf("notes do not explain the registration:\n%s", notes)
	}
}

func TestAddJobDefaultsToHourly(t *testing.T) {
	t.Parallel()
	dir := create(t, t.TempDir())
	add(t, dir, "cron")
	addJob(t, dir, "cleanup", "")
	if body := readFile(t, filepath.Join(dir, "internal/jobs/cleanup.go")); !strings.Contains(body, `cron.Schedule("`+DefaultJobSchedule+`")`) {
		t.Fatalf("cleanup.go does not use the default schedule:\n%s", body)
	}
}

func TestAddJobIsIdempotent(t *testing.T) {
	t.Parallel()
	dir := create(t, t.TempDir())
	add(t, dir, "cron")
	addJob(t, dir, "cleanup", "")
	path := filepath.Join(dir, "internal/jobs/cleanup.go")
	edited := readFile(t, path) + "\n// edited by the project\n"
	if err := os.WriteFile(path, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}

	// A repeat with a different schedule is still a no-op: the file is the
	// project's now, so the schedule is changed there.
	result, err := AddJob(Request{Dir: dir, Catalog: newCatalog()}, "cleanup", "0 0 * * *")
	if err != nil {
		t.Fatalf("second AddJob: %v", err)
	}
	if len(result.Operations) != 0 || !strings.Contains(strings.Join(result.Notes, "\n"), "already installed") {
		t.Fatalf("second AddJob = %d operations, notes %q; want a no-op", len(result.Operations), result.Notes)
	}
	if readFile(t, path) != edited {
		t.Fatal("a repeat add changed the project's job")
	}
}

func TestAddJobRejectsBadScheduleBeforePlanning(t *testing.T) {
	t.Parallel()
	dir := create(t, t.TempDir())
	if _, err := AddJob(Request{Dir: dir, Catalog: newCatalog(), WithDependencies: true}, "cleanup", "every hour"); err == nil || !strings.Contains(err.Error(), "invalid job schedule") {
		t.Fatalf("AddJob with a bad schedule = %v, want a schedule error", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "internal/jobs")); !os.IsNotExist(statErr) {
		t.Fatal("a rejected AddJob wrote files")
	}
}

func TestAddJobRequiresCron(t *testing.T) {
	t.Parallel()
	dir := create(t, t.TempDir())

	_, err := AddJob(Request{Dir: dir, Catalog: newCatalog()}, "cleanup", "")
	missing, ok := err.(*MissingRequirementsError)
	if !ok {
		t.Fatalf("AddJob error = %v, want *MissingRequirementsError", err)
	}
	if got, want := strings.Join(missing.Missing, ","), "config,loom,http,cron"; got != want {
		t.Fatalf("missing = %q, want %q", got, want)
	}
}

func TestAddJobWithDependenciesInstallsChain(t *testing.T) {
	t.Parallel()
	dir := create(t, t.TempDir())

	result, err := AddJob(Request{Dir: dir, Catalog: newCatalog(), WithDependencies: true}, "cleanup", "")
	if err != nil {
		t.Fatalf("AddJob --with-dependencies: %v", err)
	}
	if got, want := strings.Join(result.Installed, ","), "config,loom,http,cron"; got != want {
		t.Fatalf("installed = %q, want %q", got, want)
	}
	if err := result.Apply(); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	for _, path := range []string{"internal/cron/cron.go", "internal/jobs/cleanup.go"} {
		if _, err := os.Stat(filepath.Join(dir, path)); err != nil {
			t.Errorf("expected %s: %v", path, err)
		}
	}
}

// TestAddJobRegisteredProjectBuildsAndTests follows the plan's note — it
// registers the job in the cron seam by hand — and proves the result compiles,
// is gofmt-clean and passes its tests, including the schedule parse.
func TestAddJobRegisteredProjectBuildsAndTests(t *testing.T) {
	t.Parallel()
	loomToolAvailable(t)
	dir := create(t, t.TempDir())
	add(t, dir, "cron")
	addJob(t, dir, "daily_report", "*/15 9-17 * * 1-5")
	addJob(t, dir, "heartbeat", "@every 30s")

	seamPath := filepath.Join(dir, "internal/di/cron_jobs.go")
	seam := readFile(t, seamPath)
	seam = strings.Replace(seam, `import "`+testModule+`/internal/cron"`,
		"import (\n\t\""+testModule+"/internal/cron\"\n\t\""+testModule+"/internal/jobs\"\n)", 1)
	seam = strings.Replace(seam, "(*CronJobs, error) {\n\treturn",
		"(*CronJobs, error) {\n\tif err := s.Register(jobs.DailyReportName, jobs.DailyReportSchedule, jobs.DailyReport); err != nil {\n\t\treturn nil, err\n\t}\n\tif err := s.Register(jobs.HeartbeatName, jobs.HeartbeatSchedule, jobs.Heartbeat); err != nil {\n\t\treturn nil, err\n\t}\n\treturn", 1)
	if !strings.Contains(seam, "jobs.HeartbeatName") || !strings.Contains(seam, "/internal/jobs\"") {
		t.Fatalf("could not register the jobs in the shipped seam:\n%s", seam)
	}
	if err := os.WriteFile(seamPath, []byte(seam), 0o644); err != nil {
		t.Fatal(err)
	}
	if !buildAndTestGeneratedProject(t, dir) {
		t.Skip("cannot resolve the generated project's dependencies (network/module cache unavailable)")
	}
}
