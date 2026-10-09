//go:build linux || darwin

package friend

import (
	"errors"
	"io"
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

// Legacy claims are read once through a bounded no-follow regular descriptor.
// A changed pathname or growing file holds; this is not an atomic launch grant.
func nativeControlReadLegacyLane(path string) ([]byte, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > 4096 {
		return nil, errors.New("legacy lane is not a bounded regular file")
	}
	raw, err := io.ReadAll(io.LimitReader(f, 4097))
	if err != nil || len(raw) > 4096 {
		return nil, errors.New("legacy lane read exceeds its bound")
	}
	after, err := f.Stat()
	if err != nil || after.Size() != info.Size() || int64(len(raw)) != info.Size() || !after.ModTime().Equal(info.ModTime()) {
		return nil, errors.New("legacy lane changed during its read")
	}
	current, err := os.Lstat(path)
	if err != nil || !os.SameFile(info, current) {
		return nil, errors.New("legacy lane pathname changed during its read")
	}
	return raw, nil
}
