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

// Apply writes every operation, attempting a best-effort rollback on the first
// failure. Each file's previous content is captured before it is overwritten,
// so an error restores what was there. The rollback is best effort: it can
// itself fail (for example on a full disk), in which case Apply returns the
// original error and the project may still be partially updated.
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

func markerToken(marker, suffix string) string { return "weld:" + marker + ":" + suffix }

// isMarkerLine reports whether line is a comment whose only content is the weld
// marker token. Markers use the host file's comment syntax, so both
// "# weld:<marker>:begin" (Makefile, .gitignore) and
// "// weld:<marker>:begin" (Go) are accepted.
func isMarkerLine(line, token string) bool {
	trimmed := strings.TrimLeft(strings.TrimSpace(line), "#/")
	return strings.TrimSpace(trimmed) == token
}

// ReplaceMarker rewrites the content of a marker region with snippet.
//
// Unlike PatchMarker it neither appends nor records a sentinel: the region is
// owned and rewritten wholesale, which is how a capability sets a value the
// base scaffold cannot know in advance (for example the go directive). The
// begin and end marker lines are preserved, so repeated applications are
// idempotent and every byte outside the region is untouched.
func ReplaceMarker(original []byte, marker string, snippet []byte) ([]byte, error) {
	lines := strings.Split(string(original), "\n")
	begin := -1
	for i, line := range lines {
		if isMarkerLine(line, markerToken(marker, "begin")) {
			begin = i
			break
		}
	}
	if begin < 0 {
		return nil, fmt.Errorf("extension point %q not found", markerToken(marker, "begin"))
	}
	end := -1
	for i := begin + 1; i < len(lines); i++ {
		if isMarkerLine(lines[i], markerToken(marker, "end")) {
			end = i
			break
		}
	}
	if end < 0 {
		return nil, fmt.Errorf("extension point %q is not closed", markerToken(marker, "begin"))
	}
	inserted := strings.Split(strings.TrimRight(string(snippet), "\n"), "\n")
	merged := make([]string, 0, len(lines)-(end-begin-1)+len(inserted))
	merged = append(merged, lines[:begin+1]...)
	merged = append(merged, inserted...)
	merged = append(merged, lines[end:]...)
	return []byte(strings.Join(merged, "\n")), nil
}

// PatchMarker inserts snippet into the named extension point of original.
//
// An extension point is a begin/end marker pair placed by the capability that
// owns the file. capability is the capability applying the patch; its sentinel
// (weld:<capability>:installed) makes repeated applications no-ops and lets
// several capabilities append to one region without shadowing each other. The
// snippet is appended to whatever the region already holds, and every byte
// outside the region is left untouched, so user edits in the same file survive.
func PatchMarker(original []byte, marker, capability string, snippet []byte) ([]byte, bool, error) {
	lines := strings.Split(string(original), "\n")
	begin := -1
	for i, line := range lines {
		if isMarkerLine(line, markerToken(marker, "begin")) {
			begin = i
			break
		}
	}
	if begin < 0 {
		return nil, false, fmt.Errorf("extension point %q not found", markerToken(marker, "begin"))
	}
	end := -1
	for i := begin + 1; i < len(lines); i++ {
		if isMarkerLine(lines[i], markerToken(marker, "end")) {
			end = i
			break
		}
	}
	if end < 0 {
		return nil, false, fmt.Errorf("extension point %q is not closed", markerToken(marker, "begin"))
	}
	region := strings.Join(lines[begin+1:end], "\n")
	if strings.Contains(region, markerToken(capability, "installed")) {
		return original, true, nil
	}
	inserted := strings.Split(strings.TrimRight(string(snippet), "\n"), "\n")
	merged := make([]string, 0, len(lines)+len(inserted))
	merged = append(merged, lines[:end]...)
	merged = append(merged, inserted...)
	merged = append(merged, lines[end:]...)
	return []byte(strings.Join(merged, "\n")), false, nil
}
