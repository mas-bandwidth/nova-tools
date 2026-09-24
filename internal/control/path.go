package control

import (
	"fmt"
	"os"
)

// ensurePlainDir creates path if missing and refuses a planted symlink at the
// final component. Ancestors may be volume aliases (for example /var on macOS);
// the control and acks names themselves must not be links.
func ensurePlainDir(path string) error {
	info, err := os.Lstat(path)
	switch {
	case err == nil:
		return refuseSymlinkDir(path, info)
	case os.IsNotExist(err):
		if err := os.MkdirAll(path, 0o755); err != nil {
			return err
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		return refuseSymlinkDir(path, info)
	default:
		return err
	}
}

func refuseSymlinkDir(path string, info os.FileInfo) error {
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%w: %s", ErrUnsafePath, path)
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", path)
	}
	return nil
}
