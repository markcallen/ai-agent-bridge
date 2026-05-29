//go:build windows

package main

import "os"

func setupSigwinch(_ chan os.Signal) {
	// Windows does not have SIGWINCH.
}

func isSigwinch(_ os.Signal) bool {
	return false
}
