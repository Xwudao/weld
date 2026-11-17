package dev

import (
	"context"
	"errors"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Xwudao/weld/internal/projectcfg"
)

func TestInferBackendURL(t *testing.T) {
	for _, tc := range []struct {
		name, raw, want string
	}{
		{"wildcard", "http: {addr: ':8080'}", "http://127.0.0.1:8080"},
		{"ipv4", "http: {addr: '0.0.0.0:9000'}", "http://127.0.0.1:9000"},
		{"ipv6", "http: {addr: '[::]:3000'}", "http://127.0.0.1:3000"},
		{"hostname", "http: {addr: 'localhost:8081'}\nother: value", "http://127.0.0.1:8081"},
		{"missing", "database: {}", ""},
		{"invalid yaml", "http: [secret-sentinel", ""},
		{"no port", "http: {addr: localhost}", ""},
		{"dynamic port", "http: {addr: ':0'}", ""},
		{"too large", "http: {addr: ':65536'}", ""},
		{"service", "http: {addr: ':http'}", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := inferBackendURL("project", func(path string) ([]byte, error) {
				if path != filepath.Join("project", "config.yml") {
					t.Fatalf("path = %q", path)
				}
				return []byte(tc.raw), nil
			})
			if got != tc.want || (err != nil) != (tc.want == "") {
				t.Fatalf("got %q, %v; want %q", got, err, tc.want)
			}
			if err != nil && strings.Contains(err.Error(), "secret-sentinel") {
				t.Fatal("leaked configuration")
			}
		})
	}
	if _, err := inferBackendURL("", func(string) ([]byte, error) { return nil, errors.New("secret-sentinel") }); err == nil || strings.Contains(err.Error(), "secret-sentinel") {
		t.Fatalf("read error = %v", err)
	}
}

func TestOpenFailures(t *testing.T) {
	for _, mode := range []string{"missing config", "browser failure", "frontend URL missing", "frontend absent"} {
		t.Run(mode, func(t *testing.T) {
			var out strings.Builder
			s := newSupervisor(context.Background(), []spec{{Name: backendName}, {Name: frontendName}}, nil, &out, io.Discard)
			defer s.cancel()
			for _, p := range s.processes {
				p.cmd = &exec.Cmd{}
			}
			s.openConfig.Target = backendName
			s.readFile = func(string) ([]byte, error) { return nil, errors.New("secret-sentinel") }
			calls := 0
			s.openBrowser = func(string) error { calls++; return errors.New("secret-sentinel") }
			want, wantCalls := "cannot read config.yml", 0
			switch mode {
			case "browser failure":
				s.openConfig.BackendURL = "http://127.0.0.1:8080"
				want, wantCalls = "could not open backend", 1
			case "frontend URL missing":
				s.openConfig.Target = frontendName
				want = "frontend url not detected yet"
			case "frontend absent":
				s.openConfig.Target = frontendName
				delete(s.processes, frontendName)
				want = "frontend is not running"
			}
			s.handle("o")
			if calls != wantCalls || !strings.Contains(out.String(), want) || strings.Contains(out.String(), "secret-sentinel") {
				t.Fatalf("calls = %d, output = %q", calls, out.String())
			}
		})
	}
}

func TestOpenCommands(t *testing.T) {
	for _, tc := range []struct {
		command, defaultTarget, override, want, message string
		stopped                                         bool
	}{
		{command: "o", want: "http://localhost:5173/"},
		{command: "open", defaultTarget: "backend", want: "http://127.0.0.1:9000"},
		{command: "o b", want: "http://127.0.0.1:9000"},
		{command: "o backend", override: "https://localhost:8443/admin", want: "https://localhost:8443/admin"},
		{command: "o f", defaultTarget: "backend", want: "http://localhost:5173/"},
		{command: "o frontend", want: "http://localhost:5173/"},
		{command: "o all", message: "usage:"},
		{command: "o b extra", message: "usage:"},
		{command: "o b", stopped: true, message: "backend is not running"},
	} {
		t.Run(tc.command+tc.defaultTarget+tc.message, func(t *testing.T) {
			var out strings.Builder
			s := newSupervisor(context.Background(), []spec{{Name: backendName}, {Name: frontendName, URL: "http://localhost:5173/"}}, nil, &out, io.Discard)
			defer s.cancel()
			s.openConfig = projectcfg.DevOpenConfig{Target: tc.defaultTarget, BackendURL: tc.override}
			if !tc.stopped {
				for _, p := range s.processes {
					p.cmd = &exec.Cmd{}
				}
			}
			s.readFile = func(string) ([]byte, error) { return []byte("http: {addr: ':9000'}"), nil }
			var got string
			s.openBrowser = func(target string) error { got = target; return nil }
			s.handle(tc.command)
			if got != tc.want || !strings.Contains(out.String(), tc.message) {
				t.Fatalf("opened %q, output %q", got, out.String())
			}
		})
	}
}
