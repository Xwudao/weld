package template

import (
	"encoding/json"
	"fmt"
	"io/fs"

	weldtemplate "github.com/Xwudao/weld-template"
)

// CommandVariant is one flavour of the per-name command payload: the
// independent generic group (`weld add command`) or the module-backed group
// (`weld add module --command`).
type CommandVariant struct {
	Files []ModuleFile `json:"files"`
}

// CommandTemplate is the payload `weld add command <name>` and
// `weld add module <name> --command` write.
//
// Both variants declare the same target paths for a given name, so a name is
// always one command group in `internal/commands/<name>/` plus one registration
// file in `internal/app/`; only the sources differ. The generic group is
// independent, while the module group is backed by the module package's Service
// interface and NewService constructor.
type CommandTemplate struct {
	Version string         `json:"version"`
	Generic CommandVariant `json:"generic"`
	Module  CommandVariant `json:"module"`
	fsys    fs.FS
}

// LoadCommands returns the command payload embedded in the weld-template module.
func LoadCommands() (*CommandTemplate, error) {
	root := weldtemplate.FS()
	raw, err := fs.ReadFile(root, "commands/command.json")
	if err != nil {
		return nil, fmt.Errorf("command template: read descriptor: %w", err)
	}
	command := &CommandTemplate{}
	if err := json.Unmarshal(raw, command); err != nil {
		return nil, fmt.Errorf("command template: invalid descriptor: %w", err)
	}
	if command.Version == "" {
		return nil, fmt.Errorf("command template: missing version")
	}
	sub, err := fs.Sub(root, "commands")
	if err != nil {
		return nil, fmt.Errorf("command template: %w", err)
	}
	command.fsys = sub
	for _, variant := range []struct {
		name  string
		files []ModuleFile
	}{{"generic", command.Generic.Files}, {"module", command.Module.Files}} {
		if len(variant.files) == 0 {
			return nil, fmt.Errorf("command template: %s variant declares no files", variant.name)
		}
		for _, file := range variant.files {
			if file.Path == "" || file.Source == "" {
				return nil, fmt.Errorf("command template: %s file entry needs path and source", variant.name)
			}
			if err := command.requireSource(file.Source); err != nil {
				return nil, err
			}
		}
	}
	return command, nil
}

func (m *CommandTemplate) requireSource(source string) error {
	if _, err := fs.Stat(m.fsys, source); err != nil {
		return fmt.Errorf("command template: payload %q: %w", source, err)
	}
	return nil
}

// ReadFile returns the raw payload for a command file.
func (m *CommandTemplate) ReadFile(file ModuleFile) ([]byte, error) {
	return m.read(file.Source)
}

func (m *CommandTemplate) read(source string) ([]byte, error) {
	raw, err := fs.ReadFile(m.fsys, source)
	if err != nil {
		return nil, fmt.Errorf("command template: read %s: %w", source, err)
	}
	return raw, nil
}
