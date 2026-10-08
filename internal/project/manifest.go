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
	manifestKind    = "weld-project"
	manifestVersion = 1
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
	Files           []ManagedFile   `json:"files"`
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
	SHA256     string `json:"sha256"`
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

// SetFile records or updates a managed file and its content hash.
func (m *Manifest) SetFile(path, capability string, content []byte) {
	entry := ManagedFile{Path: path, Capability: capability, SHA256: HashContent(content)}
	for i := range m.Files {
		if m.Files[i].Path == path {
			m.Files[i] = entry
			return
		}
	}
	m.Files = append(m.Files, entry)
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

// Owns reports whether path is a file weld manages in this project.
func (m *Manifest) Owns(path string) bool {
	for _, file := range m.Files {
		if file.Path == path {
			return true
		}
	}
	return false
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
