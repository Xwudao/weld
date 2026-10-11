package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/Xwudao/weld/internal/build"
	"github.com/Xwudao/weld/internal/project"
	"github.com/Xwudao/weld/internal/projectcfg"
)

// newBuildCommand builds `weld build`.
func newBuildCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "build",
		Short: "build the frontend and cross-compile the application",
		Long: `Build the project described by the optional weld.yml.

The frontend is built first when the web capability is installed (or when
weld.yml sets build.web), then the Go binary is cross-compiled for every
build.targets entry. With no weld.yml the host platform is built once into bin/.
weld.yml is user-owned: weld never rewrites it and ` + "`weld add`" + ` never touches it.`,
		Args: cobra.NoArgs,
		RunE: runBuild,
	}
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
	if manifest, loadErr := project.Load(dir); loadErr == nil {
		name = manifest.Name
		webInstalled = manifest.HasCapability("web")
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
	if result.WebBuilt {
		fmt.Fprintln(stdout, "built frontend")
	}
	for _, binary := range result.Binaries {
		fmt.Fprintf(stdout, "built %s\n", binary)
	}
	return nil
}
