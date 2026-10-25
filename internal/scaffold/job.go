package scaffold

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/Xwudao/weld/internal/project"
	"github.com/Xwudao/weld/internal/template"
)

const (
	// jobDir is the package `weld add job` writes into, relative to the project
	// root. It is separate from the cron capability's own internal/cron package,
	// which weld owns and may rewrite on upgrade.
	jobDir = "internal/jobs"
	// DefaultJobSchedule is the schedule a job gets when none is given: hourly,
	// on the hour, in the scheduler's location.
	DefaultJobSchedule = "0 * * * *"
)

// ValidateJobName reports why a job name is unusable. The name is the <name>.go
// file, the registry name and, in CamelCase, the job function, so it is
// lower_snake_case (daily_report -> DailyReport).
func ValidateJobName(name string) error {
	if !enumValuePattern.MatchString(name) {
		return fmt.Errorf("invalid job name %q: use lower_snake_case starting with a letter, for example daily_report", name)
	}
	return checkFileNameSuffix("job", name)
}

// validateJobSchedule checks the shape of a schedule: a five-field cron
// expression, or "@every <duration>", the interval form cron.Every writes. The
// scheduler parses it for real when the job is registered (and the generated
// test registers it), so this only catches the obvious mistakes before any file
// is written, and refuses a quote that would break the generated Go literal.
func validateJobSchedule(schedule string) error {
	if strings.ContainsAny(schedule, "\"`\\\n") {
		return fmt.Errorf("invalid job schedule %q: it must not contain quotes, backslashes or newlines", schedule)
	}
	if interval, ok := strings.CutPrefix(schedule, "@every "); ok {
		if duration, err := time.ParseDuration(interval); err != nil || duration < time.Second {
			return fmt.Errorf("invalid job schedule %q: @every takes a Go duration of at least one second, for example \"@every 5m\"", schedule)
		}
		return nil
	}
	if fields := strings.Fields(schedule); len(fields) != 5 {
		return fmt.Errorf("invalid job schedule %q: use five fields, \"minute hour day-of-month month day-of-week\" (for example \"0 3 * * *\")", schedule)
	}
	return nil
}

// AddJob plans a scheduled job named name: internal/jobs/<name>.go with the job
// function, its registry name and its schedule, and a test that registers it on a
// real scheduler (which parses the schedule) and runs it once.
//
// The files are project-owned seams, written once and never rewritten. The job
// is not registered automatically: registration belongs in the project-owned
// internal/di/cron_jobs.go, which carries no marker region weld may patch, so the
// plan notes the s.Register call to add there instead.
//
// An empty schedule is DefaultJobSchedule. The cron capability (and its
// http/loom/config chain) must be installed first; a missing requirement is an
// error unless Request.WithDependencies is set.
func AddJob(req Request, name, schedule string) (*Result, error) {
	if err := ValidateJobName(name); err != nil {
		return nil, err
	}
	schedule = strings.Join(strings.Fields(schedule), " ")
	if schedule == "" {
		schedule = DefaultJobSchedule
	}
	if err := validateJobSchedule(schedule); err != nil {
		return nil, err
	}
	manifest, err := project.Load(req.Dir)
	if err != nil {
		return nil, err
	}

	path := filepath.ToSlash(filepath.Join(jobDir, name+".go"))
	testPath := filepath.ToSlash(filepath.Join(jobDir, name+"_test.go"))
	result := &Result{Dir: req.Dir}
	if manifest.Owns(path) {
		result.Notes = []string{fmt.Sprintf("job %s is already installed", name)}
		return result, nil
	}

	if err := checkCamelCollision(req.Dir, jobDir, name); err != nil {
		return nil, err
	}

	p := newPlanner(req, manifest, result)
	if err := p.requireCapability("cron", fmt.Sprintf("job %q", name)); err != nil {
		return nil, err
	}

	owner := "job:" + name
	vars := template.Vars{Name: manifest.Name, Module: manifest.Module}
	if err := p.planNewFile(path, owner, "", project.RoleSeam, template.Render(jobFile(name, schedule), vars)); err != nil {
		return nil, err
	}
	if err := p.planNewFile(testPath, owner, "", project.RoleSeam, template.Render(jobTestFile(name), vars)); err != nil {
		return nil, err
	}
	if err := p.finish(); err != nil {
		return nil, err
	}
	job := camel(name)
	result.Notes = append(result.Notes, fmt.Sprintf(
		"job %s is not registered yet: add `if err := s.Register(jobs.%sName, jobs.%sSchedule, jobs.%s); err != nil { return nil, err }` to NewCronJobs in internal/di/cron_jobs.go (import %q)",
		name, job, job, job, manifest.Module+"/"+jobDir))
	return result, nil
}

// jobFile renders the <name>.go job. Like a seeder it is built in code: only the
// identifiers and the schedule change between names.
func jobFile(name, schedule string) []byte {
	job := camel(name)
	return []byte(fmt.Sprintf(`package jobs

import (
	"context"

	"__module__/internal/cron"
)

// %[1]sName is the name the job is registered under. Names are unique in the
// scheduler, and it is the name the scheduler logs a failure or panic with.
const %[1]sName = %[2]q

// %[1]sSchedule is when %[1]s runs: a five-field "minute hour day-of-month
// month day-of-week" expression in the scheduler's location (cron.timezone in
// config.yml, UTC by default), or "@every <duration>". cron.Every, cron.DailyAt,
// cron.WeeklyOn and cron.MonthlyOn name the common cases.
const %[1]sSchedule = cron.Schedule(%[3]q)

// %[1]s is the %[2]s job. It is project-owned: weld writes it once and never
// rewrites it.
//
// Register it in NewCronJobs (internal/di/cron_jobs.go):
//
//	if err := s.Register(jobs.%[1]sName, jobs.%[1]sSchedule, jobs.%[1]s); err != nil {
//		return nil, err
//	}
//
// A returned error or a panic is logged by the scheduler, and a run that is still
// going when the next one is due is skipped. Return promptly when ctx is
// cancelled: Stop cancels it on shutdown. When the job needs a service, make it a
// method on that service instead and pass the service to NewCronJobs.
func %[1]s(ctx context.Context) error {
	_ = ctx
	return nil
}
`, job, name, schedule))
}

// jobTestFile renders the hermetic test for the <name>.go job.
func jobTestFile(name string) []byte {
	job := camel(name)
	return []byte(fmt.Sprintf(`package jobs

import (
	"context"
	"testing"

	"__module__/internal/cron"
	"__module__/internal/logging"
)

// Test%[1]sRegistersAndRuns registers the job the way internal/di/cron_jobs.go
// does, which parses its schedule, then runs it once directly, without waiting
// for the schedule. Extend it when the job gains behavior.
func Test%[1]sRegistersAndRuns(t *testing.T) {
	scheduler := cron.New(logging.Nop())
	if err := scheduler.Register(%[1]sName, %[1]sSchedule, %[1]s); err != nil {
		t.Fatalf("Register(%%q) = %%v, want nil", %[1]sName, err)
	}
	if err := %[1]s(context.Background()); err != nil {
		t.Fatalf("%[1]s = %%v, want nil", err)
	}
}
`, job))
}
