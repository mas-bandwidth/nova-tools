//go:build windows

package atomicfile

import (
	"io/fs"
	"os"
)

// canWrite is a directory check on Windows only as far as attributes go: its
// access lists are answered by trying, which a plan does not do.
func canWrite(string) error { return nil }

// canWriteFile refuses a file with the read-only attribute.
func canWriteFile(path string, fi os.FileInfo) error {
	if fi.Mode().Perm()&0o200 == 0 {
		return &fs.PathError{Op: "open", Path: path, Err: fs.ErrPermission}
	}
	return nil
}
