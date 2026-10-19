// Package migration allocates and parses goose migration versions.
//
// goose identifies a migration by the integer prefix of its filename,
// "<version>_<name>.sql", and applies pending migrations in version order. A
// version must be unique inside the directory and must not sort before one the
// directory already holds: goose treats such a file as out of order and refuses
// it unless it is run with --allow-missing.
//
// weld writes migrations from two places: the `weld db create` skeleton and a
// capability payload installed later. Both take their version from this package,
// so a project has one numbering convention. A sequential convention for shipped
// payloads and a timestamped one for the project's own files is what forced an
// installed capability to be applied with --allow-missing, because the payload's
// fixed number always sorted before migrations the project wrote itself.
package migration

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Layout is the goose timestamp layout, in UTC. It is the format goose's own
// create command uses by default, so a weld-created migration sorts exactly like
// one created by hand.
const Layout = "20060102150405"

// Version returns the goose version for t: the UTC timestamp as the decimal
// integer goose compares.
func Version(t time.Time) int64 {
	value, err := strconv.ParseInt(t.UTC().Format(Layout), 10, 64)
	if err != nil {
		// Layout is a fixed all-digit layout, so a parse error cannot happen. Zero
		// is never a valid version for a real timestamp, so a caller that somehow
		// reaches this branch writes no ordering claim instead of a garbage one.
		return 0
	}
	return value
}

// Format returns the decimal version prefix a filename carries.
func Format(version int64) string {
	return strconv.FormatInt(version, 10)
}

// Name formats the filename for a version and a lower_snake_case name.
func Name(version int64, name string) string {
	return Format(version) + "_" + name + ".sql"
}

// Parse returns the version prefix of a migration filename.
func Parse(filename string) (int64, bool) {
	base := filepath.Base(filename)
	i := strings.IndexByte(base, '_')
	if i <= 0 {
		return 0, false
	}
	version, err := strconv.ParseInt(base[:i], 10, 64)
	if err != nil {
		return 0, false
	}
	return version, true
}

// Highest returns the highest version in dir, or 0 when the directory is missing
// or holds no migration.
func Highest(dir string) (int64, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	var highest int64
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		if version, ok := Parse(entry.Name()); ok && version > highest {
			highest = version
		}
	}
	return highest, nil
}

// Allocator hands out versions for one migration directory.
type Allocator struct {
	dir  string
	now  func() time.Time
	last int64
	read bool
}

// NewAllocator returns an allocator for dir. A nil clock uses time.Now.
func NewAllocator(dir string, now func() time.Time) *Allocator {
	if now == nil {
		now = time.Now
	}
	return &Allocator{dir: dir, now: now}
}

// Next returns the next version and advances the allocator: the later of the
// clock and one past the highest version the directory held when the allocator
// was first used.
//
// Following the clock keeps the filename readable as when the migration was
// written. Following the directory is what keeps a migration from sorting before
// one that is already there, so a capability installed into an existing project
// never needs --allow-missing and two migrations planned in the same second never
// share a version. A file dated in the future pushes the next version past it
// instead of being ignored.
func (a *Allocator) Next() (int64, error) {
	if !a.read {
		highest, err := Highest(a.dir)
		if err != nil {
			return 0, err
		}
		a.last = highest
		a.read = true
	}
	version := Version(a.now())
	if version <= a.last {
		version = a.last + 1
	}
	a.last = version
	return version, nil
}
