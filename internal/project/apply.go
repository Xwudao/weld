package project

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Operation is a single planned file write.
//
// The final content is computed when a plan is built, so applying a plan reads
// nothing and performs no text replacement: every precondition has already been
// checked against the on-disk project.
type Operation struct {
	Path      string
	Content   []byte
	Overwrite bool
}

// ConflictError reports a precondition that would lose or clobber user data.
type ConflictError struct {
	Path   string
	Reason string
}

func (e *ConflictError) Error() string {
	return fmt.Sprintf("%s: %s", e.Path, e.Reason)
}

// Apply writes every operation, rolling back on the first failure so a project
// is never left half-updated.
func Apply(root string, operations []Operation) (err error) {
	type backup struct {
		existed bool
		content []byte
	}
	backups := make(map[string]backup, len(operations))
	defer func() {
		if err == nil {
			return
		}
		for path, previous := range backups {
			absolute := filepath.Join(root, path)
			if previous.existed {
				_ = os.WriteFile(absolute, previous.content, 0o644)
				continue
			}
			_ = os.Remove(absolute)
		}
	}()

	for _, operation := range operations {
		absolute := filepath.Join(root, operation.Path)
		existing, readErr := os.ReadFile(absolute)
		switch {
		case readErr == nil:
			if _, seen := backups[operation.Path]; !seen {
				backups[operation.Path] = backup{existed: true, content: existing}
			}
			if !operation.Overwrite {
				return &ConflictError{Path: operation.Path, Reason: "already exists"}
			}
		case errors.Is(readErr, os.ErrNotExist):
			if _, seen := backups[operation.Path]; !seen {
				backups[operation.Path] = backup{}
			}
		default:
			return readErr
		}

		if err := os.MkdirAll(filepath.Dir(absolute), 0o755); err != nil {
			return err
		}
		if err := writeAtomic(absolute, operation.Content); err != nil {
			return err
		}
	}
	return nil
}

func writeAtomic(path string, content []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".weld-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(content); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}

func markerBegin(marker string) string     { return "# weld:" + marker + ":begin" }
func markerEnd(marker string) string       { return "# weld:" + marker + ":end" }
func markerInstalled(marker string) string { return "# weld:" + marker + ":installed" }

// PatchMarker inserts snippet into the named extension point of original.
//
// An extension point is a begin/end marker pair placed by a base capability. It
// returns the new contents and whether the point was already populated, so
// repeated applications are no-ops.
func PatchMarker(original []byte, marker string, snippet []byte) ([]byte, bool, error) {
	lines := strings.Split(string(original), "\n")
	begin, end := -1, -1
	for i, line := range lines {
		trimmed := strings.TrimRight(line, " \t")
		if trimmed == markerBegin(marker) {
			begin = i
		}
		if trimmed == markerEnd(marker) {
			end = i
		}
	}
	if begin < 0 || end < 0 || end < begin {
		return nil, false, fmt.Errorf("extension point %q not found", markerBegin(marker))
	}
	region := strings.Join(lines[begin+1:end], "\n")
	if strings.Contains(region, markerInstalled(marker)) {
		return original, true, nil
	}
	inserted := strings.Split(strings.TrimRight(string(snippet), "\n"), "\n")
	merged := make([]string, 0, len(lines)+len(inserted))
	merged = append(merged, lines[:begin+1]...)
	merged = append(merged, inserted...)
	merged = append(merged, lines[end:]...)
	return []byte(strings.Join(merged, "\n")), false, nil
}
