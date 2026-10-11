//go:build !windows

package dev

import (
	"fmt"
	"os/exec"
	"runtime"
)

// openBrowser opens target in the platform browser.
func openBrowser(target string) error {
	var name string
	switch runtime.GOOS {
	case "darwin":
		name = "open"
	case "linux":
		name = "xdg-open"
	default:
		return fmt.Errorf("unsupported platform %s", runtime.GOOS)
	}
	return exec.Command(name, target).Start()
}
