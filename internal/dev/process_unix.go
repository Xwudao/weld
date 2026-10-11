//go:build !windows

package dev

import (
	"os/exec"
	"syscall"
)

// configureProcess puts the child in its own process group, so a Ctrl-C sent to
// the group interrupts the whole tree (go run, vite, their children) rather than
// orphaning the grandchildren.
func configureProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// interruptProcess sends SIGINT to the child's process group.
func interruptProcess(cmd *exec.Cmd) error {
	return syscall.Kill(-cmd.Process.Pid, syscall.SIGINT)
}

// killProcess sends SIGKILL to the child's process group after a graceful stop
// timed out.
func killProcess(cmd *exec.Cmd) error {
	return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}
