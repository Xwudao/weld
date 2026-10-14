package cli

import (
	"fmt"

	"github.com/Xwudao/weld/internal/docker"
	"github.com/Xwudao/weld/internal/project"
	"github.com/Xwudao/weld/internal/projectcfg"
	"github.com/spf13/cobra"
)

func newDockerCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "docker", Short: "initialize user-owned Docker support for the generated project", Args: cobra.NoArgs}
	cmd.AddCommand(&cobra.Command{
		Use: "init", Short: "create Dockerfile and .dockerignore without overwriting existing files", Args: cobra.NoArgs,
		Long: "Create user-owned Dockerfile and .dockerignore for the installed capabilities.\nRun after adding web. Existing files are kept; remove them explicitly to regenerate.\nThe default image uses CGO_ENABLED=0; CGO apps need a custom Dockerfile/runtime.\nRuntime config.yml must be mounted, not baked into the image.",
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, _ := cmd.Flags().GetString("dir")
			dry, _ := cmd.Flags().GetBool("dry-run")
			manifest, err := project.Load(dir)
			if err != nil {
				return err
			}
			cfg, err := projectcfg.LoadOptional(dir)
			if err != nil {
				return err
			}
			files, err := docker.Files(dir, cfg, manifest.HasCapability("web"), manifest.HasCapability("http"))
			if err != nil {
				return err
			}
			if err := docker.Init(dir, files, dry, cmd.OutOrStdout()); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "Next: weld build --docker (mount config.yml at /app/config.yml when required)")
			return nil
		},
	})
	return cmd
}
