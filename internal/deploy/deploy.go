// Package deploy implements `weld deploy`: it uploads the built artifact to the
// host named in weld.yml, or publishes it with deploy.upload_command, and runs the
// remote deploy script over ssh.
//
// Planning and execution are separated the same way build and the scaffold
// separate them: Run validates the whole plan first, so a bad host or a missing
// artifact fails before a single command runs and --dry-run can print the exact
// steps without executing them. Command execution is injected through Runner and
// UploadRunner, so a test records the commands instead of reaching the network.
package deploy

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/Xwudao/weld/internal/projectcfg"
)

// Runner runs one external command with the inherited environment. It is the
// injection seam: a test records the command instead of invoking scp or ssh.
type Runner func(name string, args ...string) error

// UploadRunner runs deploy.upload_command for the absolute artifact path and
// returns what it wrote to stdout. It is the injection seam for that command.
type UploadRunner func(command, artifact string) (string, error)

// Request describes a deploy.
type Request struct {
	// Config is the parsed weld.yml, or nil when the project has none.
	Config *projectcfg.Config
	// Artifact is the local file uploaded to the host. It is required unless
	// DryRun is set.
	Artifact string
	// DryRun plans the upload without checking for the artifact or running a
	// command, so the steps can be printed before the binary exists.
	DryRun bool
	// Dir is the directory deploy.upload_command runs in.
	Dir string
	// Env is extra KEY=VALUE environment for deploy.upload_command.
	Env []string
	// Stdout and Stderr receive the output of every command.
	Stdout io.Writer
	Stderr io.Writer
	// Runner overrides command execution. A nil Runner runs real commands.
	Runner Runner
	// UploadRunner overrides deploy.upload_command. A nil UploadRunner runs it
	// through the platform shell.
	UploadRunner UploadRunner
}

// Step is one command in a deploy plan.
type Step struct {
	Name string
	Args []string
}

// String renders a step the way it would be typed at a shell. It is display
// only: Apply runs the arguments directly, never through a local shell.
func (s Step) String() string {
	return strings.Join(append([]string{s.Name}, s.Args...), " ")
}

// Result reports what a deploy produced (or planned).
type Result struct {
	// Artifact is the local file uploaded.
	Artifact string
	// RemotePath is the artifact's path on the remote host. It is empty when
	// deploy.upload_command published the artifact.
	RemotePath string
	// Steps are the upload and ssh commands, in order. With deploy.upload_command
	// the ssh step shows a placeholder for arguments that may be secret, such as a
	// presigned URL.
	Steps []Step
	// Applied reports whether the steps ran. It is false for a dry run.
	Applied bool
}

// Run executes a deploy request.
func Run(req Request) (*Result, error) {
	cfg := req.Config.EffectiveDeploy()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	artifact := strings.TrimSpace(req.Artifact)
	if artifact == "" {
		return nil, fmt.Errorf("weld deploy: no artifact to upload")
	}
	if !req.DryRun {
		if _, err := os.Stat(artifact); err != nil {
			return nil, fmt.Errorf("weld deploy: stat artifact: %w", err)
		}
	}

	if command := strings.TrimSpace(cfg.UploadCommand); command != "" {
		return runUploadCommand(req, cfg, command, artifact)
	}

	host := strings.TrimSpace(cfg.Host)
	remotePath := path.Join(strings.TrimSpace(cfg.RemoteUploadDir), filepath.Base(artifact))
	remoteCmd := fmt.Sprintf("sh %s %s", shellQuote(strings.TrimSpace(cfg.RemoteScript)), shellQuote(remotePath))

	result := &Result{
		Artifact:   artifact,
		RemotePath: remotePath,
		Steps: []Step{
			{Name: "scp", Args: []string{artifact, host + ":" + remotePath}},
			{Name: "ssh", Args: []string{host, remoteCmd}},
		},
	}
	if req.DryRun {
		return result, nil
	}

	runner := req.Runner
	if runner == nil {
		runner = commandRunner(req.Stdout, req.Stderr)
	}
	for _, step := range result.Steps {
		if err := runner(step.Name, step.Args...); err != nil {
			return nil, fmt.Errorf("%s: %w", step.Name, err)
		}
	}
	result.Applied = true
	return result, nil
}

// runUploadCommand publishes the artifact with deploy.upload_command and passes
// its output to the remote script. The output is never printed or put in an
// error, because it may carry a presigned URL.
func runUploadCommand(req Request, cfg projectcfg.DeployConfig, command, artifact string) (*Result, error) {
	host := strings.TrimSpace(cfg.Host)
	script := shellQuote(strings.TrimSpace(cfg.RemoteScript))
	shell, shellArgs := shellCommand(command)
	result := &Result{
		Artifact: artifact,
		Steps: []Step{
			{Name: shell, Args: shellArgs},
			{Name: "ssh", Args: []string{host, "sh " + script + " <upload_command output>"}},
		},
	}
	if req.DryRun {
		return result, nil
	}

	absolute, err := filepath.Abs(artifact)
	if err != nil {
		return nil, fmt.Errorf("weld deploy: resolve artifact: %w", err)
	}
	upload := req.UploadRunner
	if upload == nil {
		upload = shellUploadRunner(req)
	}
	output, err := upload(command, absolute)
	if err != nil {
		return nil, fmt.Errorf("deploy.upload_command: %w", err)
	}
	notes, args := splitUploadOutput(output)
	if len(args) == 0 {
		return nil, fmt.Errorf("deploy.upload_command: printed nothing to pass to %s", strings.TrimSpace(cfg.RemoteScript))
	}
	if notes != "" && req.Stdout != nil {
		fmt.Fprintln(req.Stdout, notes)
	}

	remoteCmd := "sh " + script
	for _, arg := range args {
		remoteCmd += " " + shellQuote(arg)
	}
	runner := req.Runner
	if runner == nil {
		runner = commandRunner(req.Stdout, req.Stderr)
	}
	if err := runner("ssh", host, remoteCmd); err != nil {
		return nil, fmt.Errorf("ssh: %w", err)
	}
	result.Applied = true
	return result, nil
}

// splitUploadOutput separates the last non-empty line, split on whitespace into
// the remote script's arguments, from the lines printed before it.
func splitUploadOutput(output string) (notes string, args []string) {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	last := len(lines) - 1
	return strings.TrimSpace(strings.Join(lines[:last], "\n")), strings.Fields(lines[last])
}

// shellUploadRunner returns the default UploadRunner. Stdout is captured for the
// remote script; stderr streams so a long upload can report progress.
func shellUploadRunner(req Request) UploadRunner {
	return func(command, artifact string) (string, error) {
		name, args := shellCommand(command)
		cmd := exec.Command(name, args...)
		cmd.Dir = req.Dir
		cmd.Env = append(append(os.Environ(), req.Env...), "WELD_ARTIFACT="+artifact)
		var stdout bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = req.Stderr
		cmd.Stdin = os.Stdin
		if err := cmd.Run(); err != nil {
			return "", err
		}
		return stdout.String(), nil
	}
}

// shellCommand wraps a command line for the host platform's shell.
func shellCommand(line string) (string, []string) {
	if runtime.GOOS == "windows" {
		return "cmd", []string{"/C", line}
	}
	return "sh", []string{"-c", line}
}

// shellQuote wraps s in single quotes so the remote shell receives it as one
// argument even when it contains spaces. ssh joins its command arguments and
// runs the result through the remote shell, so an unquoted path would split.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// commandRunner returns the default Runner, which streams every command's
// output to stdout/stderr and inherits the process environment.
func commandRunner(stdout, stderr io.Writer) Runner {
	return func(name string, args ...string) error {
		cmd := exec.Command(name, args...)
		cmd.Stdout = stdout
		cmd.Stderr = stderr
		cmd.Stdin = os.Stdin
		return cmd.Run()
	}
}
