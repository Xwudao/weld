// Package deploy implements `weld deploy`: it uploads the built artifact to the
// host named in weld.yml and runs the remote deploy script over ssh.
//
// Planning and execution are separated the same way build and the scaffold
// separate them: Run validates the whole plan first, so a bad host or a missing
// artifact fails before a single command runs and --dry-run can print the exact
// scp/ssh steps without executing them. Command execution is injected through
// Runner, so a test records the commands instead of reaching the network.
package deploy

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"

	"github.com/Xwudao/weld/internal/projectcfg"
)

// Runner runs one external command with the inherited environment. It is the
// injection seam: a test records the command instead of invoking scp or ssh.
type Runner func(name string, args ...string) error

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
	// Stdout and Stderr receive the output of every command.
	Stdout io.Writer
	Stderr io.Writer
	// Runner overrides command execution. A nil Runner runs real commands.
	Runner Runner
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
	// RemotePath is the artifact's path on the remote host.
	RemotePath string
	// Steps are the scp and ssh commands, in order.
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
