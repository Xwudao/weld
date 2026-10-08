package project

import (
	"errors"
	"os"
	"path/filepath"
)

// fileState records whether a file existed and, if so, its content.
type fileState struct {
	existed bool
	content []byte
}

// Snapshot captures the content of paths so a failed post-apply step (such as a
// code generator) can be rolled back exactly. A path that does not exist is
// recorded as absent and is removed on restore.
type Snapshot struct {
	states map[string]fileState
}

// TakeSnapshot captures paths relative to root.
func TakeSnapshot(root string, paths []string) (*Snapshot, error) {
	snapshot := &Snapshot{states: make(map[string]fileState, len(paths))}
	for _, path := range paths {
		if _, seen := snapshot.states[path]; seen {
			continue
		}
		content, err := os.ReadFile(filepath.Join(root, path))
		switch {
		case err == nil:
			snapshot.states[path] = fileState{existed: true, content: content}
		case errors.Is(err, os.ErrNotExist):
			snapshot.states[path] = fileState{}
		default:
			return nil, err
		}
	}
	return snapshot, nil
}

// Restore rewrites every captured path to its previous state, deleting files
// that did not exist. It is best effort: it returns the first error but attempts
// every path.
func (s *Snapshot) Restore(root string) error {
	var firstErr error
	record := func(err error) {
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}
	for path, state := range s.states {
		absolute := filepath.Join(root, path)
		if !state.existed {
			if err := os.Remove(absolute); err != nil && !errors.Is(err, os.ErrNotExist) {
				record(err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(absolute), 0o755); err != nil {
			record(err)
			continue
		}
		record(writeAtomic(absolute, state.content))
	}
	return firstErr
}
