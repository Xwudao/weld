// Package project models a weld project on disk: the manifest that records
// what weld wrote, and the best-effort rollback apply of planned operations.
package project

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// ManifestName is the project manifest filename.
const ManifestName = "weld.json"

const (
	manifestKind = "weld-project"
	// manifestVersion 2 keeps a trustworthy baseline. Weld refreshes a file's
	// recorded hash only when weld itself wrote that file in the same run, so a
	// matching hash means "the project has not changed this file since weld last
	// wrote it" — the fact `weld upgrade` needs before it rewrites anything.
	//
	// Version 1 recomputed every recorded hash after every `weld add`, which
	// quietly absorbed project edits into the baseline and let an upgrade
	// overwrite them. A version 1 manifest is loaded with LegacyBaseline set, so
	// weld can say what it does not know instead of pretending.
	manifestVersion = 2
)

// Role is how weld may treat a managed file when a newer payload ships it. A
// capability declares the role of a file it ships; the manifest records it, so a
// file a newer payload no longer ships can still be classified (a seam is never
// deleted, a plain payload file may be pruned).
type Role string

const (
	// RolePayload is a file weld owns outright: upgrade rewrites it while the
	// project has not changed it, and hands over a diff when it has.
	RolePayload Role = "payload"
	// RolePatched is a file weld owns that also carries marker regions another
	// capability extends: upgrade moves the payload forward and keeps the live
	// region bodies.
	RolePatched Role = "patched"
	// RoleSeam is a file weld writes once and the project then owns: upgrade
	// never writes it and only reports that the shipped version changed.
	RoleSeam Role = "seam"
	// RoleGenerated is a file a code generator rewrites from the graph: it is
	// re-rendered, never merged, never pruned.
	RoleGenerated Role = "generated"
)

// Manifest records the provenance of every file weld wrote.
type Manifest struct {
	Kind            string          `json:"kind"`
	ManifestVersion int             `json:"manifestVersion"`
	Name            string          `json:"name"`
	Module          string          `json:"module"`
	CreatedWith     string          `json:"createdWith"`
	Base            CapabilityRef   `json:"base"`
	Capabilities    []CapabilityRef `json:"capabilities"`
	// Modules records the business modules installed with `weld add module
	// <name>`. They are not capabilities: each carries its own name, so the Loom
	// graph can be re-rendered from the record without a runtime registry. The
	// field is omitted for projects that have none, so the manifest of a
	// capabilities-only project is byte-for-byte unchanged.
	Modules []ModuleRef `json:"modules,omitempty"`
	// Commands records the root command groups installed with `weld add command
	// <name>` or `weld add module <name> --command`. A command shares its name
	// with the business module it exposes, so the two lists together distinguish
	// an independent command from a module-backed one. The field is omitted for
	// projects that have none.
	Commands []CommandRef  `json:"commands,omitempty"`
	Files    []ManagedFile `json:"files"`
	// LegacyBaseline reports that the manifest was written by a weld that
	// refreshed every hash after every add, so a matching hash is not evidence
	// that a file is unmodified. Weld keeps the conservative half of its rules
	// (a seam is never rewritten) and says so in the upgrade report. It is never
	// serialized: the rewritten manifest is version 2.
	LegacyBaseline bool `json:"-"`
}

// ModuleRef pins an installed business module to the template version that
// produced it.
type ModuleRef struct {
	Name      string `json:"name"`
	Version   string `json:"version"`
	AppliedAt string `json:"appliedAt,omitempty"`
}

// CapabilityRef pins a capability to the version that was applied.
type CapabilityRef struct {
	Name      string `json:"name"`
	Version   string `json:"version"`
	AppliedAt string `json:"appliedAt,omitempty"`
}

// ManagedFile is a file owned by weld in a project.
type ManagedFile struct {
	Path       string `json:"path"`
	Capability string `json:"capability"`
	// Role is how `weld upgrade` may treat the file, copied from the capability
	// file entry that shipped it. A legacy manifest records none, which upgrade
	// reads as RolePayload.
	Role Role `json:"role,omitempty"`
	// Baseline reports that SHA256 was recorded by weld writing this file, so a
	// match means the project has not changed it. A manifest written by an older
	// weld has none: it refreshed hashes it did not write.
	Baseline bool   `json:"baseline,omitempty"`
	SHA256   string `json:"sha256"`
}

// NewManifest builds an empty manifest for a freshly scaffolded project.
func NewManifest(name, module, createdWith string) *Manifest {
	return &Manifest{
		Kind:            manifestKind,
		ManifestVersion: manifestVersion,
		Name:            name,
		Module:          module,
		CreatedWith:     createdWith,
	}
}

// Load reads the manifest from dir.
func Load(dir string) (*Manifest, error) {
	raw, err := os.ReadFile(filepath.Join(dir, ManifestName))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("%s: not a weld project (run: weld new)", dir)
		}
		return nil, err
	}
	manifest := &Manifest{}
	if err := json.Unmarshal(raw, manifest); err != nil {
		return nil, fmt.Errorf("%s: invalid manifest: %w", ManifestName, err)
	}
	if manifest.Kind != manifestKind {
		return nil, fmt.Errorf("%s: unexpected kind %q", ManifestName, manifest.Kind)
	}
	switch {
	case manifest.ManifestVersion == manifestVersion:
	case manifest.ManifestVersion < manifestVersion:
		// A project scaffolded by an older weld: its hashes were refreshed for
		// every managed file after every add, so they cannot tell a project edit
		// from weld's own output. Record the fact instead of trusting it.
		manifest.LegacyBaseline = true
	case manifest.ManifestVersion > manifestVersion:
		return nil, fmt.Errorf("%s: manifest version %d was written by a newer weld (this weld writes %d)",
			ManifestName, manifest.ManifestVersion, manifestVersion)
	}
	manifest.ManifestVersion = manifestVersion
	return manifest, nil
}

// HasCapability reports whether a capability (base or added) is recorded.
func (m *Manifest) HasCapability(name string) bool {
	if m.Base.Name == name {
		return true
	}
	for _, capability := range m.Capabilities {
		if capability.Name == name {
			return true
		}
	}
	return false
}

// CapabilityVersion returns the recorded version for a capability.
func (m *Manifest) CapabilityVersion(name string) string {
	if m.Base.Name == name {
		return m.Base.Version
	}
	for _, capability := range m.Capabilities {
		if capability.Name == name {
			return capability.Version
		}
	}
	return ""
}

// AddCapability records an applied capability with a timestamp.
func (m *Manifest) AddCapability(ref CapabilityRef) {
	if ref.AppliedAt == "" {
		ref.AppliedAt = time.Now().UTC().Format(time.RFC3339)
	}
	m.Capabilities = append(m.Capabilities, ref)
}

// HasModule reports whether a business module is recorded.
func (m *Manifest) HasModule(name string) bool {
	for _, module := range m.Modules {
		if module.Name == name {
			return true
		}
	}
	return false
}

// ModuleVersion returns the recorded version for a business module.
func (m *Manifest) ModuleVersion(name string) string {
	for _, module := range m.Modules {
		if module.Name == name {
			return module.Version
		}
	}
	return ""
}

// AddModule records an applied business module with a timestamp.
func (m *Manifest) AddModule(ref ModuleRef) {
	if ref.AppliedAt == "" {
		ref.AppliedAt = time.Now().UTC().Format(time.RFC3339)
	}
	m.Modules = append(m.Modules, ref)
}

// CommandRef pins an installed root command group to the template version that
// produced it.
type CommandRef struct {
	Name      string `json:"name"`
	Version   string `json:"version"`
	AppliedAt string `json:"appliedAt,omitempty"`
}

// HasCommand reports whether a command group is recorded.
func (m *Manifest) HasCommand(name string) bool {
	for _, command := range m.Commands {
		if command.Name == name {
			return true
		}
	}
	return false
}

// CommandVersion returns the recorded version for a command group.
func (m *Manifest) CommandVersion(name string) string {
	for _, command := range m.Commands {
		if command.Name == name {
			return command.Version
		}
	}
	return ""
}

// AddCommand records an applied command group with a timestamp.
func (m *Manifest) AddCommand(ref CommandRef) {
	if ref.AppliedAt == "" {
		ref.AppliedAt = time.Now().UTC().Format(time.RFC3339)
	}
	m.Commands = append(m.Commands, ref)
}

// SetFile records or updates a managed file, the capability and role that
// produced it, and its content hash.
func (m *Manifest) SetFile(path, capability string, role Role, content []byte) {
	entry := ManagedFile{Path: path, Capability: capability, Role: role, Baseline: true, SHA256: HashContent(content)}
	for i := range m.Files {
		if m.Files[i].Path == path {
			m.Files[i] = entry
			return
		}
	}
	m.Files = append(m.Files, entry)
}

// SetPatchedFile records the content hash of a shared file after a marker patch
// without taking ownership of it.
//
// A marker patch extends a file another capability ships (go.mod, the Makefile,
// config.yml, internal/config/config.go). Recording the patcher as the file's
// owner made the manifest claim the file belonged to the last capability that
// touched it, which is how internal/config/config.go came to look like redis's
// file and stopped being upgradable by config. The owner stays whoever ships the
// payload; only the hash moves.
func (m *Manifest) SetPatchedFile(path, capability string, role Role, content []byte) {
	if entry, ok := m.FileEntry(path); ok {
		entry.SHA256 = HashContent(content)
		entry.Baseline = true
		if role != "" {
			entry.Role = role
		}
		for i := range m.Files {
			if m.Files[i].Path == path {
				m.Files[i] = entry
				return
			}
		}
	}
	m.SetFile(path, capability, role, content)
}

// Encode serializes the manifest with a trailing newline.
func (m *Manifest) Encode() ([]byte, error) {
	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(raw, '\n'), nil
}

// HashContent returns the hex sha256 of content.
func HashContent(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

// RefreshManifest reloads the manifest and recomputes the hash of the named
// managed files from disk, so weld records what its own tooling wrote rather
// than reporting it as project drift.
//
// Only the paths the caller names are refreshed, because a hash is the baseline
// `weld upgrade` trusts: refreshing a file weld did not write in this run would
// absorb a project edit into that baseline and license upgrade to overwrite it.
// The caller passes the files a post-apply generator rewrote (go.mod, go.sum and
// the generated Loom initializer).
func RefreshManifest(root string, paths ...string) error {
	manifest, err := Load(root)
	if err != nil {
		return err
	}
	for _, target := range paths {
		raw, err := os.ReadFile(filepath.Join(root, target))
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return err
		}
		for i := range manifest.Files {
			if manifest.Files[i].Path == target {
				manifest.Files[i].SHA256 = HashContent(raw)
				manifest.Files[i].Baseline = true
				break
			}
		}
	}
	encoded, err := manifest.Encode()
	if err != nil {
		return err
	}
	return writeAtomic(filepath.Join(root, ManifestName), encoded)
}

// RemoveFile drops a managed file from the manifest.
func (m *Manifest) RemoveFile(path string) {
	for i := range m.Files {
		if m.Files[i].Path == path {
			m.Files = append(m.Files[:i], m.Files[i+1:]...)
			return
		}
	}
}

// Owns reports whether path is a file weld manages in this project.
func (m *Manifest) Owns(path string) bool {
	_, ok := m.FileEntry(path)
	return ok
}

// FileEntry returns the record of a managed file.
func (m *Manifest) FileEntry(path string) (ManagedFile, bool) {
	for _, file := range m.Files {
		if file.Path == path {
			return file, true
		}
	}
	return ManagedFile{}, false
}

// ManagedFiles returns the files recorded under one capability, in manifest
// order.
func (m *Manifest) ManagedFiles(capability string) []ManagedFile {
	var files []ManagedFile
	for _, file := range m.Files {
		if file.Capability == capability {
			files = append(files, file)
		}
	}
	return files
}

// SetCapabilityVersion moves a recorded capability to another template version.
//
// It replaces the version in place rather than appending a second entry, so
// `weld upgrade` never grows the list, and it leaves AppliedAt alone: that
// timestamp records when the capability was first installed, which an upgrade
// does not change.
func (m *Manifest) SetCapabilityVersion(name, version string) {
	if m.Base.Name == name {
		m.Base.Version = version
		return
	}
	for i := range m.Capabilities {
		if m.Capabilities[i].Name == name {
			m.Capabilities[i].Version = version
			return
		}
	}
	m.AddCapability(CapabilityRef{Name: name, Version: version})
}

// RemoveCapability drops a recorded capability. It is used when a capability
// leaves the catalog entirely, so a project does not report it forever.
func (m *Manifest) RemoveCapability(name string) {
	for i := range m.Capabilities {
		if m.Capabilities[i].Name == name {
			m.Capabilities = append(m.Capabilities[:i], m.Capabilities[i+1:]...)
			return
		}
	}
}

// Drift describes a managed file that no longer matches the manifest.
type Drift struct {
	Path   string
	Reason string
}

// Drift returns managed files that are missing or locally modified.
func (m *Manifest) Drift(root string) []Drift {
	var drift []Drift
	for _, file := range m.Files {
		raw, err := os.ReadFile(filepath.Join(root, file.Path))
		if err != nil {
			drift = append(drift, Drift{Path: file.Path, Reason: "missing"})
			continue
		}
		if HashContent(raw) != file.SHA256 {
			drift = append(drift, Drift{Path: file.Path, Reason: "modified"})
		}
	}
	return drift
}
