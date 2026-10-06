//go:build linux || darwin

package sprint

import (
	"os"
	"path/filepath"
	"syscall"
)

// StatVolume is the figures of the volume dir lives on: its mount point (the highest
// directory above dir on the same device), its size and the bytes free to an unprivileged
// writer, and its inodes and free inodes.
func StatVolume(dir string) (DiskStat, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return DiskStat{}, err
	}
	var fs syscall.Statfs_t
	if err := syscall.Statfs(abs, &fs); err != nil {
		return DiskStat{}, err
	}
	bsize := uint64(fs.Bsize) // int64 on linux, uint32 on darwin
	return DiskStat{Volume: mountOf(abs), Size: uint64(fs.Blocks) * bsize, Free: uint64(fs.Bavail) * bsize,
		Inodes: uint64(fs.Files), InodesFree: uint64(fs.Ffree)}, nil
}

// mountOf is the highest directory at or above dir on dir's device.
func mountOf(dir string) string {
	dev := func(p string) (uint64, bool) {
		info, err := os.Stat(p)
		if err != nil {
			return 0, false
		}
		st, ok := info.Sys().(*syscall.Stat_t)
		if !ok {
			return 0, false
		}
		return uint64(st.Dev), true // int32 on darwin, uint64 on linux
	}
	want, ok := dev(dir)
	if !ok {
		return dir
	}
	for {
		up := filepath.Dir(dir)
		if up == dir {
			return dir
		}
		if d, ok := dev(up); !ok || d != want {
			return dir
		}
		dir = up
	}
}
