package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Xwudao/weld/internal/httpcontract"
)

func TestClientRefreshRemovesOnlyObsoleteOwnedEnums(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/consumer\n\ngo 1.23\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	response := httpcontract.Type{Kind: "ref", Name: "Status"}
	contract := &httpcontract.Contract{Version: httpcontract.Version, Service: "service", Types: []httpcontract.Definition{{Name: "Status", Type: httpcontract.Type{Kind: "enum", Values: []httpcontract.EnumValue{{Name: "StatusReady", Value: "ready"}}}}}, Operations: []httpcontract.Operation{{Name: "state", Method: "GET", Path: "/state", Binding: "none", Response: &response, Status: 200}}}
	writeContract := func() {
		t.Helper()
		data, err := contract.JSON()
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "api.json"), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeContract()
	args := []string{"client", "add", "catalog", "--from", "api.json", "--dir", dir}
	if _, err := run(t, args...); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(dir, "internal/clients/catalog")
	enumPath := filepath.Join(output, "enum_status_gen.go")
	if _, err := os.Stat(enumPath); err != nil {
		t.Fatal(err)
	}
	// A hand-owned file matching the enum naming convention is not tool-owned.
	handPath := filepath.Join(output, "enum_hand_gen.go")
	if err := os.WriteFile(handPath, []byte("package catalog\n// hand-owned wrapper\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	response = httpcontract.Type{Kind: "basic", Name: "string"}
	contract.Types = nil
	writeContract()
	if out, err := run(t, append(args, "--dry-run")...); err != nil || !strings.Contains(out, "delete") {
		t.Fatalf("dry run: %s %v", out, err)
	}
	if _, err := os.Stat(enumPath); err != nil {
		t.Fatal("dry run deleted enum")
	}
	if _, err := run(t, args...); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(enumPath); !os.IsNotExist(err) {
		t.Fatal("obsolete enum still exists")
	}
	if _, err := os.Stat(handPath); err != nil {
		t.Fatal("hand-owned file was removed")
	}
	cmd := exec.Command("go", "test", "./internal/clients/catalog")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOWORK=off")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("refreshed client doesn't compile: %v\n%s", err, out)
	}
}
