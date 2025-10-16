//go:build linux || darwin || freebsd || netbsd || openbsd

package state

import (
	"os"
	"syscall"
)

// getInode extracts the inode number from file info on Unix-like systems.
func getInode(info os.FileInfo) (uint64, bool) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return stat.Ino, true
}
