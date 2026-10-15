//go:build !aix && !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !solaris

package codexdesktop

import "errors"

func validateSocketPath(string) error {
	return errors.New("Unix socket delivery is unsupported on this platform")
}

// ValidatePrivateDirectory refuses unsupported ownership checks.
func ValidatePrivateDirectory(directory string) error {
	return errors.New("private event directory ownership is unsupported on this platform")
}
