package projectcfg

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// HookConfig is the lifecycle-hook configuration from weld.yml. Hooks run
// user-declared commands at named points in a weld command, so a project can
// extend `weld build`, `weld dev`, `weld deploy` and `weld seed` with its own
// tooling without weld learning about that tooling.
//
// Hooks are user-owned configuration: `weld add` never touches weld.yml and
// weld never records hooks in weld.json.
//
// The command keys are explicit fields rather than a free-form map so an
// unknown command (a typo like `buld`) is a load-time error instead of a silently
// ignored hook. Adding a hookable command means adding a field here and an Event
// in internal/hook.
type HookConfig struct {
	// Enabled is the global switch. It is a pointer so an omitted value reads as
	// true: declaring a hook is opting in, and only `enabled: false` turns the
	// whole set off. It is also overridden per run by --no-hooks / WELD_NO_HOOKS.
	Enabled *bool      `yaml:"enabled"`
	Build   HookPhases `yaml:"build"`
	Dev     HookPhases `yaml:"dev"`
	Deploy  HookPhases `yaml:"deploy"`
	Seed    HookPhases `yaml:"seed"`
}

// IsEnabled reports whether hooks are enabled. An omitted value defaults to
// true.
func (h HookConfig) IsEnabled() bool {
	return h.Enabled == nil || *h.Enabled
}

// HookPhases is the pre/post pair one command exposes.
type HookPhases struct {
	Pre  []HookStep `yaml:"pre"`
	Post []HookStep `yaml:"post"`
}

// HookStep is one command run at a hook point.
type HookStep struct {
	// Name labels the step in logs and errors. It is optional.
	Name string `yaml:"name"`
	// Run is the command. A scalar is a shell command line, a sequence is an
	// argv vector. It is required.
	Run HookCommand `yaml:"run"`
	// Dir is the working directory, relative to the project root. Empty means
	// the project root.
	Dir string `yaml:"dir"`
	// Env overrides the inherited process environment for this step.
	Env map[string]string `yaml:"env"`
	// Platforms restricts the step to an operating system. Empty runs
	// everywhere. Accepted: linux, darwin (or macos/mac), windows.
	Platforms []string `yaml:"platforms"`
	// When is an optional set of conditions; every one must hold.
	When *HookWhen `yaml:"when"`
	// Timeout bounds the step. Zero leaves it unbounded.
	Timeout time.Duration `yaml:"timeout"`
	// ContinueOnError turns a failure into a warning that does not abort the
	// command. The default is fail-fast.
	ContinueOnError bool `yaml:"continue_on_error"`
}

// HookWhen is a set of conditions; every one must hold for the step to run.
type HookWhen struct {
	// Capability requires each named capability to be installed in the project.
	Capability []string `yaml:"capability"`
	// Env requires each variable to equal the given value in the process
	// environment.
	Env map[string]string `yaml:"env"`
	// Files requires each path (relative to the project root) to exist.
	Files []string `yaml:"files"`
}

// HookCommand is a hook command. A YAML scalar is a shell command line, run
// through the platform shell so pipes and redirection work; a YAML sequence is
// an argv vector run directly, with no shell and no word splitting. The latter
// is the portable form and is preferred for simple commands.
type HookCommand struct {
	// Shell is the scalar form: a command line run through the platform shell.
	Shell string
	// Argv is the sequence form: an argv vector run directly.
	Argv []string
}

// UnmarshalYAML accepts either a scalar (shell) or a sequence (argv).
func (c *HookCommand) UnmarshalYAML(value *yaml.Node) error {
	switch value.Kind {
	case yaml.ScalarNode:
		var s string
		if err := value.Decode(&s); err != nil {
			return err
		}
		c.Shell, c.Argv = s, nil
		return nil
	case yaml.SequenceNode:
		var argv []string
		if err := value.Decode(&argv); err != nil {
			return err
		}
		c.Argv, c.Shell = argv, ""
		return nil
	default:
		return fmt.Errorf("run must be a string (shell) or a list (argv), got %s", yamlKind(value.Kind))
	}
}

// String renders the command for a plan or a log line.
func (c HookCommand) String() string {
	if len(c.Argv) > 0 {
		return strings.Join(c.Argv, " ")
	}
	return c.Shell
}

// yamlKind names a YAML node kind for an error message.
func yamlKind(kind yaml.Kind) string {
	switch kind {
	case yaml.DocumentNode:
		return "a document"
	case yaml.SequenceNode:
		return "a list"
	case yaml.MappingNode:
		return "a mapping"
	case yaml.ScalarNode:
		return "a scalar"
	case yaml.AliasNode:
		return "an alias"
	default:
		return "an unknown value"
	}
}

// NormalizePlatform maps a configured platform name to a GOOS. It accepts the
// documented spellings and reports whether the name was known.
func NormalizePlatform(name string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "linux":
		return "linux", true
	case "darwin", "macos", "mac":
		return "darwin", true
	case "windows":
		return "windows", true
	default:
		return "", false
	}
}

// MatchPlatform reports whether a step restricted to platforms runs on goos. An
// empty list runs everywhere.
func MatchPlatform(platforms []string, goos string) bool {
	if len(platforms) == 0 {
		return true
	}
	for _, p := range platforms {
		if normalized, ok := NormalizePlatform(p); ok && normalized == goos {
			return true
		}
	}
	return false
}

// validateHooks reports the first invalid hook setting in the config.
func (c *Config) validateHooks() error {
	groups := []struct {
		name   string
		phases HookPhases
	}{
		{"build", c.Hooks.Build},
		{"dev", c.Hooks.Dev},
		{"deploy", c.Hooks.Deploy},
		{"seed", c.Hooks.Seed},
	}
	for _, group := range groups {
		phases := []struct {
			name  string
			steps []HookStep
		}{
			{"pre", group.phases.Pre},
			{"post", group.phases.Post},
		}
		for _, phase := range phases {
			for i := range phase.steps {
				where := fmt.Sprintf("weld.yml: hooks.%s.%s[%d]", group.name, phase.name, i)
				if err := phase.steps[i].validate(where); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// validate reports the first invalid field of one hook step. where names the
// step in the configuration for the error message.
func (s HookStep) validate(where string) error {
	if err := s.Run.validate(where); err != nil {
		return err
	}
	if s.Dir != "" {
		cleaned := filepath.Clean(s.Dir)
		if filepath.IsAbs(s.Dir) || cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
			return fmt.Errorf("%s: dir must be a relative path inside the project, got %q", where, s.Dir)
		}
	}
	for _, p := range s.Platforms {
		if _, ok := NormalizePlatform(p); !ok {
			return fmt.Errorf("%s: unknown platform %q (want linux, darwin, macos or windows)", where, p)
		}
	}
	if s.Timeout < 0 {
		return fmt.Errorf("%s: timeout must not be negative, got %s", where, s.Timeout)
	}
	return s.When.validate(where)
}

// validate reports the first invalid field of a run command.
func (c HookCommand) validate(where string) error {
	if len(c.Argv) > 0 {
		for i, arg := range c.Argv {
			if strings.TrimSpace(arg) == "" {
				return fmt.Errorf("%s: run[%d] is empty", where, i)
			}
		}
		return nil
	}
	if strings.TrimSpace(c.Shell) == "" {
		return fmt.Errorf("%s: run is required", where)
	}
	return nil
}

// validate reports the first invalid condition. A nil When is valid.
func (w *HookWhen) validate(where string) error {
	if w == nil {
		return nil
	}
	for _, capability := range w.Capability {
		if strings.TrimSpace(capability) == "" {
			return fmt.Errorf("%s: when.capability contains an empty name", where)
		}
	}
	for key := range w.Env {
		if strings.TrimSpace(key) == "" {
			return fmt.Errorf("%s: when.env contains an empty name", where)
		}
	}
	for _, file := range w.Files {
		if strings.TrimSpace(file) == "" {
			return fmt.Errorf("%s: when.files contains an empty path", where)
		}
	}
	return nil
}
