// Package db implements the `weld db` commands: it runs the project's pinned
// goose migration tool against the database described by the project's own
// config.yml (or DATABASE_URL), and creates timestamped migration files.
//
// The generated project already ships a nested db/tools module that pins goose,
// and a Makefile that runs it. These commands add the same operations without
// requiring the caller to export DATABASE_URL or cd into db/tools by hand.
// Planning and execution are separated so --dry-run prints the exact command
// without touching a database; the dsn is never printed or logged.
package db

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/Xwudao/weld/internal/project"
)

const (
	// ToolsDir is the nested module that pins the goose migration tool.
	ToolsDir = "db/tools"
	// MigrationsDir is the project's migration directory.
	MigrationsDir = "db/migrations"
	// gooseTable is the version table the Makefile and these commands share.
	gooseTable = "goose_db_version"
)

// Environment names read by the dsn resolver. They match the generated config
// loader, so `weld db` and the application read one set of variables.
const (
	EnvDatabaseURL = "DATABASE_URL"
	EnvDBHost      = "DB_HOST"
	EnvDBPort      = "DB_PORT"
	EnvDBUser      = "DB_USER"
	EnvDBPassword  = "DB_PASSWORD"
	EnvDBName      = "DB_NAME"
)

// DefaultDBHost and DefaultDBPort match the generated configuration defaults.
const (
	DefaultDBHost = "localhost"
	DefaultDBPort = 5432
)

// Action is one goose migration action.
type Action string

const (
	ActionUp     Action = "up"
	ActionStatus Action = "status"
	ActionDown   Action = "down"
)

// Runner runs one external command. It is the injection seam: a test records the
// command instead of invoking go.
type Runner func(name, dir string, env []string, args ...string) error

// Request describes one migration action.
type Request struct {
	// Dir is the project directory.
	Dir string
	// ConfigPath overrides config.yml. An empty value uses config.yml in Dir.
	ConfigPath string
	// Action is up, status or down.
	Action Action
	// Confirm is required for down: it reverts exactly one migration.
	Confirm bool
	// DryRun plans the command without running it.
	DryRun bool
	// Stdout and Stderr receive the command's output.
	Stdout io.Writer
	Stderr io.Writer
	// Runner overrides command execution. A nil Runner runs the real command.
	Runner Runner
	// LookupEnv overrides the process environment. A nil function uses os.LookupEnv.
	LookupEnv func(string) (string, bool)
}

// Step is one command in a migration plan.
type Step struct {
	Name string
	Dir  string
	Env  []string
	Args []string
	// Display is the command with the credential-bearing dsn replaced by
	// $DATABASE_URL. It is the only form safe to print.
	Display string
}

// String renders the step as it would be typed at a shell. It never includes the
// dsn.
func (s Step) String() string {
	if s.Dir != "" {
		return "cd " + s.Dir + " && " + s.Display
	}
	return s.Display
}

// Result reports what a migration action produced (or planned).
type Result struct {
	Action Action
	Step   Step
	// Source names where the dsn came from: DATABASE_URL or the config path.
	Source string
	// Applied reports whether the command ran. It is false for a dry run.
	Applied bool
}

// Migrate validates the project and runs one goose action.
func Migrate(req Request) (*Result, error) {
	dir := strings.TrimSpace(req.Dir)
	if dir == "" {
		return nil, errors.New("weld db: project directory is required")
	}
	switch req.Action {
	case ActionUp, ActionStatus, ActionDown:
	default:
		return nil, fmt.Errorf("weld db: unknown migration action %q (want up, status or down)", req.Action)
	}
	if req.Action == ActionDown && !req.Confirm {
		return nil, errors.New("weld db migrate down reverts the latest migration; re-run with --yes or CONFIRM=1")
	}

	manifest, err := project.Load(dir)
	if err != nil {
		return nil, fmt.Errorf("weld db: %w", err)
	}
	if !manifest.HasCapability("db") {
		return nil, fmt.Errorf("weld db: the db capability is not installed in %s (run: weld add db)", dir)
	}
	for _, path := range []string{filepath.Join(ToolsDir, "go.mod"), MigrationsDir} {
		if _, err := os.Stat(filepath.Join(dir, path)); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil, fmt.Errorf("weld db: %s is missing; reinstall with: weld add db", path)
			}
			return nil, fmt.Errorf("weld db: stat %s: %w", path, err)
		}
	}

	dsn, source, err := ResolveDSN(dir, req.ConfigPath, req.LookupEnv)
	if err != nil {
		return nil, err
	}
	step := gooseStep(filepath.Join(dir, ToolsDir), dsn, req.Action)
	result := &Result{Action: req.Action, Step: step, Source: source}
	if req.DryRun {
		return result, nil
	}

	runner := req.Runner
	if runner == nil {
		runner = commandRunner(req.Stdout, req.Stderr)
	}
	if err := runner(step.Name, step.Dir, step.Env, step.Args...); err != nil {
		return nil, fmt.Errorf("weld db migrate %s: %w", req.Action, err)
	}
	result.Applied = true
	return result, nil
}

// gooseStep builds the pinned-tool command. The dsn is an argv element because
// that is how goose receives a database target; it is deliberately omitted from
// Display so output and dry runs never echo a credential.
func gooseStep(toolsDir, dsn string, action Action) Step {
	args := []string{
		"tool", "goose",
		"-dir", "../migrations",
		"-table", gooseTable,
		"postgres", dsn,
		string(action),
	}
	display := []string{"go", "tool", "goose", "-dir", "../migrations", "-table", gooseTable, "postgres", "$DATABASE_URL", string(action)}
	return Step{
		Name:    "go",
		Dir:     toolsDir,
		Env:     []string{"GOWORK=off", "GOFLAGS=-mod=mod"},
		Args:    args,
		Display: strings.Join(display, " "),
	}
}

// ResolveDSN returns the PostgreSQL dsn for the project.
//
// Precedence matches the generated application: DATABASE_URL first, then the
// config file's explicit dsn, then the individual database fields with the
// DB_* environment overrides applied on top. An empty DATABASE_URL is ignored
// rather than treated as a target. The returned source is safe to print; the dsn
// is not.
func ResolveDSN(dir, configPath string, lookupEnv func(string) (string, bool)) (dsn, source string, err error) {
	if lookupEnv == nil {
		lookupEnv = os.LookupEnv
	}
	if value, ok := lookupEnv(EnvDatabaseURL); ok {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed, EnvDatabaseURL, nil
		}
	}

	path := strings.TrimSpace(configPath)
	if path == "" {
		path = "config.yml"
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(dir, path)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", "", fmt.Errorf("weld db: %s not found; pass --config or set %s", path, EnvDatabaseURL)
		}
		return "", "", fmt.Errorf("weld db: read %s: %w", path, err)
	}
	var file struct {
		Database struct {
			Host     string `yaml:"host"`
			Port     int    `yaml:"port"`
			User     string `yaml:"user"`
			Password string `yaml:"password"`
			Name     string `yaml:"name"`
			DSN      string `yaml:"dsn"`
		} `yaml:"database"`
	}
	if err := yaml.Unmarshal(raw, &file); err != nil {
		// The YAML error can quote the offending line, which may hold a password.
		return "", "", fmt.Errorf("weld db: %s: invalid YAML", path)
	}
	db := file.Database

	if value, ok := lookupEnv(EnvDBHost); ok {
		db.Host = strings.TrimSpace(value)
	}
	if value, ok := lookupEnv(EnvDBPort); ok {
		port, parseErr := strconv.Atoi(strings.TrimSpace(value))
		if parseErr != nil {
			return "", "", fmt.Errorf("weld db: %s is not a number", EnvDBPort)
		}
		db.Port = port
	}
	if value, ok := lookupEnv(EnvDBUser); ok {
		db.User = strings.TrimSpace(value)
	}
	if value, ok := lookupEnv(EnvDBPassword); ok {
		db.Password = value
	}
	if value, ok := lookupEnv(EnvDBName); ok {
		db.Name = strings.TrimSpace(value)
	}

	if explicit := strings.TrimSpace(db.DSN); explicit != "" {
		return explicit, path, nil
	}
	if strings.TrimSpace(db.Host) == "" {
		db.Host = DefaultDBHost
	}
	if db.Port == 0 {
		db.Port = DefaultDBPort
	}
	switch {
	case db.Port <= 0:
		return "", "", errors.New("weld db: database.port must be positive")
	case strings.TrimSpace(db.User) == "":
		return "", "", fmt.Errorf("weld db: database.user is required (set it in %s, %s or %s)", path, EnvDBUser, EnvDatabaseURL)
	case strings.TrimSpace(db.Name) == "":
		return "", "", fmt.Errorf("weld db: database.name is required (set it in %s or %s)", path, EnvDBName)
	}
	return (&url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(db.User, db.Password),
		Host:   net.JoinHostPort(db.Host, strconv.Itoa(db.Port)),
		Path:   db.Name,
	}).String(), path, nil
}

// commandRunner returns the default Runner, which streams the command's output,
// inherits the process environment and overlays step env.
func commandRunner(stdout, stderr io.Writer) Runner {
	return func(name, dir string, env []string, args ...string) error {
		cmd := exec.Command(name, args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), env...)
		cmd.Stdout = stdout
		cmd.Stderr = stderr
		cmd.Stdin = os.Stdin
		return cmd.Run()
	}
}

// CreateRequest describes `weld db create <name>`.
type CreateRequest struct {
	// Dir is the project directory.
	Dir string
	// Name is the migration name, lower_snake_case.
	Name string
	// DryRun prints the planned path without writing.
	DryRun bool
	// Now overrides the clock used for the timestamp prefix. A nil clock uses
	// time.Now.
	Now func() time.Time
}

// CreateResult reports the created (or planned) migration file.
type CreateResult struct {
	// Path is relative to the project directory.
	Path string
	// Written reports whether the file was created.
	Written bool
}

// migrationNamePattern matches a migration name: lower_snake_case, suitable for
// the filename suffix after the timestamp.
var migrationNamePattern = regexp.MustCompile(`^[a-z][a-z0-9]*(_[a-z0-9]+)*$`)

// Create writes a timestamped goose migration skeleton.
func Create(req CreateRequest) (*CreateResult, error) {
	dir := strings.TrimSpace(req.Dir)
	if dir == "" {
		return nil, errors.New("weld db: project directory is required")
	}
	name := strings.TrimSpace(req.Name)
	if !migrationNamePattern.MatchString(name) {
		return nil, fmt.Errorf("invalid migration name %q: use lower_snake_case starting with a letter", name)
	}
	manifest, err := project.Load(dir)
	if err != nil {
		return nil, fmt.Errorf("weld db: %w", err)
	}
	if !manifest.HasCapability("db") {
		return nil, fmt.Errorf("weld db: the db capability is not installed in %s (run: weld add db)", dir)
	}
	migrationsDir := filepath.Join(dir, MigrationsDir)
	if info, err := os.Stat(migrationsDir); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("weld db: %s is missing; reinstall with: weld add db", MigrationsDir)
		}
		return nil, fmt.Errorf("weld db: stat %s: %w", MigrationsDir, err)
	} else if !info.IsDir() {
		return nil, fmt.Errorf("weld db: %s is not a directory", MigrationsDir)
	}
	if matches, err := filepath.Glob(filepath.Join(migrationsDir, "*_"+name+".sql")); err != nil {
		return nil, fmt.Errorf("weld db: inspect %s: %w", MigrationsDir, err)
	} else if len(matches) > 0 {
		return nil, fmt.Errorf("weld db: migration %q already exists at %s", name, matches[0])
	}

	now := req.Now
	if now == nil {
		now = time.Now
	}
	target := filepath.Join(MigrationsDir, now().UTC().Format("20060102150405")+"_"+name+".sql")
	result := &CreateResult{Path: target}
	if req.DryRun {
		return result, nil
	}
	absolute := filepath.Join(dir, target)
	file, err := os.OpenFile(absolute, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return nil, fmt.Errorf("weld db: %s already exists; try again in a moment", target)
		}
		return nil, fmt.Errorf("weld db: create %s: %w", target, err)
	}
	content := "-- +goose Up\n-- Write the forward migration here.\n\n-- +goose Down\n-- Write the reverse migration here.\n"
	if _, err := file.WriteString(content); err != nil {
		_ = file.Close()
		_ = os.Remove(absolute)
		return nil, fmt.Errorf("weld db: write %s: %w", target, err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(absolute)
		return nil, fmt.Errorf("weld db: close %s: %w", target, err)
	}
	result.Written = true
	return result, nil
}
