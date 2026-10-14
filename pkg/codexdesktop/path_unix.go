//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package codexdesktop

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

type lstatFunc func(string) (os.FileInfo, error)

// validateSocketPath applies the ownership and permission rule in
// SPEC-CODEX-DESKTOP-WAKE without following either final path component.
func validateSocketPath(socketPath string) error {
	return validateSocketPathWith(socketPath, uint32(os.Getuid()), os.Lstat)
}

func validateSocketPathWith(socketPath string, uid uint32, lstat lstatFunc) error {
	parent := filepath.Dir(socketPath)
	for _, check := range []struct {
		name string
		path string
	}{
		{name: "socket parent", path: parent},
		{name: "socket", path: socketPath},
	} {
		info, err := lstat(check.path)
		if err != nil {
			return fmt.Errorf("inspect %s: %w", check.name, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%s is a symlink", check.name)
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok {
			return fmt.Errorf("%s ownership is unavailable", check.name)
		}
		if stat.Uid != uid {
			return fmt.Errorf("%s is not owned by the current user", check.name)
		}
		if info.Mode().Perm()&0o022 != 0 {
			return fmt.Errorf("%s is group or world writable", check.name)
		}
		if check.path == socketPath && info.Mode()&os.ModeSocket == 0 {
			return fmt.Errorf("socket path is not a Unix socket")
		}
	}
	return nil
}

// ValidatePrivateDirectory requires a private recipient-owned spool directory.
func ValidatePrivateDirectory(directory string) error {
	info, err := os.Lstat(directory)
	if err != nil {
		return fmt.Errorf("inspect event directory: %w", err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0077 != 0 || !ok || stat.Uid != uint32(os.Getuid()) {
		return fmt.Errorf("event directory wants a private directory owned by the current user (mode 0700), without a symlink")
	}
	return nil
}
