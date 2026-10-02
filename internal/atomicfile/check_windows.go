//go:build windows

package atomicfile

import (
	"io/fs"
	"os"
)

// canWrite checks nothing on Windows: a directory's access list is answered by
// trying, which a plan does not do, so a Windows plan's success is not proof
// that the write is permitted. Everything else Check makes holds there too.
func canWrite(string) error { return nil }

// canWriteFile refuses a file with the read-only attribute.
func canWriteFile(path string, fi os.FileInfo) error {
	if fi.Mode().Perm()&0o200 == 0 {
		return &fs.PathError{Op: "open", Path: path, Err: fs.ErrPermission}
	}
	return nil
}
