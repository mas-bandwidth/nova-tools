//go:build linux || darwin

package friend

import (
	"errors"
	"os"
	"syscall"
)

type nativeWorkID struct {
	UID           uint32
	Device, Inode uint64
}

func nativeWorkIdentity(info os.FileInfo) (nativeWorkID, error) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.IsDir() || st.Ino == 0 || uint64(st.Dev) == 0 || st.Uid != uint32(os.Geteuid()) {
		return nativeWorkID{}, errors.New("native work/control path must be a directory owned by the effective OS UID; shared cross-UID work is unsupported")
	}
	return nativeWorkID{UID: st.Uid, Device: uint64(st.Dev), Inode: st.Ino}, nil
}

func nativeControlOwnedPath(path string, private bool) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if _, err := nativeWorkIdentity(info); err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o022 != 0 || (private && info.Mode().Perm()&0o077 != 0) {
		return errors.New("native control ownership path is writable by another account or is not private")
	}
	return nil
}

func nativeControlNoSymlinkFile(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.Mode().IsRegular() || st.Uid != uint32(os.Geteuid()) {
		return errors.New("native control binding/lock must be a real file owned by the effective UID")
	}
	return nil
}
