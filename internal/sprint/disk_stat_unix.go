//go:build !windows

package sprint

import "syscall"

func init() { VolumeStatOf = statVolume }

func statVolume(path string) (VolumeStat, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return VolumeStat{}, err
	}
	return VolumeStat{
		Bsize:  uint64(st.Bsize),
		Blocks: uint64(st.Blocks),
		Bavail: uint64(st.Bavail),
		Files:  uint64(st.Files),
		Ffree:  uint64(st.Ffree),
	}, nil
}
