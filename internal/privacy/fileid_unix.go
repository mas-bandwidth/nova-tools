//go:build unix

package privacy

import (
	"fmt"
	"io/fs"
	"syscall"
)

// fileKey is a file's identity: its device and inode, so a document reached
// by two paths, a symlink or a hard link is one document.
func fileKey(path string, info fs.FileInfo) string {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return fmt.Sprintf("%d:%d", st.Dev, st.Ino)
	}
	return resolvedPath(path)
}
