package scaffold

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/Xwudao/weld/internal/project"
)

// RemoveModule plans removal of a business module and its optional command
// facet, leaving shared capabilities and database artifacts installed.
func RemoveModule(req Request, name string, deleteSource bool) (*Result, error) {
	if err := ValidateModuleName(name); err != nil {
		return nil, err
	}
	if _, err := removalStat(req.Dir, project.ManifestName); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	manifest, err := project.Load(req.Dir)
	if err != nil {
		return nil, err
	}
	manifestContent, err := os.ReadFile(filepath.Join(req.Dir, project.ManifestName))
	if err != nil {
		return nil, err
	}
	manifestHash := project.HashContent(manifestContent)
	result := &Result{Dir: req.Dir}
	if !manifest.HasModule(name) {
		return nil, fmt.Errorf("module %q is not installed", name)
	}
	withCommand := manifest.HasCommand(name)
	moduleDir := ModuleDir(name)
	moduleOwner, commandOwner := "module:"+name, "command:"+name
	commandDir := "internal/commands/" + name
	commandRegistration := "internal/app/" + name + "_command.go"
	commandGraph := "internal/di/" + name + "_graph.go"
	isCommandFile := func(file string) bool {
		return withCommand && (strings.HasPrefix(file, commandDir+"/") || file == commandRegistration || file == commandGraph)
	}

	files := map[string]project.ManagedFile{}
	var commandFiles []string
	keepsDatabaseFiles := false
	for _, entry := range manifest.Files {
		file := entry.Path
		switch {
		case strings.HasPrefix(file, moduleDir+"/"):
			if entry.Capability != moduleOwner {
				return nil, &project.ConflictError{Path: file, Reason: "owned by " + entry.Capability + ", not this module"}
			}
			files[file] = entry
		case isCommandFile(file) && entry.Capability == commandOwner:
			files[file] = entry
			commandFiles = append(commandFiles, file)
		case entry.Capability == moduleOwner:
			keepsDatabaseFiles = true
		}
	}
	// A command can register at init time independently of the generated HTTP
	// graph. Do not silently leave an unowned registration or graph behind.
	if withCommand {
		for _, file := range []string{commandRegistration, commandGraph} {
			if _, owned := files[file]; owned {
				continue
			}
			if _, err := removalStat(req.Dir, file); !errors.Is(err, os.ErrNotExist) {
				if err != nil {
					return nil, err
				}
				return nil, &project.ConflictError{Path: file, Reason: "cannot remove module command wiring without a matching owner in weld.json"}
			}
			// Also recheck paths that were absent at planning time.
			commandFiles = append(commandFiles, file)
		}
	}
	slices.Sort(commandFiles)
	paths, dirs, err := moduleRemovalFiles(req.Dir, moduleDir, commandFiles)
	if err != nil {
		return nil, err
	}

	p := newPlanner(req, manifest, result)
	var changed []string
	for _, file := range slices.Sorted(maps.Keys(paths)) {
		entry, tracked := files[file]
		if !tracked || manifest.LegacyBaseline || !entry.Baseline || entry.SHA256 != paths[file].hash {
			changed = append(changed, file)
		}
		p.upsert(project.Operation{Path: file, Delete: true})
	}
	if len(changed) > 0 {
		list := "  " + strings.Join(changed, "\n  ")
		if !deleteSource {
			return nil, &project.ConflictError{Path: moduleDir, Reason: fmt.Sprintf("modified, untracked, or unverified files; review with `weld remove module %s --delete-source --dry-run`:\n%s", name, list)}
		}
		result.Notes = append(result.Notes, "Removing modified, untracked, or unverified files:\n"+list)
	}
	if keepsDatabaseFiles {
		result.Notes = append(result.Notes, "Module files outside its source directory (including database artifacts) are kept.")
	}

	manifest.Modules = slices.DeleteFunc(manifest.Modules, func(ref project.ModuleRef) bool { return ref.Name == name })
	manifest.Commands = slices.DeleteFunc(manifest.Commands, func(ref project.CommandRef) bool { return withCommand && ref.Name == name })
	manifest.Files = slices.DeleteFunc(manifest.Files, func(entry project.ManagedFile) bool {
		_, removing := files[entry.Path]
		return removing
	})
	manifest.Pending = slices.DeleteFunc(manifest.Pending, func(change project.PendingChange) bool {
		_, removing := files[change.Path]
		return removing
	})
	if err := p.finish(); err != nil {
		return nil, err
	}
	if p.present["loom"] {
		result.GenerateLoom = true
		result.DIPackage = "internal/di"
	}
	result.Verify = []string{"build", "./..."}
	result.pruneDirs = dirs
	result.preflight = func() error {
		for _, op := range result.Operations {
			if _, err := removalStat(req.Dir, op.Path); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
		if result.GenerateLoom {
			file := path.Join(result.DIPackage, "loom_gen.go")
			if _, err := removalStat(req.Dir, file); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
		content, err := os.ReadFile(filepath.Join(req.Dir, project.ManifestName))
		if err != nil {
			return err
		}
		if project.HashContent(content) != manifestHash {
			return &project.ConflictError{Path: project.ManifestName, Reason: "manifest changed after planning; run the removal again"}
		}
		current, currentDirs, err := moduleRemovalFiles(req.Dir, moduleDir, commandFiles)
		if err != nil {
			return err
		}
		if !maps.Equal(paths, current) || !slices.Equal(dirs, currentDirs) {
			return &project.ConflictError{Path: moduleDir, Reason: "module files changed after planning; run the removal again"}
		}
		return nil
	}
	return result, nil
}

type removalFile struct {
	hash string
	mode os.FileMode
}

// moduleRemovalFiles inventories regular files, including untracked files in
// the module directory. Reject links and special files: the rollback machinery
// snapshots regular files, and must not follow links out of the removal scope.
func moduleRemovalFiles(root, moduleDir string, commandFiles []string) (map[string]removalFile, []string, error) {
	files := map[string]removalFile{}
	var dirs []string
	add := func(file string, info os.FileInfo) error {
		if !info.Mode().IsRegular() {
			return &project.ConflictError{Path: file, Reason: "cannot remove a non-regular file"}
		}
		content, err := os.ReadFile(filepath.Join(root, file))
		if err != nil {
			return err
		}
		files[file] = removalFile{hash: project.HashContent(content), mode: info.Mode()}
		return nil
	}
	if info, err := removalStat(root, moduleDir); err == nil {
		if !info.IsDir() {
			return nil, nil, &project.ConflictError{Path: moduleDir, Reason: "not a directory"}
		}
		if err := filepath.WalkDir(filepath.Join(root, moduleDir), func(abs string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(root, abs)
			if err != nil {
				return err
			}
			file := filepath.ToSlash(rel)
			if strings.EqualFold(d.Name(), ".git") {
				return &project.ConflictError{Path: file, Reason: "cannot remove nested Git metadata"}
			}
			if d.IsDir() {
				dirs = append(dirs, file)
				return nil
			}
			info, err := d.Info()
			if err != nil {
				return err
			}
			return add(file, info)
		}); err != nil {
			return nil, nil, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, nil, err
	}
	for _, file := range commandFiles {
		info, err := removalStat(root, file)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, nil, err
		}
		if err := add(file, info); err != nil {
			return nil, nil, err
		}
	}
	// WalkDir visits parents first; prune children first after apply succeeds.
	slices.Reverse(dirs)
	return files, dirs, nil
}

// removalStat checks every path component below the project root rather than
// following a symlink to a target outside the requested module.
func removalStat(root, file string) (os.FileInfo, error) {
	if !filepath.IsLocal(file) || path.Clean(file) != file || strings.Contains(file, `\`) {
		return nil, &project.ConflictError{Path: file, Reason: "invalid project-relative path"}
	}
	parts := strings.Split(file, "/")
	var info os.FileInfo
	for i := range parts {
		rel := path.Join(parts[:i+1]...)
		var err error
		info, err = os.Lstat(filepath.Join(root, rel))
		if err != nil {
			return nil, err
		}
		if info.Mode()&os.ModeSymlink != 0 || (i < len(parts)-1 && !info.IsDir()) {
			return nil, &project.ConflictError{Path: rel, Reason: "cannot remove through a symlink or non-directory parent"}
		}
	}
	return info, nil
}
