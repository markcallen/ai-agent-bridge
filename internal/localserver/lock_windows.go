//go:build windows

package localserver

import "os"

// acquireLock is a no-op on Windows where unix sockets are not used.
func acquireLock(_ *os.File) error {
	return nil
}
