package hostload

import (
	"context"
	"errors"
	"runtime"
	"syscall"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/subproc"
)

// topTimeout bounds one run of top.
const topTimeout = 5 * time.Second

func localSource() Source {
	return Source{
		NCPU: runtime.NumCPU(),
		// One sample, no processes, no delay: the header's CPU usage line.
		Top: func() (string, error) {
			ctx, cancel := context.WithTimeout(context.Background(), topTimeout)
			defer cancel()
			out, err := subproc.Context(ctx, "/usr/bin/top", "-l", "1", "-n", "0", "-s", "0").Output()
			return string(out), err
		},
		// The busy percent of the next second: iostat's own two readings a second
		// apart (about 4 ms of CPU), where top costs about 300.
		CPUSecond: func() (float64, error) {
			ctx, cancel := context.WithTimeout(context.Background(), topTimeout)
			defer cancel()
			out, err := subproc.Context(ctx, "/usr/sbin/iostat", "-c", "2", "-w", "1", "-n", "0").Output()
			if err != nil {
				return 0, err
			}
			if p, ok := ParseIostat(string(out)); ok {
				return p, nil
			}
			return 0, errors.New("iostat gave no second reading")
		},
		Load1: func() (float64, bool) {
			s, err := syscall.Sysctl("vm.loadavg")
			if err != nil {
				return 0, false
			}
			return ParseVMLoadavg([]byte(s))
		},
	}
}
