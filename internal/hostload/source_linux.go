package hostload

import (
	"os"
	"runtime"
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
	}
}
