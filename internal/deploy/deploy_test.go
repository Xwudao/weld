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

func uploadCommandConfig() *projectcfg.Config {
	return &projectcfg.Config{Deploy: projectcfg.DeployConfig{
		Host:          "prod-app",
		UploadCommand: "./publish.sh",
		RemoteScript:  "/srv/app/deploy.sh",
	}}
}

func TestRunUploadCommandReplacesScp(t *testing.T) {
	dir := t.TempDir()
	artifact := filepath.Join(dir, "demo")
	if err := os.WriteFile(artifact, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	var stdout strings.Builder
	var calls []call
	var gotCommand, gotArtifact string
	const secret = "https://r2.example/demo?X-Amz-Signature=abc&a=it's"
	result, err := Run(Request{
		Config:   uploadCommandConfig(),
		Artifact: artifact,
		Stdout:   &stdout,
		Runner:   recorder(&calls),
		UploadRunner: func(command, path string) (string, error) {
			gotCommand, gotArtifact = command, path
			return "uploaded 83 MB\n" + secret + "  deadbeef\n\n", nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if gotCommand != "./publish.sh" || gotArtifact != artifact {
		t.Errorf("upload ran %q on %q", gotCommand, gotArtifact)
	}
	want := []call{{name: "ssh", args: []string{
		"prod-app",
		"sh '/srv/app/deploy.sh' " + shellQuote(secret) + " 'deadbeef'",
	}}}
	if !reflect.DeepEqual(calls, want) {
		t.Errorf("calls = %+v, want %+v", calls, want)
	}
	if !result.Applied || result.RemotePath != "" {
		t.Errorf("result = %+v", result)
	}
	if got := stdout.String(); got != "uploaded 83 MB\n" {
		t.Errorf("stdout = %q", got)
	}
	for _, step := range result.Steps {
		if strings.Contains(step.String(), "Signature") {
			t.Errorf("step exposes the output: %s", step)
		}
	}
}

func TestRunUploadCommandRunsInDirWithArtifactEnv(t *testing.T) {
	dir := t.TempDir()
	artifact := filepath.Join(dir, "bin", "demo")
	if err := os.MkdirAll(filepath.Dir(artifact), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(artifact, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := uploadCommandConfig()
	cfg.Deploy.UploadCommand = `echo progress >&2; echo "$PWD $WELD_ARTIFACT $EXTRA"`
	var stderr strings.Builder
	var calls []call
	if _, err := Run(Request{
		Config:   cfg,
		Artifact: artifact,
		Dir:      dir,
		Env:      []string{"EXTRA=value"},
		Stderr:   &stderr,
		Runner:   recorder(&calls),
	}); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 || !strings.HasSuffix(calls[0].args[1], shellQuote(artifact)+" 'value'") {
		t.Fatalf("calls = %+v", calls)
	}
	if !strings.Contains(stderr.String(), "progress") {
		t.Errorf("stderr = %q", stderr.String())
	}
}

func TestRunUploadCommandFailureStopsBeforeSsh(t *testing.T) {
	artifact := filepath.Join(t.TempDir(), "demo")
	if err := os.WriteFile(artifact, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	boom := errors.New("boom")
	for name, tc := range map[string]struct {
		output string
		err    error
		want   string
	}{
		"failed": {err: boom, want: "boom"},
		"empty":  {output: " \n", want: "printed nothing"},
	} {
		t.Run(name, func(t *testing.T) {
			var calls []call
			_, err := Run(Request{
				Config:       uploadCommandConfig(),
				Artifact:     artifact,
				Runner:       recorder(&calls),
				UploadRunner: func(string, string) (string, error) { return tc.output, tc.err },
			})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
			if len(calls) != 0 {
				t.Errorf("ssh ran after a failed upload: %+v", calls)
			}
		})
	}
}

func TestRunUploadCommandDryRunRunsNothing(t *testing.T) {
	ran := false
	result, err := Run(Request{
		Config:   uploadCommandConfig(),
		Artifact: filepath.Join(t.TempDir(), "missing"),
		DryRun:   true,
		Runner:   func(string, ...string) error { ran = true; return nil },
		UploadRunner: func(string, string) (string, error) {
			ran = true
			return "", nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if ran || result.Applied {
		t.Errorf("ran = %v, result = %+v", ran, result)
	}
	if len(result.Steps) != 2 || !strings.Contains(result.Steps[0].String(), "./publish.sh") {
		t.Errorf("steps = %v", result.Steps)
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
