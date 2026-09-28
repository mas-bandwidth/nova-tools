//go:build darwin || linux

// The handoff's descriptor layer on unix: every open is an openat from a
// descriptor already checked, with O_NOFOLLOW so no component is a link and
// O_NONBLOCK so a FIFO cannot hold the open, and every descriptor is fstat'd for
// its type and its device before anything reads it. The two system calls
// are hooks on the input, so a test drives the swap a surviving process would
// make between the check and the open, and the device a different filesystem
// would report, without a package-level seam.
package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// handoffHooks are the two system calls a checked open is made with. A test
// sets them on its own handoffInput to make the swap a surviving process would
// make between the check and the open, or the device another filesystem would
// report; a nil hook is the real call.
type handoffHooks struct {
	openat func(dirfd int, name string, flags int) (int, error)
	fstat  func(fd int, st *unix.Stat_t) error
}

func (h *handoffHooks) openatCall(dirfd int, name string, flags int) (int, error) {
	if h != nil && h.openat != nil {
		return h.openat(dirfd, name, flags)
	}
	return unix.Openat(dirfd, name, flags, 0)
}

func (h *handoffHooks) fstatCall(fd int, st *unix.Stat_t) error {
	if h != nil && h.fstat != nil {
		return h.fstat(fd, st)
	}
	return unix.Fstat(fd, st)
}

// handoffOpenFlags opens anything read-only, follows no link, waits on nothing,
// and does not leak into a child.
const handoffOpenFlags = unix.O_RDONLY | unix.O_NOFOLLOW | unix.O_NONBLOCK | unix.O_CLOEXEC

// handoffOpenRoot opens the card's working directory by descriptor and returns
// it with the volume's device. The device is the mount point's own, read with
// lstat; the mount is opened, then each component from it to work is opened
// with a checked openat, so a work/ the card swapped for a link is refused.
func handoffOpenRoot(mount, work string, hooks *handoffHooks) (*os.File, volumeReader, error) {
	var st unix.Stat_t
	if err := unix.Lstat(mount, &st); err != nil {
		return nil, volumeReader{}, &fs.PathError{Op: "lstat", Path: mount, Err: err}
	}
	if st.Mode&unix.S_IFMT != unix.S_IFDIR {
		return nil, volumeReader{}, fmt.Errorf("the volume's mount %s is not a directory", mount)
	}
	vr := volumeReader{dev: uint64(st.Dev), hooks: hooks}
	rel, err := filepath.Rel(mount, work)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return nil, volumeReader{}, fmt.Errorf("the working directory %s is not on the volume mounted at %s", work, mount)
	}
	fd, err := unix.Open(mount, handoffOpenFlags|unix.O_DIRECTORY, 0)
	if err != nil {
		return nil, volumeReader{}, &fs.PathError{Op: "open", Path: mount, Err: err}
	}
	dir, err := checkedEntry(fd, mount, vr)
	if err != nil {
		return nil, volumeReader{}, err
	}
	if dir.kind != entryDir {
		dir.f.Close()
		return nil, volumeReader{}, fmt.Errorf("the volume's mount %s is not a directory", mount)
	}
	if rel == "." {
		return dir.f, vr, nil
	}
	e, err := openComps(dir.f, vr, strings.Split(filepath.ToSlash(rel), "/"))
	dir.f.Close()
	if err != nil {
		return nil, volumeReader{}, err
	}
	if e.kind != entryDir {
		e.f.Close()
		return nil, volumeReader{}, fmt.Errorf("the working directory %s is not a directory", work)
	}
	return e.f, vr, nil
}

// handoffOpenAt opens one name in a checked directory and checks what it got.
func handoffOpenAt(dir *os.File, name string, vr volumeReader) (handoffEntry, error) {
	if name == "" || name == "." || name == ".." || strings.ContainsRune(name, '/') {
		return handoffEntry{}, fmt.Errorf("%q is not one path component", name)
	}
	fd, err := vr.hooks.openatCall(int(dir.Fd()), name, handoffOpenFlags)
	if err != nil {
		if err == unix.ELOOP || err == unix.EMLINK {
			return handoffEntry{}, fmt.Errorf("%s is a symlink; a handoff follows no link", name)
		}
		return handoffEntry{}, &fs.PathError{Op: "open", Path: name, Err: err}
	}
	return checkedEntry(fd, name, vr)
}

// checkedEntry fstats a fresh descriptor and keeps it only when it is a
// regular file or a directory on the volume's device. A regular file has
// O_NONBLOCK cleared before it is read.
func checkedEntry(fd int, name string, vr volumeReader) (handoffEntry, error) {
	var st unix.Stat_t
	if err := vr.hooks.fstatCall(fd, &st); err != nil {
		unix.Close(fd)
		return handoffEntry{}, err
	}
	if uint64(st.Dev) != vr.dev {
		unix.Close(fd)
		return handoffEntry{}, fmt.Errorf("%s is not on the volume (device %d, the volume is %d)", name, uint64(st.Dev), vr.dev)
	}
	kind := entryOther
	switch st.Mode & unix.S_IFMT {
	case unix.S_IFREG:
		kind = entryFile
	case unix.S_IFDIR:
		kind = entryDir
	}
	if kind == entryOther {
		unix.Close(fd)
		return handoffEntry{}, fmt.Errorf("%s is neither a regular file nor a directory, and a handoff copies bytes", name)
	}
	if kind == entryFile {
		if err := unix.SetNonblock(fd, false); err != nil {
			unix.Close(fd)
			return handoffEntry{}, err
		}
	}
	return handoffEntry{
		f:    os.NewFile(uintptr(fd), name),
		kind: kind,
		size: st.Size,
		mode: fs.FileMode(st.Mode & 0o777),
	}, nil
}
