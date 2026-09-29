package release

import (
	"errors"
	"os"
	"syscall"
)

// writeNoFollow creates or replaces the regular file at path.
//
// os.WriteFile is OpenFile with O_WRONLY|O_CREATE|O_TRUNC and no O_NOFOLLOW,
// so it follows a symlink and replaces the file the link names. Lstat and
// then os.WriteFile is not the repair: the open still follows a link that
// appears after the check, and on macOS Lstat of a trailing slash reports
// the directory a symlink points at, so the check never sees the link.
//
// The open itself carries O_NOFOLLOW. A symlink is refused and nothing is
// written. A path that is not there yet is created.
func writeNoFollow(op, path string, data []byte, perm os.FileMode) error {
	if err := refuseSymlinkDestination(op, path); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC|oNoFollow, perm)
	if err != nil {
		if errors.Is(err, syscall.ELOOP) {
			if serr := refuseSymlinkDestination(op, path); serr != nil {
				return serr
			}
		}
		return err
	}
	_, err = f.Write(data)
	if err1 := f.Close(); err1 != nil && err == nil {
		err = err1
	}
	return err
}

// refuseSymlinkDestination reports a final component that is a symlink.
// A trailing separator is stripped only for this look: on macOS, Lstat of
// the path with one reports the target directory and hides the link. A backslash
// is a literal filename byte on Unix, so only the host's separators are stripped.
func refuseSymlinkDestination(op, path string) error {
	fi, err := os.Lstat(pathWithoutTrailingSep(path))
	if err != nil || fi.Mode()&os.ModeSymlink == 0 {
		return nil
	}
	return refuse("pass the real file, or remove the link, and retry",
		"%s: destination is a symlink: %s", op, path)
}

func pathWithoutTrailingSep(path string) string {
	end := len(path)
	for end > 0 && os.IsPathSeparator(path[end-1]) {
		end--
	}
	if end == 0 {
		return path
	}
	return path[:end]
}
