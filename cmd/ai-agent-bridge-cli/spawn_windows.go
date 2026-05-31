//go:build windows

package main

import "os/exec"

func setDetachedProcess(_ *exec.Cmd) {
	// On Windows, child processes are already independent.
	// TODO(windows): Consider CREATE_NEW_PROCESS_GROUP and DETACHED_PROCESS
	// flags for more robust process isolation.
}
