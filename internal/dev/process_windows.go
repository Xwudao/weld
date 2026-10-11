//go:build windows

package dev

import (
	"os"
	"os/exec"
)

// configureProcess is a no-op on Windows, which has no process groups.
func configureProcess(cmd *exec.Cmd) {}

// interruptProcess asks the child to stop.
func interruptProcess(cmd *exec.Cmd) error {
	return cmd.Process.Signal(os.Interrupt)
}

// killProcess terminates the child after a graceful stop timed out.
func killProcess(cmd *exec.Cmd) error {
	return cmd.Process.Kill()
}
