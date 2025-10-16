//go:build !linux && !darwin && !freebsd && !netbsd && !openbsd

package state

import "os"

// getInode is a fallback for non-Unix systems where inode is not available.
func getInode(info os.FileInfo) (uint64, bool) {
	// Inode concept is not reliably available on all platforms (e.g., Windows).
	return 0, false
}
