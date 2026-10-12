package deploy

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Xwudao/weld/internal/projectcfg"
)

type call struct {
	name string
	args []string
}

func recorder(calls *[]call) Runner {
	return func(name string, args ...string) error {
		*calls = append(*calls, call{name: name, args: args})
		return nil
	}
}

func deployConfig() *projectcfg.Config {
	return &projectcfg.Config{Deploy: projectcfg.DeployConfig{
		Host:            "prod-app",
		RemoteUploadDir: "/srv/app",
		RemoteScript:    "/srv/app/deploy.sh",
	}}
}

func TestRunPlansAndApplies(t *testing.T) {
	dir := t.TempDir()
	artifact := filepath.Join(dir, "bin", "demo")
	if err := os.MkdirAll(filepath.Dir(artifact), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(artifact, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}

	var calls []call
	result, err := Run(Request{Config: deployConfig(), Artifact: artifact, Runner: recorder(&calls)})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Applied {
		t.Error("Applied = false after a successful run")
	}
	if result.RemotePath != "/srv/app/demo" {
		t.Errorf("RemotePath = %q", result.RemotePath)
	}
	want := []call{
		{name: "scp", args: []string{artifact, "prod-app:/srv/app/demo"}},
		{name: "ssh", args: []string{"prod-app", "sh '/srv/app/deploy.sh' '/srv/app/demo'"}},
	}
	if !reflect.DeepEqual(calls, want) {
		t.Errorf("calls = %+v, want %+v", calls, want)
	}
}

func TestRunDryRunSkipsStatAndExecution(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "bin", "demo")
	ran := false
	result, err := Run(Request{Config: deployConfig(), Artifact: missing, DryRun: true, Runner: func(string, ...string) error {
		ran = true
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	if ran {
		t.Error("a command ran during a dry run")
	}
	if result.Applied {
		t.Error("Applied = true for a dry run")
	}
	if len(result.Steps) != 2 {
		t.Fatalf("Steps = %v", result.Steps)
	}
	if got := result.Steps[0].String(); !strings.HasPrefix(got, "scp ") {
		t.Errorf("step = %q", got)
	}
}

func TestRunRequiresArtifact(t *testing.T) {
	_, err := Run(Request{Config: deployConfig(), Runner: func(string, ...string) error { return nil }})
	if err == nil || !strings.Contains(err.Error(), "no artifact") {
		t.Fatalf("err = %v, want a no-artifact error", err)
	}
}

func TestRunValidatesConfig(t *testing.T) {
	cases := []struct {
		name string
		cfg  projectcfg.DeployConfig
		want string
	}{
		{"host", projectcfg.DeployConfig{RemoteUploadDir: "/srv", RemoteScript: "/srv/x.sh"}, "deploy.host"},
		{"upload dir", projectcfg.DeployConfig{Host: "h", RemoteScript: "/srv/x.sh"}, "deploy.remote_upload_dir"},
		{"script", projectcfg.DeployConfig{Host: "h", RemoteUploadDir: "/srv"}, "deploy.remote_script"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Run(Request{Config: &projectcfg.Config{Deploy: tc.cfg}, Artifact: "x", Runner: func(string, ...string) error { return nil }})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestRunMissingArtifact(t *testing.T) {
	_, err := Run(Request{Config: deployConfig(), Artifact: filepath.Join(t.TempDir(), "nope"), Runner: func(string, ...string) error { return nil }})
	if err == nil || !strings.Contains(err.Error(), "stat artifact") {
		t.Fatalf("err = %v, want a stat error", err)
	}
}

func TestRunRunnerErrorPropagates(t *testing.T) {
	dir := t.TempDir()
	artifact := filepath.Join(dir, "demo")
	if err := os.WriteFile(artifact, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	want := errors.New("boom")
	_, err := Run(Request{Config: deployConfig(), Artifact: artifact, Runner: func(string, ...string) error { return want }})
	if !errors.Is(err, want) {
		t.Fatalf("err = %v, want %v", err, want)
	}
}

func TestShellQuote(t *testing.T) {
	if got := shellQuote("/srv/my app/x.sh"); got != "'/srv/my app/x.sh'" {
		t.Errorf("shellQuote = %q", got)
	}
	if got := shellQuote("a'b"); got != `'a'\''b'` {
		t.Errorf("shellQuote = %q", got)
	}
}
