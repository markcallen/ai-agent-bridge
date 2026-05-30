//go:build windows

package main

import "os/exec"

func setDetachedProcess(_ *exec.Cmd) {
	// On Windows, child processes are already independent.
}
