// Package doctor implements `weld doctor`: read-only checks of a project's
// manifest, configuration, managed files, toolchain and installed capabilities.
//
// It never writes, builds or connects to a service. The checks are deliberately
// about the facts weld can know locally: a missing manifest, a drifted managed
// file, an invalid weld.yml, a stale generated graph, a missing tool a capability
// needs, or an installed capability this weld no longer ships.
package doctor

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/Xwudao/weld/internal/db"
	"github.com/Xwudao/weld/internal/project"
	"github.com/Xwudao/weld/internal/projectcfg"
	"github.com/Xwudao/weld/internal/scaffold"
	"github.com/Xwudao/weld/internal/semver"
	"github.com/Xwudao/weld/internal/template"
)

// Level is a check's outcome.
type Level string

const (
	LevelOK   Level = "ok"
	LevelWarn Level = "warn"
	LevelFail Level = "fail"
)

// Check is one diagnostic.
type Check struct {
	Name    string `json:"name"`
	Level   Level  `json:"level"`
	Message string `json:"message"`
}

// Result is the outcome of a doctor run.
type Result struct {
	Dir    string  `json:"dir"`
	Checks []Check `json:"checks"`
}

// Request configures a doctor run.
type Request struct {
	// Dir is the project directory.
	Dir string
	// Catalog overrides the embedded capability catalog. A nil catalog uses the
	// one this weld ships.
	Catalog *template.Catalog
	// LookPath overrides tool lookup. A nil function uses exec.LookPath.
	LookPath func(string) (string, error)
	// LookupEnv overrides the process environment for the test-database check.
	// A nil function uses os.LookupEnv.
	LookupEnv func(string) (string, bool)
}

// Failed reports whether any check failed.
func (r *Result) Failed() bool {
	_, _, failures := r.Counts()
	return failures > 0
}

// Counts returns the number of ok, warning and failure checks.
func (r *Result) Counts() (ok, warnings, failures int) {
	for _, check := range r.Checks {
		switch check.Level {
		case LevelFail:
			failures++
		case LevelWarn:
			warnings++
		default:
			ok++
		}
	}
	return ok, warnings, failures
}

// Summary returns a one-line count of the run.
func (r *Result) Summary() string {
	ok, warnings, failures := r.Counts()
	return fmt.Sprintf("%d ok, %d warning(s), %d failure(s)", ok, warnings, failures)
}

// Run checks the project in req.Dir. It returns a result even when the project
// is not a weld project; a fatal load error becomes a failed check so the caller
// can print the rest of the report.
func Run(req Request) *Result {
	dir := strings.TrimSpace(req.Dir)
	if dir == "" {
		dir = "."
	}
	result := &Result{Dir: dir}
	add := func(name string, level Level, format string, args ...any) {
		result.Checks = append(result.Checks, Check{Name: name, Level: level, Message: fmt.Sprintf(format, args...)})
	}

	manifest, err := project.Load(dir)
	if err != nil {
		add("project", LevelFail, "%v", err)
		return result
	}
	add("project", LevelOK, "%s (%s)", manifest.Name, manifest.Module)

	cfg := checkConfigFile(dir, manifest, add)
	checkManagedFiles(dir, manifest, add)
	checkGoModule(dir, manifest, add)
	checkToolchain(dir, manifest, cfg, req.LookPath, add)
	checkCapabilities(manifest, req.Catalog, add)
	checkGeneratedFiles(dir, manifest, add)
	checkDatabaseFiles(dir, manifest, add)
	checkTestDatabase(dir, manifest, req.LookupEnv, add)
	return result
}

// checkConfigFile validates weld.yml and the generated application config, and
// returns the parsed weld.yml for the later toolchain/deploy checks.
func checkConfigFile(dir string, manifest *project.Manifest, add func(string, Level, string, ...any)) *projectcfg.Config {
	cfg, err := projectcfg.LoadOptional(dir)
	switch {
	case err != nil:
		add("weld.yml", LevelFail, "%v", err)
		cfg = nil
	case cfg == nil:
		add("weld.yml", LevelOK, "no weld.yml; defaults apply")
	default:
		add("weld.yml", LevelOK, "valid")
	}

	if !manifest.HasCapability("config") {
		return cfg
	}
	local := filepath.Join(dir, "config.yml")
	raw, readErr := os.ReadFile(local)
	switch {
	case errors.Is(readErr, os.ErrNotExist):
		add("config.yml", LevelWarn, "missing (git-ignored); restore it with `git checkout config.example.yml` or re-run the capability add")
	case readErr != nil:
		add("config.yml", LevelFail, "cannot read: %v", readErr)
	case !validYAML(raw):
		add("config.yml", LevelFail, "invalid YAML")
	default:
		add("config.yml", LevelOK, "present and valid YAML")
	}

	if _, err := os.Stat(filepath.Join(dir, "config.example.yml")); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			add("config.example.yml", LevelFail, "missing; restore it from version control")
		} else {
			add("config.example.yml", LevelFail, "cannot stat: %v", err)
		}
	}

	if gitignore, err := os.ReadFile(filepath.Join(dir, ".gitignore")); err == nil {
		if !ignoreRulePresent(string(gitignore), "config.yml") {
			add("secrets", LevelWarn, "config.yml is not git-ignored; it may contain local credentials")
		}
	}
	return cfg
}

// checkManagedFiles reports every recorded file that is missing or modified.
// Drift is a warning, not a failure: a user edit is legitimate, and weld's
// upgrade path reports it rather than overwriting it.
func checkManagedFiles(dir string, manifest *project.Manifest, add func(string, Level, string, ...any)) {
	drift := manifest.Drift(dir)
	if len(drift) == 0 {
		add("managed files", LevelOK, "%d file(s) match the manifest", len(manifest.Files))
		return
	}
	parts := make([]string, 0, len(drift))
	for _, item := range drift {
		parts = append(parts, item.Path+" ("+item.Reason+")")
	}
	if len(parts) > 8 {
		parts = append(parts[:8], fmt.Sprintf("and %d more", len(parts)-8))
	}
	add("managed files", LevelWarn, "%d file(s) differ: %s; run `weld upgrade --dry-run` or restore a missing local file", len(drift), strings.Join(parts, ", "))
}

// checkGoModule verifies that go.mod still declares the manifest's module path.
func checkGoModule(dir string, manifest *project.Manifest, add func(string, Level, string, ...any)) {
	raw, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			add("go.mod", LevelFail, "missing")
		} else {
			add("go.mod", LevelFail, "cannot read: %v", err)
		}
		return
	}
	module := goModulePath(string(raw))
	if module == "" {
		add("go.mod", LevelFail, "no module directive")
		return
	}
	if module != manifest.Module {
		add("go.mod", LevelFail, "module is %q, but weld.json records %q", module, manifest.Module)
		return
	}
	add("go.mod", LevelOK, module)
}

// checkToolchain verifies the commands the installed capabilities need.
func checkToolchain(dir string, manifest *project.Manifest, cfg *projectcfg.Config, lookPath func(string) (string, error), add func(string, Level, string, ...any)) {
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	if path, err := lookPath("go"); err != nil {
		add("go", LevelFail, "not found in PATH; the Go toolchain is required")
	} else {
		add("go", LevelOK, path)
	}

	if manifest.HasCapability("web") {
		missing := []string{}
		for _, name := range []string{"node", "pnpm"} {
			if _, err := lookPath(name); err != nil {
				missing = append(missing, name)
			}
		}
		if len(missing) > 0 {
			add("web toolchain", LevelWarn, "%s not found in PATH; `weld dev` and `weld build` need them", strings.Join(missing, " and "))
		} else {
			add("web toolchain", LevelOK, "node and pnpm found")
		}
	}

	dockerfile := "Dockerfile"
	if cfg != nil && strings.TrimSpace(cfg.Build.Docker.Dockerfile) != "" {
		dockerfile = cfg.Build.Docker.Dockerfile
	}
	_, dockerfileErr := os.Stat(filepath.Join(dir, dockerfile))
	dockerConfigured := cfg != nil && strings.TrimSpace(cfg.Build.Docker.Image) != ""
	if dockerfileErr == nil || dockerConfigured {
		if path, err := lookPath("docker"); err != nil {
			add("docker", LevelWarn, "Dockerfile or build.docker is configured, but docker is not in PATH")
		} else {
			add("docker", LevelOK, path)
		}
	}

	if cfg != nil {
		deploy := cfg.EffectiveDeploy()
		if deploy.Host != "" || deploy.RemoteUploadDir != "" || deploy.RemoteScript != "" {
			if err := deploy.Validate(); err != nil {
				add("deploy", LevelFail, "%v", err)
			} else {
				missing := []string{}
				for _, name := range []string{"scp", "ssh"} {
					if _, err := lookPath(name); err != nil {
						missing = append(missing, name)
					}
				}
				if len(missing) > 0 {
					add("deploy", LevelWarn, "%s not found in PATH; `weld deploy` needs them", strings.Join(missing, ", "))
				} else {
					add("deploy", LevelOK, "%s@%s", deploy.Host, deploy.Target.OS+"/"+deploy.Target.Arch)
				}
			}
		}
	}
}

// checkCapabilities reports installed capabilities this weld does not ship and
// ones whose recorded version is behind (or ahead of) the catalog.
func checkCapabilities(manifest *project.Manifest, catalog *template.Catalog, add func(string, Level, string, ...any)) {
	if catalog == nil {
		catalog = template.Load()
	}
	type installed struct {
		name    string
		version string
	}
	list := []installed{{manifest.Base.Name, manifest.Base.Version}}
	for _, capability := range manifest.Capabilities {
		list = append(list, installed{capability.Name, capability.Version})
	}
	behind := []string{}
	ahead := []string{}
	missing := []string{}
	for _, item := range list {
		capability, err := catalog.Get(item.name)
		if err != nil {
			missing = append(missing, item.name)
			continue
		}
		newer, err := semver.IsNewer(capability.Version, item.version)
		if err != nil {
			missing = append(missing, item.name+" (unparseable version "+item.version+")")
			continue
		}
		if newer {
			behind = append(behind, fmt.Sprintf("%s %s -> %s", item.name, item.version, capability.Version))
		}
		if recordedNewer, err := semver.IsNewer(item.version, capability.Version); err == nil && recordedNewer {
			ahead = append(ahead, fmt.Sprintf("%s %s", item.name, item.version))
		}
	}
	if len(missing) > 0 {
		add("capabilities", LevelWarn, "not shipped by this weld: %s; use a newer weld or remove the entry from weld.json", strings.Join(missing, ", "))
	}
	if len(behind) > 0 {
		sort.Strings(behind)
		add("capabilities", LevelWarn, "upgrade available: %s; run `weld upgrade`", strings.Join(behind, ", "))
	}
	if len(ahead) > 0 {
		sort.Strings(ahead)
		add("capabilities", LevelWarn, "recorded version is newer than this weld ships: %s; use a newer weld", strings.Join(ahead, ", "))
	}
	if len(missing) == 0 && len(behind) == 0 && len(ahead) == 0 {
		add("capabilities", LevelOK, "%d installed", len(list))
	}
}

// checkGeneratedFiles verifies the files a generator owns. It checks existence
// and, for enums, whether the generated methods are current.
func checkGeneratedFiles(dir string, manifest *project.Manifest, add func(string, Level, string, ...any)) {
	if manifest.HasCapability("loom") {
		missing := []string{}
		for _, path := range []string{"internal/di/di.go", "internal/di/loom_gen.go", filepath.Join("tools", "loom", "go.mod")} {
			if _, err := os.Stat(filepath.Join(dir, path)); err != nil {
				missing = append(missing, path)
			}
		}
		if len(missing) > 0 {
			add("generated", LevelFail, "missing %s; reinstall with `weld add loom`", strings.Join(missing, ", "))
		} else {
			add("generated", LevelOK, "Loom graph present")
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "internal", "enums")); err == nil {
		stale, err := scaffold.CheckEnums(scaffold.Request{Dir: dir})
		if err != nil {
			add("enums", LevelWarn, "cannot inspect generated enums: %v", err)
		} else if len(stale) > 0 {
			add("enums", LevelWarn, "generated files are out of date: %s; run `weld gen enum`", strings.Join(stale, ", "))
		} else {
			add("enums", LevelOK, "generated enum files are up to date")
		}
	}
}

// checkDatabaseFiles verifies the files the db commands need.
func checkDatabaseFiles(dir string, manifest *project.Manifest, add func(string, Level, string, ...any)) {
	if !manifest.HasCapability("db") {
		return
	}
	missing := []string{}
	for _, path := range []string{filepath.Join("db", "tools", "go.mod"), filepath.Join("db", "migrations")} {
		if _, err := os.Stat(filepath.Join(dir, path)); err != nil {
			missing = append(missing, path)
		}
	}
	if len(missing) > 0 {
		add("database", LevelFail, "missing %s; reinstall with `weld add db`", strings.Join(missing, ", "))
		return
	}
	add("database", LevelOK, "migration tool and migrations present")
}

// checkTestDatabase reports whether a test database is configured for the
// generated integration tests. It never connects: it resolves config.yml (or
// PG_TEST_DSN) and reports the source, never the dsn.
func checkTestDatabase(dir string, manifest *project.Manifest, lookupEnv func(string) (string, bool), add func(string, Level, string, ...any)) {
	if !manifest.HasCapability("db") {
		return
	}
	_, source, err := db.ResolveTestDSN(dir, "", lookupEnv)
	switch {
	case err == nil:
		add("database test", LevelOK, "configured from %s", source)
	case errors.Is(err, db.ErrTestDatabaseNotConfigured):
		add("database test", LevelWarn, "no test database configured; set database_test.name in config.yml or %s", db.EnvTestDatabaseURL)
	default:
		add("database test", LevelFail, "%v", err)
	}
}

// goModulePath returns the path from a go.mod module directive.
func goModulePath(raw string) string {
	for _, line := range strings.Split(raw, "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) == 2 && fields[0] == "module" {
			return strings.Trim(fields[1], `"`)
		}
	}
	return ""
}

// ignoreRulePresent reports whether a .gitignore carries name as an exact line,
// including the common leading-slash form. It deliberately does not implement
// full gitignore semantics.
func ignoreRulePresent(content, name string) bool {
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "/")
		if line == name || line == "/"+name {
			return true
		}
	}
	return false
}

// validYAML parses raw as YAML without retaining or echoing its content.
func validYAML(raw []byte) bool {
	var value any
	return yaml.Unmarshal(raw, &value) == nil
}
