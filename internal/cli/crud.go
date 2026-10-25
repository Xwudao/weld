package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/Xwudao/weld/internal/scaffold"
	"github.com/Xwudao/weld/internal/template"
)

// newAddCrudSubcommand builds `weld add crud <name> [field:type...]`.
func newAddCrudSubcommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "crud <name> [field:type...]",
		Short: "add a database-backed CRUD resource: migration, sqlc queries, repository and HTTP module",
		Long: `Add a database-backed CRUD resource in one step: a goose migration for the
<name>s table, sqlc statements, a repository in internal/data, and a business
module under internal/modules/<name> with list, get, create, update and delete
routes. Reads are public; writes carry the project's admin policy.

Fields are name:type pairs; the types are string, int, bool, float and time,
every column is NOT NULL, and no field means name:string. For example:

  weld add crud product title:string price:float stock:int active:bool

After writing the files weld runs the pinned sqlc generator, regenerates the
Loom graph and builds the project; if any of these fails, every change is
rolled back. The files are written once and belong to the project. It requires
http and db; a missing requirement is an error unless --with-dependencies is
given.`,
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) < 1 {
				return fmt.Errorf("usage: %s add crud <name> [field:type...] [--with-dependencies] [--dir dir] [--dry-run]", progName)
			}
			return nil
		},
		RunE: runAddCrud,
	}
	cmd.Flags().Bool("with-dependencies", false, "install missing required capabilities instead of failing")
	return cmd
}

// runAddCrud plans and applies `weld add crud <name> [field:type...]`.
func runAddCrud(cmd *cobra.Command, args []string) error {
	stdout := cmd.OutOrStdout()
	dir, err := cmd.Flags().GetString("dir")
	if err != nil {
		return err
	}
	dryRun, err := cmd.Flags().GetBool("dry-run")
	if err != nil {
		return err
	}
	withDependencies, err := cmd.Flags().GetBool("with-dependencies")
	if err != nil {
		return err
	}
	result, err := scaffold.AddCrud(scaffold.Request{Dir: dir, Catalog: template.Load(), WithDependencies: withDependencies}, args[0], args[1:])
	if err != nil {
		return err
	}
	if dryRun {
		printResult(stdout, result, true)
		fmt.Fprintln(stdout, "Then: run the pinned sqlc generator, regenerate the Loom graph and go build ./...")
		return nil
	}
	return applyAddResult(stdout, result, false)
}
