//go:build !windows

package localserver

import (
	"os"
	"syscall"
)

// acquireLock places an exclusive non-blocking flock on the file.
func acquireLock(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
}
