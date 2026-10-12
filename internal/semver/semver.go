// Package semver compares the simple version strings weld records.
//
// It deliberately implements only what weld writes: a dotted numeric version
// with one to three components and an optional leading "v". No capability
// version carries pre-release or build metadata, and a version weld cannot
// compare is an error rather than a guess: `weld upgrade` must never invent that
// an upgrade is available.
package semver

import (
	"fmt"
	"strconv"
	"strings"
)

// Compare returns -1 when a sorts before b, 0 when they are equal and 1 when a
// sorts after b. Both versions must be parseable.
func Compare(a, b string) (int, error) {
	left, err := parse(a)
	if err != nil {
		return 0, err
	}
	right, err := parse(b)
	if err != nil {
		return 0, err
	}
	for i := range left {
		switch {
		case left[i] < right[i]:
			return -1, nil
		case left[i] > right[i]:
			return 1, nil
		}
	}
	return 0, nil
}

// IsNewer reports whether candidate is strictly newer than installed. An
// unparseable version on either side is an error, not a silent false.
func IsNewer(candidate, installed string) (bool, error) {
	order, err := Compare(candidate, installed)
	if err != nil {
		return false, err
	}
	return order > 0, nil
}

// Valid reports whether s is a version weld can compare.
func Valid(s string) bool {
	_, err := parse(s)
	return err == nil
}

// parse reads major.minor.patch, padding a missing component with zero.
func parse(s string) ([3]int, error) {
	var parts [3]int
	trimmed := strings.TrimPrefix(strings.TrimSpace(s), "v")
	if trimmed == "" {
		return parts, fmt.Errorf("version %q is empty", s)
	}
	fields := strings.Split(trimmed, ".")
	if len(fields) > 3 {
		return parts, fmt.Errorf("version %q has more than three components", s)
	}
	for i, field := range fields {
		value, err := strconv.Atoi(field)
		if err != nil || value < 0 {
			return parts, fmt.Errorf("version %q is not a dotted numeric version", s)
		}
		parts[i] = value
	}
	return parts, nil
}
