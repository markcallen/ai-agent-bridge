//go:build !windows

package main

import (
	"os"
	"os/signal"
	"syscall"
)

func setupSigwinch(ch chan os.Signal) {
	signal.Notify(ch, syscall.SIGWINCH)
}

func isSigwinch(sig os.Signal) bool {
	return sig == syscall.SIGWINCH
}
