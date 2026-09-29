//go:build !unix

package privacy

import "io/fs"

// fileKey is a file's identity: its absolute path with every symlink
// resolved, where the platform gives no device and inode.
func fileKey(path string, _ fs.FileInfo) string { return resolvedPath(path) }
