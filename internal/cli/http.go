package cli

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Xwudao/weld/internal/httpcontract"
	"github.com/Xwudao/weld/internal/project"
	"github.com/spf13/cobra"
)

func newHTTPCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "http", Short: "export typed JSON interfaces without starting the application", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error { return cmd.Help() }}
	export := &cobra.Command{Use: "export", Short: "export a lightweight HTTP contract from typed module routes", Args: cobra.NoArgs, RunE: runHTTPExport}
	export.Flags().String("out", "weld-api.json", "contract output, relative to --dir")
	export.Flags().String("module", "", "export one HTTP module (default all installed modules)")
	export.Flags().String("prefix", "", "explicit mount override; requires --module")
	export.Flags().Bool("force", false, "overwrite an existing changed contract")
	cmd.AddCommand(export)
	return cmd
}

func runHTTPExport(cmd *cobra.Command, _ []string) error {
	dir, _ := cmd.Flags().GetString("dir")
	out, _ := cmd.Flags().GetString("out")
	module, _ := cmd.Flags().GetString("module")
	force, _ := cmd.Flags().GetBool("force")
	opts := httpcontract.ExportOptions{Module: module}
	if cmd.Flags().Changed("prefix") {
		p, _ := cmd.Flags().GetString("prefix")
		opts.Prefix = &p
	}
	contract, err := httpcontract.Export(cmd.Context(), dir, opts)
	if err != nil {
		return err
	}
	data, err := contract.JSON()
	if err != nil {
		return err
	}
	for _, skipped := range contract.Skipped {
		fmt.Fprintf(cmd.ErrOrStderr(), "Skipped %s\n", skipped)
	}
	if err := applyHTTPArtifacts(cmd, dir, map[string][]byte{out: data}, force, false); err != nil {
		return err
	}
	verb := "Exported"
	if dry, _ := cmd.Flags().GetBool("dry-run"); dry {
		verb = "Would export"
	}
	fmt.Fprintf(cmd.OutOrStdout(), "%s %d typed operation(s), %d DTO(s).\n", verb, len(contract.Operations), len(contract.Types))
	return nil
}

func newClientCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "client", Short: "generate standalone Go clients from weld HTTP contracts", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error { return cmd.Help() }}
	add := &cobra.Command{Use: "add <name>", Short: "generate or refresh a standard-library-only typed HTTP client", Args: cobra.ExactArgs(1), RunE: runClientAdd}
	add.Flags().String("from", "", "contract file (relative to --dir, or absolute)")
	add.Flags().String("out", "", "output package directory (default internal/clients/<name>)")
	add.Flags().Bool("force", false, "overwrite files not marked as weld-generated")
	_ = add.MarkFlagRequired("from")
	cmd.AddCommand(add)
	return cmd
}

func runClientAdd(cmd *cobra.Command, args []string) error {
	dir, _ := cmd.Flags().GetString("dir")
	from, _ := cmd.Flags().GetString("from")
	out, _ := cmd.Flags().GetString("out")
	force, _ := cmd.Flags().GetBool("force")
	// Reading from another project is deliberate; output stays inside --dir.
	if !filepath.IsAbs(from) {
		from = filepath.Join(dir, from)
	}
	file, err := os.Open(from)
	if err != nil {
		return err
	}
	defer file.Close()
	contract, err := httpcontract.Decode(file)
	if err != nil {
		return err
	}
	files, err := httpcontract.Generate(contract, args[0])
	if err != nil {
		return err
	}
	if out == "" {
		out = filepath.Join("internal", "clients", args[0])
	}
	if !filepath.IsLocal(out) {
		return fmt.Errorf("client output must be a local path inside --dir")
	}
	artifacts := map[string][]byte{}
	for name, data := range files {
		artifacts[filepath.Join(out, name)] = data
	}
	stale, err := obsoleteClientEnums(dir, out, files)
	if err != nil {
		return err
	}
	if err := applyHTTPArtifacts(cmd, dir, artifacts, force, true, stale...); err != nil {
		return err
	}
	verb := "Generated"
	if dry, _ := cmd.Flags().GetBool("dry-run"); dry {
		verb = "Would generate"
	}
	fmt.Fprintf(cmd.OutOrStdout(), "%s %s client with %d operation(s).\n", verb, args[0], len(contract.Operations))
	return nil
}

// Only obsolete enum files carrying the client ownership marker are removed.
func obsoleteClientEnums(dir, out string, files map[string][]byte) ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(dir, out))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var stale []string
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || !strings.HasPrefix(name, "enum_") || !strings.HasSuffix(name, "_gen.go") || files[name] != nil {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, out, name))
		if err != nil {
			return nil, err
		}
		if bytes.HasPrefix(data, []byte(httpcontract.ClientHeader+"\n")) {
			stale = append(stale, filepath.Join(out, name))
		}
	}
	return stale, nil
}

// Preflight every file before writing any, then use the existing atomic write
// and rollback helper. Dry runs execute the exact same validation and planning.
func applyHTTPArtifacts(cmd *cobra.Command, dir string, files map[string][]byte, force, generated bool, deleteFiles ...string) error {
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	deletes := map[string]bool{}
	for _, name := range deleteFiles {
		if _, exists := files[name]; exists {
			return fmt.Errorf("cannot write and delete %s", name)
		}
		names = append(names, name)
		deletes[name] = true
	}
	sort.Strings(names)
	var operations []project.Operation
	for _, name := range names {
		if !filepath.IsLocal(name) {
			return fmt.Errorf("output %q must be a local path inside --dir", name)
		}
		path := filepath.Join(dir, name)
		for parent := filepath.Dir(name); parent != "."; parent = filepath.Dir(parent) {
			info, err := os.Lstat(filepath.Join(dir, parent))
			if err != nil && !os.IsNotExist(err) {
				return err
			}
			if err == nil && info.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("refusing output through symlink %s", filepath.Join(dir, parent))
			}
		}
		if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing to overwrite symlink %s", path)
		}
		existing, err := os.ReadFile(path)
		if deletes[name] {
			if os.IsNotExist(err) {
				continue
			}
			if err != nil {
				return err
			}
			if !bytes.HasPrefix(existing, []byte(httpcontract.ClientHeader+"\n")) {
				return fmt.Errorf("refusing to delete hand-owned file %s", path)
			}
			operations = append(operations, project.Operation{Path: name, Delete: true})
			continue
		}
		if err == nil {
			if bytes.Equal(existing, files[name]) {
				continue
			}
			owned := generated && strings.HasPrefix(string(existing), httpcontract.ClientHeader+"\n")
			if !force && !owned {
				return fmt.Errorf("%s already exists and differs; use --force to overwrite", path)
			}
		} else if !os.IsNotExist(err) {
			return err
		}
		operations = append(operations, project.Operation{Path: name, Content: files[name], Overwrite: err == nil})
	}
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	if dryRun {
		fmt.Fprintf(cmd.OutOrStdout(), "Plan (%d change(s), dry run):\n", len(operations))
		for _, op := range operations {
			action := "write"
			if op.Delete {
				action = "delete"
			}
			fmt.Fprintf(cmd.OutOrStdout(), "  %s %s\n", action, filepath.Join(dir, op.Path))
		}
		return nil
	}
	if err := project.Apply(dir, operations); err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Applied %d change(s).\n", len(operations))
	return nil
}
