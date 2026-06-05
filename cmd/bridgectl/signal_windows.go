//go:build windows

package main

import "os"

func setupSigwinch(_ chan os.Signal) {
	// Windows does not have SIGWINCH.
	// TODO(windows): Investigate ConPTY resize events for terminal resize support.
}

func isSigwinch(_ os.Signal) bool {
	return false
}
