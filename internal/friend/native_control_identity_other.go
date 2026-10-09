//go:build !linux && !darwin

package friend

import (
	"errors"
	"os"
)

type nativeWorkID struct { UID uint32; Device, Inode uint64 }

func nativeWorkIdentity(os.FileInfo) (nativeWorkID, error) {
	return nativeWorkID{}, errors.New("native control work identity is unsupported on this platform")
}
func nativeControlOwnedPath(string, bool) error {
	return errors.New("native control path ownership is unsupported on this platform")
}

func nativeControlNoSymlinkFile(string) error { return errors.New("native control binding ownership is unsupported on this platform") }
