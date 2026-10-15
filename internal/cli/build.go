package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/Xwudao/weld/internal/build"
	"github.com/Xwudao/weld/internal/docker"
	"github.com/Xwudao/weld/internal/hook"
	"github.com/Xwudao/weld/internal/project"
	"github.com/Xwudao/weld/internal/projectcfg"
)

// newBuildCommand builds `weld build`.
func newBuildCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "build",
		Short: "build the frontend and cross-compile the application",
		Long: `Build the project described by the optional weld.yml.

The frontend is built first when the web capability is installed (or when
weld.yml sets build.web), then the Go binary is cross-compiled for every
build.targets entry. With no weld.yml the host platform is built once into bin/.
With --docker, build a local image using the user-owned Dockerfile created by
weld docker init; frontend and Go compilation run inside Docker, not on the host.
Docker settings use flags > build.docker in weld.yml > defaults (name:latest,
Dockerfile, linux/amd64). This does not push or deploy the image.

weld.yml is user-owned: weld never rewrites it and ` + "`weld add`" + ` never touches it.`,
		Args: cobra.NoArgs,
		RunE: runBuild,
	}
	cmd.Flags().Bool("docker", false, "build a local Docker image instead of host binaries")
	cmd.Flags().String("image", "", "Docker image name:tag")
	cmd.Flags().String("dockerfile", "", "Dockerfile path (default Dockerfile)")
	cmd.Flags().String("platform", "", "single Docker platform (default linux/amd64)")
	return cmd
}

// runBuild plans and applies `weld build`.
func runBuild(cmd *cobra.Command, args []string) error {
	stdout := cmd.OutOrStdout()
	dir, err := cmd.Flags().GetString("dir")
	if err != nil {
		return err
	}
	cfg, err := projectcfg.LoadOptional(dir)
	if err != nil {
		return err
	}

	// The manifest supplies the binary name and whether the web capability is
	// installed. A directory without one still builds, using the directory name
	// and no frontend.
	var name string
	webInstalled := false
	httpInstalled := false
	if manifest, loadErr := project.Load(dir); loadErr == nil {
		name = manifest.Name
		webInstalled = manifest.HasCapability("web")
		httpInstalled = manifest.HasCapability("http")
	}

	dockerMode, _ := cmd.Flags().GetBool("docker")
	if !dockerMode && (cmd.Flags().Changed("image") || cmd.Flags().Changed("dockerfile") || cmd.Flags().Changed("platform")) {
		return fmt.Errorf("--image, --dockerfile and --platform require --docker")
	}
	if dockerMode {
		if cfg != nil && len(cfg.Build.Targets) > 1 {
			fmt.Fprintf(cmd.ErrOrStderr(), "warning: --docker builds one image on --platform and ignores build.targets\n")
		}
		image, _ := cmd.Flags().GetString("image")
		dockerfile, _ := cmd.Flags().GetString("dockerfile")
		platform, _ := cmd.Flags().GetString("platform")
		dry, _ := cmd.Flags().GetBool("dry-run")
		buildEnv := []string{"WELD_BUILD_OUTPUT=" + cfg.Output()}
		if err := runHooks(cmd, cfg, hook.EventBuildPre, dry, buildEnv...); err != nil {
			return err
		}
		built, err := docker.Run(docker.Request{Dir: dir, Name: name, Config: cfg, Overrides: projectcfg.DockerConfig{Image: image, Dockerfile: dockerfile, Platform: platform}, WebInstalled: webInstalled, HTTPInstalled: httpInstalled, Stdout: stdout, Stderr: cmd.ErrOrStderr(), DryRun: dry})
		if err != nil {
			return err
		}
		if err := runHooks(cmd, cfg, hook.EventBuildPost, dry, buildEnv...); err != nil {
			return err
		}
		if !dry {
			fmt.Fprintf(stdout, "built image %s\n", built)
		}
		return nil
	}

	buildEnv := []string{"WELD_BUILD_OUTPUT=" + cfg.Output()}
	if err := runHooks(cmd, cfg, hook.EventBuildPre, false, buildEnv...); err != nil {
		return err
	}
	result, err := build.Run(build.Request{
		Dir:          dir,
		Name:         name,
		Config:       cfg,
		WebInstalled: webInstalled,
		Stdout:       stdout,
		Stderr:       cmd.ErrOrStderr(),
	})
	if err != nil {
		return err
	}
	if err := runHooks(cmd, cfg, hook.EventBuildPost, false, buildEnv...); err != nil {
		return err
	}
	if result.WebBuilt {
		fmt.Fprintln(stdout, "built frontend")
	}
	for _, binary := range result.Binaries {
		fmt.Fprintf(stdout, "built %s\n", binary)
	}
	return nil
}
