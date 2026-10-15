package cli

import (
	"github.com/spf13/cobra"

	"github.com/Xwudao/weld/internal/hook"
	"github.com/Xwudao/weld/internal/projectcfg"
	"github.com/Xwudao/weld/internal/seed"
)

// newSeedCommand builds `weld seed`.
func newSeedCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "seed [name]",
		Short: "run the project's database seeders",
		Long: `Run the project's database seeders.

The generated seeders live behind the seed build tag, so the production binary
(go build, weld build) compiles in neither the seed data nor any faker a seeder
imports. weld seed compiles the tag in and runs the project's short seed command,
optionally naming one seeder:

  weld seed          every registered seeder, in registration order
  weld seed items    only the "items" seeder

The project must have the seed capability installed (weld add seed).`,
		Args: cobra.MaximumNArgs(1),
		RunE: runSeedCommand,
	}
	cmd.Flags().String("tag", "", "build tag that compiles the seeders (default: seed, or weld.yml seed.tag)")
	return cmd
}

// runSeedCommand runs a seed session.
func runSeedCommand(cmd *cobra.Command, args []string) error {
	dir, err := cmd.Flags().GetString("dir")
	if err != nil {
		return err
	}
	tag, err := cmd.Flags().GetString("tag")
	if err != nil {
		return err
	}
	name := ""
	if len(args) == 1 {
		name = args[0]
	}
	cfg, err := projectcfg.LoadOptional(dir)
	if err != nil {
		return err
	}
	effectiveTag := tag
	if effectiveTag == "" {
		effectiveTag = cfg.SeedTag()
	}
	seedEnv := []string{"WELD_SEED_TAG=" + effectiveTag}
	if err := runHooks(cmd, cfg, hook.EventSeedPre, false, seedEnv...); err != nil {
		return err
	}
	runErr := seed.Run(seed.Request{
		Dir:    dir,
		Name:   name,
		Tag:    tag,
		Stdout: cmd.OutOrStdout(),
		Stderr: cmd.ErrOrStderr(),
	})
	postErr := runHooks(cmd, cfg, hook.EventSeedPost, false, seedEnv...)
	if runErr != nil {
		return runErr
	}
	return postErr
}
