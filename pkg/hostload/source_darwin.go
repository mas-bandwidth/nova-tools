package hostload

import (
	"context"
	"fmt"
	"runtime"
	"syscall"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/subproc"
	"golang.org/x/sys/unix"
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
		// The busy percent of the next second: top's second of two samples a second
		// apart, the first the average since boot and the second the interval. top's
		// "CPU usage:" line is the same on arm64 and amd64, where iostat's columns
		// differed and its second reading gave no sample on amd64, so every beat
		// carried no load (docs/SPEC-SPRINT.md, the fleet, the beat).
		CPUSecond: func() (float64, error) {
			ctx, cancel := context.WithTimeout(context.Background(), topTimeout)
			defer cancel()
			out, err := subproc.Context(ctx, "/usr/bin/top", "-l", "2", "-n", "0", "-s", "1").Output()
			if err != nil {
				return 0, err
			}
			if p, ok := ParseTopCPUInterval(string(out)); ok {
				return p, nil
			}
			return 0, fmt.Errorf("top gave no CPU usage line")
		},
		// The one-minute load average, the binary sysctl read raw: syscall.Sysctl
		// cuts a trailing NUL and ParseVMLoadavg reads the whole struct.
		Load1: func() (float64, bool) {
			b, err := unix.SysctlRaw("vm.loadavg")
			if err != nil {
				return 0, false
			}
			return ParseVMLoadavg(b)
		},
		// The system's open files: two sysctl calls, in Go.
		OpenFiles: func() (int, int, error) {
			open, err := syscall.SysctlUint32("kern.num_files")
			if err != nil {
				return 0, 0, err
			}
			limit, err := syscall.SysctlUint32("kern.maxfiles")
			if err != nil {
				limit = 0
			}
			return int(open), int(limit), nil
		},
		// Every process this user can see, by lsof's field output: no name or port
		// lookups, the pid, command, user and descriptor of each open file. lsof exits 1
		// when it could not report some file, and its output stands; a run past the
		// bound is said.
		Holders: func() ([]Holder, error) {
			ctx, cancel := context.WithTimeout(context.Background(), HoldersTimeout)
			defer cancel()
			out, err := subproc.Context(ctx, "/usr/sbin/lsof", "-n", "-P", "-F", "pcLf").Output()
			if ctx.Err() != nil {
				return nil, HoldersTimedOut("lsof")
			}
			hs := ParseLsof(string(out))
			if err != nil && len(hs) == 0 {
				return nil, fmt.Errorf("lsof: %w", err)
			}
			return hs, nil
		},
	}
}
