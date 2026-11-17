package dev

import (
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"strconv"

	"gopkg.in/yaml.v3"
)

func (s *supervisor) openTarget(args []string) {
	if len(args) > 1 {
		s.message("usage: o [frontend|f|backend|b]")
		return
	}
	name := s.openConfig.Target
	if name == "" {
		name = frontendName
	}
	if len(args) == 1 {
		name = args[0]
	}
	switch name {
	case "f", frontendName:
		name = frontendName
	case "b", backendName:
		name = backendName
	default:
		s.message("usage: o [frontend|f|backend|b]")
		return
	}
	if proc, ok := s.processes[name]; !ok || proc.cmd == nil {
		s.message(name + " is not running")
		return
	}
	target := s.getFrontendURL()
	if name == backendName {
		target = s.openConfig.BackendURL
		if target == "" {
			var err error
			target, err = inferBackendURL(s.dir, s.readFile)
			if err != nil {
				s.message(err.Error())
				return
			}
		}
	}
	if target == "" {
		s.message("frontend url not detected yet")
		return
	}
	// Browser errors may include the URL (and its query), so do not echo them.
	if err := s.openBrowser(target); err != nil {
		s.message("could not open " + name + " in browser")
		return
	}
	s.message("opened " + name + " in browser")
}

// Read on each open so config edits do not leave a stale inferred port. This
// intentionally does not emulate application flags or environment overrides.
func inferBackendURL(dir string, readFile func(string) ([]byte, error)) (string, error) {
	const hint = "; set dev.open.backend_url in weld.yml to override"
	raw, err := readFile(filepath.Join(dir, "config.yml"))
	if err != nil {
		return "", errors.New("cannot read config.yml for backend URL" + hint)
	}
	var cfg struct {
		HTTP struct {
			Addr string `yaml:"addr"`
		} `yaml:"http"`
	}
	// Other application settings are intentionally ignored. Do not surface YAML
	// errors: they can contain configuration values, including secrets.
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		return "", errors.New("cannot parse config.yml for backend URL" + hint)
	}
	_, port, err := net.SplitHostPort(cfg.HTTP.Addr)
	if err != nil {
		return "", errors.New("config.yml: http.addr must contain a host and port" + hint)
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return "", errors.New("config.yml: http.addr must have a port between 1 and 65535" + hint)
	}
	return fmt.Sprintf("http://127.0.0.1:%d", n), nil
}
