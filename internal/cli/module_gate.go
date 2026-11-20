package cli

import (
	"github.com/Xwudao/weld/internal/project"
	"github.com/Xwudao/weld/internal/scaffold"
)

// loadBuildTestManifest enforces module registration without running the other
// doctor checks or requiring a manifest in a plain Go project.
func loadBuildTestManifest(dir string) (*project.Manifest, error) {
	manifest, err := project.LoadOptional(dir)
	if err != nil || manifest == nil {
		return manifest, err
	}
	if err := scaffold.CheckModuleRegistration(dir, manifest); err != nil {
		return nil, err
	}
	return manifest, nil
}
