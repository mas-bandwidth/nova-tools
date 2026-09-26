package yield

import (
	"fmt"
	"os"
	"strconv"
	"syscall"
)

// setNice is setpriority(PRIO_PROCESS, tid, n) on EVERY thread of this
// process. On Linux a nice value belongs to a thread, not a process: the
// 0 in setpriority(PRIO_PROCESS, 0, n) is the calling thread alone, and a
// child the Go runtime forks from another of its threads inherits that
// thread's nice, which is still 0 (measured on hetzner, 2026-09-26: 31 of
// 32 children of a wrapper at nice 0). So the threads are read from
// /proc/self/task and each is set, and the read repeats until a pass finds
// no thread it has not set: a thread the runtime starts meanwhile is
// forked from a thread already at n and inherits it, but the loop does not
// take that on trust.
func setNice(n int) error {
	done := map[int]bool{}
	for pass := 0; pass < 16; pass++ {
		tids, err := threads()
		if err != nil {
			return err
		}
		fresh := 0
		for _, tid := range tids {
			if done[tid] {
				continue
			}
			if err := syscall.Setpriority(syscall.PRIO_PROCESS, tid, n); err != nil {
				if err == syscall.ESRCH {
					continue // the thread ended between the read and the set
				}
				return fmt.Errorf("thread %d: %w", tid, err)
			}
			done[tid] = true
			fresh++
		}
		if fresh == 0 {
			return nil
		}
	}
	return fmt.Errorf("threads kept appearing over 16 passes")
}

// threads lists this process's thread ids from /proc/self/task.
func threads() ([]int, error) {
	names, err := os.ReadDir("/proc/self/task")
	if err != nil {
		return nil, fmt.Errorf("/proc/self/task: %w", err)
	}
	tids := make([]int, 0, len(names))
	for _, e := range names {
		if tid, err := strconv.Atoi(e.Name()); err == nil {
			tids = append(tids, tid)
		}
	}
	return tids, nil
}

// currentNice is getpriority(PRIO_PROCESS, 0) for the calling thread. The
// raw Linux system call answers 20 minus the nice value (so it never
// returns a negative), and Go's syscall.Getpriority hands that back
// untouched; this undoes it.
func currentNice() (int, error) {
	raw, err := syscall.Getpriority(syscall.PRIO_PROCESS, 0)
	if err != nil {
		return 0, err
	}
	return 20 - raw, nil
}
