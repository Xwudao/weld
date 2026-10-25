package cli

import (
	"encoding/json"
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/Xwudao/weld/internal/httpcontract"
)

// newRoutesCommand builds `weld routes`, which prints the project's HTTP routes
// as the source declares them, without building or starting the application.
func newRoutesCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "routes",
		Short: "list the project's HTTP routes from source, without starting it",
		Long: `List the HTTP routes the project's business modules register.

The routes are read from source: every installed module's Register function is
type-checked and its typed (httpx.Get, httpx.Post, ...) and raw (Router.Get,
Router.Handle, ...) registrations are listed with the full mount path, the
named policies that guard them ("-" is public) and the httpx.Operation ID. No
constructor runs, so it needs no config.yml, database or network service.

A registration whose router, method or path is not static, and a module whose
Register is not straight-line code, is listed under "not read" instead of being
guessed. Routes mounted outside a module's Register (a hand-written handler in
the composition) are not visible; the web capability's SPA mount is shown as
one GET / row. The running server logs its exact table at startup in a
development environment.`,
		Args: cobra.NoArgs,
		RunE: runRoutes,
	}
	cmd.Flags().Bool("json", false, "print the table as JSON")
	return cmd
}

// runRoutes reads and prints the route table.
func runRoutes(cmd *cobra.Command, args []string) error {
	dir, err := cmd.Flags().GetString("dir")
	if err != nil {
		return err
	}
	asJSON, err := cmd.Flags().GetBool("json")
	if err != nil {
		return err
	}
	table, err := httpcontract.ListRoutes(cmd.Context(), dir)
	if err != nil {
		return err
	}
	stdout := cmd.OutOrStdout()
	if asJSON {
		data, err := json.MarshalIndent(table, "", "  ")
		if err != nil {
			return err
		}
		fmt.Fprintf(stdout, "%s\n", data)
		return nil
	}
	if len(table.Routes) == 0 {
		fmt.Fprintln(stdout, "No HTTP routes found (add one with: "+progName+" add module <name>).")
	} else {
		w := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "METHOD\tPATH\tPOLICIES\tMODULE\tOPERATION")
		for _, route := range table.Routes {
			policies := strings.Join(route.Policies, ",")
			if policies == "" {
				policies = "-"
			}
			operation := route.Operation
			if operation == "" {
				operation = "-"
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", route.Method, route.Path, policies, route.Module, operation)
		}
		if err := w.Flush(); err != nil {
			return err
		}
	}
	if len(table.Unread) > 0 {
		fmt.Fprintf(stdout, "\nNot read statically (%d):\n", len(table.Unread))
		for _, entry := range table.Unread {
			fmt.Fprintf(stdout, "  %s\n", entry)
		}
	}
	return nil
}
