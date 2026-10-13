package cli

import (
	"go/format"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestHTTPExportAndClientCommands(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if _, err := run(t, "new", "producer", "--module", "example.com/producer", "--dir", root); err != nil {
		t.Fatal(err)
	}
	producer := filepath.Join(root, "producer")
	if _, err := run(t, "add", "module", "widget", "--with-dependencies", "--dir", producer); err != nil {
		t.Fatal(err)
	}
	if out, err := run(t, "http", "export", "--dry-run", "--dir", producer); err != nil || !strings.Contains(out, "dry run") {
		t.Fatalf("export dry run: %s %v", out, err)
	}
	contractPath := filepath.Join(producer, "weld-api.json")
	if _, err := os.Stat(contractPath); !os.IsNotExist(err) {
		t.Fatal("dry run wrote contract")
	}
	if out, err := run(t, "http", "export", "--dir", producer); err != nil || !strings.Contains(out, "3 typed operation(s)") {
		t.Fatalf("export: %s %v", out, err)
	}
	if out, err := run(t, "http", "export", "--dir", producer); err != nil || !strings.Contains(out, "Applied 0") {
		t.Fatalf("repeat export: %s %v", out, err)
	}
	if _, err := run(t, "new", "consumer", "--module", "example.com/consumer", "--dir", root); err != nil {
		t.Fatal(err)
	}
	consumer := filepath.Join(root, "consumer")
	args := []string{"client", "add", "catalog", "--from", "../producer/weld-api.json", "--dir", consumer}
	if out, err := run(t, append(args, "--dry-run")...); err != nil || !strings.Contains(out, "dry run") {
		t.Fatalf("client dry run: %s %v", out, err)
	}
	output := filepath.Join(consumer, "internal/clients/catalog")
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatal("dry run wrote client")
	}
	if out, err := run(t, args...); err != nil || !strings.Contains(out, "3 operation(s)") {
		t.Fatalf("client: %s %v", out, err)
	}
	for _, name := range []string{"client_gen.go", "types_gen.go", "errors_gen.go"} {
		data, err := os.ReadFile(filepath.Join(output, name))
		if err != nil {
			t.Fatal(err)
		}
		formatted, err := format.Source(data)
		if err != nil || string(formatted) != string(data) {
			t.Fatal("unformatted client", name)
		}
		if strings.Contains(string(data), "example.com/producer") {
			t.Fatal("client imports producer implementation")
		}
	}
	if out, err := run(t, args...); err != nil || !strings.Contains(out, "Applied 0") {
		t.Fatalf("repeat client: %s %v", out, err)
	}
	cmd := exec.Command("go", "test", "./internal/clients/catalog")
	cmd.Dir = consumer
	cmd.Env = append(os.Environ(), "GOWORK=off")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("client compilation: %v\n%s", err, out)
	}
	// A hand-owned file blocks the entire write plan unless explicitly forced.
	path := filepath.Join(output, "client_gen.go")
	if err := os.WriteFile(path, []byte("package catalog\n// hand-owned\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, args...); err == nil || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("overwrite error=%v", err)
	}
	if _, err := run(t, append(args, "--force")...); err != nil {
		t.Fatal(err)
	}
}

func TestHTTPCommandValidation(t *testing.T) {
	for _, args := range [][]string{
		{"http", "export", "--dir", t.TempDir()},
		{"client", "add", "catalog"},
		{"client", "add"},
		{"client", "bogus"},
	} {
		if _, err := run(t, args...); err == nil {
			t.Errorf("arguments accepted: %v", args)
		}
	}
	for _, name := range []string{"http", "client"} {
		out, err := run(t, name, "--help")
		if err != nil || !strings.Contains(out, "Available Commands") {
			t.Fatalf("%s help: %s %v", name, out, err)
		}
	}
}
