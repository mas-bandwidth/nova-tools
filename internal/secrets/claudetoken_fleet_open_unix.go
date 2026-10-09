//go:build darwin || linux

package secrets

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// readClaudeFleetRegularNoFollow walks from an open root directory. Each
// component is opened relative to the preceding directory with O_NOFOLLOW,
// so replacing a path component during the walk cannot redirect the read.
func readClaudeFleetRegularNoFollow(abs string, max int64) ([]byte, error) {
	if !filepath.IsAbs(abs) || abs == string(filepath.Separator) {
		return nil, fmt.Errorf("manifest path is not an absolute file path")
	}
	dirFD, err := unix.Open(string(filepath.Separator), unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	defer func() {
		if dirFD >= 0 {
			// ignored: closing a read-only directory after the walk cannot alter the verified bytes.
			_ = unix.Close(dirFD)
		}
	}()
	parts := strings.Split(strings.TrimPrefix(abs, string(filepath.Separator)), string(filepath.Separator))
	for i, part := range parts {
		if part == "" || part == "." || part == ".." {
			return nil, fmt.Errorf("manifest path has an invalid component")
		}
		if i < len(parts)-1 {
			nextFD, openErr := unix.Openat(dirFD, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
			if openErr != nil {
				return nil, openErr
			}
			// ignored: a close failure on the previous read-only directory cannot redirect nextFD.
			_ = unix.Close(dirFD)
			dirFD = nextFD
			continue
		}
		fileFD, openErr := unix.Openat(dirFD, part, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC|unix.O_NOCTTY, 0)
		if openErr != nil {
			return nil, openErr
		}
		file := os.NewFile(uintptr(fileFD), "claude-token-manifest")
		defer file.Close()
		info, statErr := file.Stat()
		if statErr != nil || !info.Mode().IsRegular() {
			return nil, fmt.Errorf("manifest is not a regular file")
		}
		raw, readErr := io.ReadAll(io.LimitReader(file, max+1))
		if readErr != nil || int64(len(raw)) > max {
			return nil, fmt.Errorf("manifest exceeds read limit")
		}
		return raw, nil
	}
	return nil, fmt.Errorf("manifest path has no file component")
}
