package dev

import (
	"crypto/sha256"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"strings"
	"time"
)

const (
	watchInterval = 250 * time.Millisecond
	watchDebounce = 300 * time.Millisecond
)

// fileWatcher keeps content fingerprints so touching a file or rewriting it
// with identical bytes does not restart a healthy backend.
type fileWatcher struct {
	dir              string
	include, exclude []string
	previous         map[string][sha256.Size]byte
	changedAt        time.Time
}

func newFileWatcher(dir string, include, exclude []string) (*fileWatcher, error) {
	for _, group := range []struct {
		name     string
		patterns []string
	}{{"watch", include}, {"watch_exclude", exclude}} {
		for _, pattern := range group.patterns {
			if err := validateWatchGlob(pattern); err != nil {
				return nil, fmt.Errorf("weld.yml: dev.backend.%s: %w", group.name, err)
			}
		}
	}
	if len(include) == 0 {
		return nil, nil
	}
	if dir == "" {
		dir = "."
	}
	w := &fileWatcher{dir: dir, include: include, exclude: exclude}
	var err error
	w.previous, err = w.snapshot()
	if err != nil {
		return nil, err
	}
	return w, nil
}

func validateWatchGlob(pattern string) error {
	if pattern == "" || strings.HasPrefix(pattern, "/") || strings.Contains(pattern, "\\") {
		return fmt.Errorf("invalid glob %q: use a project-relative path with forward slashes", pattern)
	}
	for _, segment := range strings.Split(pattern, "/") {
		if segment == ".." || segment == "." || segment == "" || strings.Contains(segment, ":") {
			return fmt.Errorf("invalid project-relative glob %q", pattern)
		}
		if strings.Contains(segment, "**") && segment != "**" {
			return fmt.Errorf("invalid glob %q: ** must be a complete path segment", pattern)
		}
		if _, err := path.Match(segment, ""); err != nil {
			return fmt.Errorf("invalid glob %q: %w", pattern, err)
		}
	}
	return nil
}

// matchWatchGlob adds recursive ** segments to the standard path.Match syntax.
func matchWatchGlob(pattern, name string) bool {
	parts, names := strings.Split(pattern, "/"), strings.Split(name, "/")
	// Dynamic programming avoids exponential backtracking with repeated **.
	matches := make([]bool, len(names)+1)
	matches[0] = true
	for _, part := range parts {
		next := make([]bool, len(matches))
		if part == "**" {
			next[0] = matches[0]
			for i := 1; i < len(next); i++ {
				next[i] = matches[i] || next[i-1]
			}
		} else {
			for i, name := range names {
				ok, _ := path.Match(part, name)
				next[i+1] = matches[i] && ok
			}
		}
		matches = next
	}
	return matches[len(names)]
}

func matchesWatchGlobs(patterns []string, name string) bool {
	for _, pattern := range patterns {
		if matchWatchGlob(pattern, name) {
			return true
		}
	}
	return false
}

func (w *fileWatcher) snapshot() (map[string][sha256.Size]byte, error) {
	files := make(map[string][sha256.Size]byte)
	err := filepath.WalkDir(w.dir, func(file string, entry fs.DirEntry, err error) error {
		if os.IsNotExist(err) {
			return nil // An editor may remove a file during the scan.
		}
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if file != w.dir && (entry.Name() == ".git" || entry.Name() == ".weld" || entry.Name() == "node_modules") {
				return filepath.SkipDir
			}
			return nil
		}
		if !entry.Type().IsRegular() {
			return nil // Never follow symlinks outside the project or read pipes.
		}
		rel, err := filepath.Rel(w.dir, file)
		if err != nil {
			return err
		}
		name := filepath.ToSlash(rel)
		if !matchesWatchGlobs(w.include, name) || matchesWatchGlobs(w.exclude, name) {
			return nil
		}
		data, err := os.ReadFile(file)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		files[name] = sha256.Sum256(data)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("scan backend watch files: %w", err)
	}
	return files, nil
}

// poll waits for a quiet period before requesting a restart. Failed scans keep
// the last complete baseline, rather than treating unreadable files as deleted.
func (w *fileWatcher) poll(now time.Time) (bool, error) {
	current, err := w.snapshot()
	if err != nil {
		return false, err
	}
	if !reflect.DeepEqual(current, w.previous) {
		w.previous = current
		w.changedAt = now
	}
	if !w.changedAt.IsZero() && now.Sub(w.changedAt) >= watchDebounce {
		w.changedAt = time.Time{}
		return true, nil
	}
	return false, nil
}
