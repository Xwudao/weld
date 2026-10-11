//go:build windows

package dev

import "os/exec"

// openBrowser opens target in the default browser through the shell URL
// handler.
func openBrowser(target string) error {
	return exec.Command("rundll32", "url.dll,FileProtocolHandler", target).Start()
}
