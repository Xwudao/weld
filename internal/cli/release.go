package cli

import (
	"fmt"
	"path"

	"github.com/spf13/cobra"

	"github.com/Xwudao/weld/internal/project"
	"github.com/Xwudao/weld/internal/projectcfg"
	"github.com/Xwudao/weld/internal/release"
)

// checksumFile is the checksum file a release writes into the build output
// directory.
const checksumFile = "SHA256SUMS"

// newReleaseCommand builds `weld release <version>`.
func newReleaseCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "release <version>",
		Short: "tag a version locally, build it and write checksums",
		Long: `Cut a local release of the project.

release checks that the project is a git repository with no uncommitted change
to a tracked file, that <version> (MAJOR.MINOR.PATCH, with or without a leading
v) is not tagged yet and is newer than the latest version tag, and that there
are commits since that tag. It then:

  1. writes release notes from the commit subjects since the previous tag,
     grouping Conventional Commits into breaking changes, features, fixes and
     other changes;
  2. creates an annotated tag at HEAD whose message is those notes;
  3. builds the binaries exactly as weld build does (weld.yml targets, ldflags
     and the build.pre/build.post hooks), so a ${git_tag} ldflag stamps the new
     version in;
  4. writes SHA256SUMS next to the binaries in the build output directory.

When the build fails the tag is deleted again. --no-build only tags. --dry-run
prints the notes and the plan and changes nothing.

release never pushes and never publishes anything: the tag and the binaries
stay on this machine until you push the tag yourself.`,
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) != 1 {
				return fmt.Errorf("usage: %s release <version> [--no-build] [--dir dir] [--dry-run]", progName)
			}
			return nil
		},
		RunE: runRelease,
	}
	cmd.Flags().Bool("no-build", false, "only create the tag")
	return cmd
}

// runRelease plans and, unless it is a dry run, applies a release.
func runRelease(cmd *cobra.Command, args []string) error {
	stdout := cmd.OutOrStdout()
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
	manifest, err := project.Load(dir)
	if err != nil {
		return err
	}
	cfg, err := projectcfg.LoadOptional(dir)
	if err != nil {
		return err
	}

	req := release.Request{Dir: dir, Version: args[0], Git: release.CommandGit()}
	if !noBuild {
		req.ChecksumFile = path.Join(cfg.Output(), checksumFile)
		req.Build = func() ([]string, error) {
			result, err := buildHost(cmd, dir, manifest.Name, cfg, manifest.HasCapability("web"))
			if err != nil {
				return nil, err
			}
			return result.Binaries, nil
		}
	}
	plan, err := release.Prepare(req)
	if err != nil {
		return err
	}

	if dryRun {
		fmt.Fprintf(stdout, "Plan (release %s, dry run):\n", plan.Tag)
		fmt.Fprintf(stdout, "  tag %s at %s\n", plan.Tag, shortCommit(plan.Commit))
		if !noBuild {
			fmt.Fprintf(stdout, "  build into %s/\n  write %s\n", cfg.Output(), req.ChecksumFile)
		}
		fmt.Fprintf(stdout, "\nRelease notes:\n\n%s", plan.Notes)
		return nil
	}
	result, err := plan.Apply()
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Tagged %s at %s (local only)\n", plan.Tag, shortCommit(plan.Commit))
	if result.Checksums != "" {
		fmt.Fprintf(stdout, "wrote %s\n", result.Checksums)
	}
	fmt.Fprintf(stdout, "\nRelease notes:\n\n%s\nNext (publishes the tag):\n  git push origin %s\n", plan.Notes, plan.Tag)
	return nil
}

// shortCommit abbreviates a commit hash for display.
func shortCommit(commit string) string {
	if len(commit) > 12 {
		return commit[:12]
	}
	return commit
}
