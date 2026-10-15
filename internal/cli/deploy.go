package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/Xwudao/weld/internal/build"
	"github.com/Xwudao/weld/internal/deploy"
	"github.com/Xwudao/weld/internal/hook"
	"github.com/Xwudao/weld/internal/project"
	"github.com/Xwudao/weld/internal/projectcfg"
)

// newDeployCommand builds `weld deploy`.
func newDeployCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "deploy",
		Short: "upload the built binary and run the remote deploy script",
		Long: `Deploy the project described by weld.yml's deploy section.

By default weld deploy builds the deploy target (deploy.target, default
linux/amd64) and then uploads the resulting binary to deploy.host with scp and
runs deploy.remote_script over ssh, passing the uploaded path as its argument.
The deploy section names the host and remote paths:

  deploy:
    host: prod-app
    remote_upload_dir: /srv/app
    remote_script: /srv/app/deploy.sh
    target: { os: linux, arch: amd64 }

weld.yml is user-owned: weld never rewrites it and ` + "`weld add`" + ` never touches it.
--no-build uploads an existing artifact, and --artifact names one explicitly
(which also skips the build). --dry-run prints the scp/ssh steps without
building or running anything.`,
		Args: cobra.NoArgs,
		RunE: runDeploy,
	}
	cmd.Flags().Bool("no-build", false, "upload an existing artifact instead of building first")
	cmd.Flags().String("artifact", "", "local artifact to upload (implies --no-build)")
	return cmd
}

// runDeploy builds, then plans and applies a deploy.
func runDeploy(cmd *cobra.Command, args []string) error {
	stdout := cmd.OutOrStdout()
	stderr := cmd.ErrOrStderr()
	dir, err := cmd.Flags().GetString("dir")
	if err != nil {
		return err
	}
	dryRun, err := cmd.Flags().GetBool("dry-run")
	if err != nil {
		return err
	}
	noBuild, err := cmd.Flags().GetBool("no-build")
	if err != nil {
		return err
	}
	artifact, err := cmd.Flags().GetString("artifact")
	if err != nil {
		return err
	}

	cfg, err := projectcfg.LoadOptional(dir)
	if err != nil {
		return err
	}
	// Validate the deploy section before doing any work, so a missing host or
	// script fails without a build.
	deployCfg := cfg.EffectiveDeploy()
	if err := deployCfg.Validate(); err != nil {
		return fmt.Errorf("%w (run: %s config example)", err, progName)
	}

	name, webInstalled := projectIdentity(dir)

	// Hooks run before any build or upload, so a pre hook can produce the
	// artifact the deploy uploads (for example a frontend build).
	deployEnv := []string{
		"WELD_DEPLOY_HOST=" + deployCfg.Host,
		"WELD_DEPLOY_TARGET=" + deployCfg.Target.OS + "/" + deployCfg.Target.Arch,
	}
	if err := runHooks(cmd, cfg, hook.EventDeployPre, dryRun, deployEnv...); err != nil {
		return err
	}

	// --artifact names the file and skips the build. Otherwise the default is
	// a build for the deploy target; --no-build reuses an earlier one.
	local := ""
	switch {
	case artifact != "":
		local = filepath.Join(dir, artifact)
	case dryRun:
		// Plan the path a deploy build would write without building it.
		local = filepath.Join(dir, "bin", name)
	case noBuild:
		local, err = existingArtifact(dir, name, deployCfg)
		if err != nil {
			return err
		}
	default:
		local, err = buildForDeploy(dir, name, webInstalled, cfg, deployCfg, stdout, stderr)
		if err != nil {
			return err
		}
	}

	result, err := deploy.Run(deploy.Request{
		Config:   cfg,
		Artifact: local,
		DryRun:   dryRun,
		Stdout:   stdout,
		Stderr:   stderr,
	})
	if err != nil {
		return err
	}
	if err := runHooks(cmd, cfg, hook.EventDeployPost, dryRun, deployEnv...); err != nil {
		return err
	}
	if dryRun {
		fmt.Fprintf(stdout, "Plan (%d step(s), dry run):\n", len(result.Steps))
		for _, step := range result.Steps {
			fmt.Fprintf(stdout, "  %s\n", step)
		}
		return nil
	}
	fmt.Fprintf(stdout, "uploaded %s to %s:%s\n", result.Artifact, deployCfg.Host, result.RemotePath)
	fmt.Fprintf(stdout, "ran %s on %s\n", deployCfg.RemoteScript, deployCfg.Host)
	return nil
}

// buildForDeploy builds only the deploy target and returns the artifact path,
// joined with the project directory. It honors weld.yml's web flag, so a web
// project still builds its frontend first.
func buildForDeploy(dir, name string, webInstalled bool, cfg *projectcfg.Config, deployCfg projectcfg.DeployConfig, stdout, stderr io.Writer) (string, error) {
	result, err := build.Run(build.Request{
		Dir:          dir,
		Name:         name,
		Config:       deployBuildConfig(cfg, deployCfg),
		WebInstalled: webInstalled,
		Stdout:       stdout,
		Stderr:       stderr,
	})
	if err != nil {
		return "", err
	}
	if len(result.Binaries) == 0 {
		return "", fmt.Errorf("%s deploy: build produced no artifact", progName)
	}
	if result.WebBuilt {
		fmt.Fprintln(stdout, "built frontend")
	}
	for _, binary := range result.Binaries {
		fmt.Fprintf(stdout, "built %s\n", binary)
	}
	return filepath.Join(dir, result.Binaries[0]), nil
}

// deployBuildConfig copies cfg and pins it to the single deploy target, so a
// deploy never cross-compiles the unrelated build.targets entries.
func deployBuildConfig(cfg *projectcfg.Config, deployCfg projectcfg.DeployConfig) *projectcfg.Config {
	out := &projectcfg.Config{}
	if cfg != nil {
		*out = *cfg
	}
	out.Build.Targets = []projectcfg.Target{*deployCfg.Target}
	return out
}

// existingArtifact resolves a `--no-build` artifact: the bare bin/<name> a
// single-target build writes, then the <os>-<arch> suffixed name a multi-target
// build writes. It reports the candidates when neither exists.
func existingArtifact(dir, name string, deployCfg projectcfg.DeployConfig) (string, error) {
	target := *deployCfg.Target
	suffixed := filepath.Join("bin", fmt.Sprintf("%s-%s-%s", name, target.OS, target.Arch))
	if target.OS == "windows" {
		suffixed += ".exe"
	}
	candidates := []string{filepath.Join("bin", name), suffixed}
	for _, candidate := range candidates {
		if _, err := os.Stat(filepath.Join(dir, candidate)); err == nil {
			return filepath.Join(dir, candidate), nil
		}
	}
	return "", fmt.Errorf("%s deploy: no artifact at %s (run: %s build, or pass --artifact)", progName, candidates[0], progName)
}

// projectIdentity returns the project name (the manifest name, or the directory
// name) and whether the web capability is installed.
func projectIdentity(dir string) (string, bool) {
	name := ""
	webInstalled := false
	if manifest, err := project.Load(dir); err == nil {
		name = manifest.Name
		webInstalled = manifest.HasCapability("web")
	}
	if name == "" {
		if abs, err := filepath.Abs(dir); err == nil {
			name = filepath.Base(abs)
		}
	}
	return name, webInstalled
}
