//go:build windows

package localserver

import "os"

// acquireLock is a no-op on Windows where unix sockets are not used.
// TODO(windows): Secure mode (mTLS+JWT over TCP) will need LockFileEx
// to prevent concurrent server starts. Local mode uses TCP 127.0.0.1:0
// which is inherently exclusive, but secure mode binds a fixed port.
func acquireLock(_ *os.File) error {
	return nil
}
