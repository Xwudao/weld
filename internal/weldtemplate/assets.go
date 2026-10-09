// Package weldtemplate ships the file payloads consumed by the weld CLI.
//
// Payloads are embedded so a weld binary carries its scaffold and capabilities
// with it: no network access or template checkout is needed at run time. Each
// capability lives in capabilities/<name>/ with a capability.json descriptor
// and a files/ payload directory. The per-name `weld add module <name>` payload
// lives in modules/ with a module.json descriptor and a files/ payload
// directory; it is not a capability because its files and paths are generated
// per module name rather than declared once. The per-name `weld add command
// <name>` payload likewise lives in commands/, carrying both the independent
// (generic) command group and the module-backed variant.
//
// This package is the embedded payload boundary within the weld module.
// The CLI imports FS() and reads descriptors from it.
package weldtemplate

import (
	"embed"
	"io/fs"
)

// Version is the template payload version recorded in generated projects.
const Version = "0.3.2"

//go:embed all:capabilities all:modules all:commands
var assets embed.FS

// FS returns the embedded template assets rooted at this package directory.
func FS() fs.FS {
	return assets
}
