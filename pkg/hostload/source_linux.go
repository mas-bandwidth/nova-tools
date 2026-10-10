package hostload

import (
	"errors"
	"os"
	"runtime"
	"time"
)

func localSource() Source {
	return Source{
		NCPU: runtime.NumCPU(),
		ProcStat: func() (string, error) {
			b, err := os.ReadFile("/proc/stat")
			return string(b), err
		},
		Load1: func() (float64, bool) {
			b, err := os.ReadFile("/proc/loadavg")
			if err != nil {
				return 0, false
			}
			return ParseProcLoadavg(string(b))
		},
		OpenFiles: func() (int, int, error) {
			b, err := os.ReadFile("/proc/sys/fs/file-nr")
			if err != nil {
				return 0, 0, err
			}
			open, limit, ok := ParseFileNr(string(b))
			if !ok {
				return 0, 0, errors.New("/proc/sys/fs/file-nr: not three counts")
			}
			return open, limit, nil
		},
		Holders: func() ([]Holder, error) { return ProcHolders("/proc", time.Now().Add(HoldersTimeout)) },
	}
}
