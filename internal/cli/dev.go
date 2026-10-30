package cli

import (
	"github.com/spf13/cobra"

	"github.com/Xwudao/weld/internal/dev"
	"github.com/Xwudao/weld/internal/hook"
	"github.com/Xwudao/weld/internal/project"
	"github.com/Xwudao/weld/internal/projectcfg"
)

// newDevCommand builds `weld dev`.
func newDevCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "dev",
		Short: "run the backend and frontend development servers together",
		Long: `Run the backend and, when the web capability is installed, the frontend
development server side by side.

Settings come from the optional weld.yml (dev.backend, dev.frontend) with
defaults derived from the installed capabilities: the backend runs ` + "`go run .`" + `
(which serves once http is installed) and the frontend runs ` + "`pnpm run dev`" + ` in
web/. Configure dev.backend.watch globs for automatic backend restarts and
watch_exclude to ignore matching files. ** matches any directory depth.
Ctrl-C stops both. Control commands on stdin: rs backend|b,
rs frontend|f, rs all|a, o (open), status|st, help|h, quit.`,
		Args: cobra.NoArgs,
		RunE: runDev,
	}
}

// runDev runs a dev session.
func runDev(cmd *cobra.Command, args []string) error {
	dir, err := cmd.Flags().GetString("dir")
	if err != nil {
		return err
	}
	cfg, err := projectcfg.LoadOptional(dir)
	if err != nil {
		return err
	}

	// The frontend only runs when the web capability is installed; a CLI-only
	// project still gets its backend process.
	frontendInstalled := false
	if manifest, loadErr := project.Load(dir); loadErr == nil {
		frontendInstalled = manifest.HasCapability("web")
	}

	if err := runHooks(cmd, cfg, hook.EventDevPre, false); err != nil {
		return err
	}
	runErr := dev.Run(dev.Request{
		Dir:               dir,
		Config:            cfg,
		FrontendInstalled: frontendInstalled,
		Stdin:             cmd.InOrStdin(),
		Stdout:            cmd.OutOrStdout(),
		Stderr:            cmd.ErrOrStderr(),
	})
	// The post hook is cleanup: run it even when the session failed so a dev
	// environment is torn down, but report the session error first.
	postErr := runHooks(cmd, cfg, hook.EventDevPost, false)
	if runErr != nil {
		return runErr
	}
	return postErr
}
