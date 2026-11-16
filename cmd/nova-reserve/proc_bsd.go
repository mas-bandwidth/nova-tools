//go:build darwin || freebsd || netbsd || openbsd || dragonfly

package main

import (
	"os"
	"path/filepath"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

func sysAttr() *syscall.SysProcAttr { return &syscall.SysProcAttr{Setpgid: true} }

func usableCPUs() ([]int, error) { return nil, nil }

func smtGroups([]int) [][]int { return nil }

func pin([]int) error { return nil }

func cpuBusyPct(time.Duration) (int, bool) { return 0, false }

type kqueueWatcher struct {
	kq  int
	fds []*os.File
}

func newOSWatcher(dir string) watcher {
	kq, err := unix.Kqueue()
	if err != nil {
		return fallbackWatcher{}
	}
	unix.CloseOnExec(kq)
	var fds []*os.File
	var changes []unix.Kevent_t
	for _, p := range []string{dir, filepath.Join(dir, "holds"), filepath.Join(dir, "line")} {
		f, err := os.OpenFile(p, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
		if err != nil {
			continue
		}
		fds = append(fds, f)
		changes = append(changes, unix.Kevent_t{
			Ident:  uint64(f.Fd()),
			Filter: unix.EVFILT_VNODE,
			Flags:  unix.EV_ADD | unix.EV_CLEAR,
			Fflags: unix.NOTE_WRITE | unix.NOTE_ATTRIB | unix.NOTE_EXTEND | unix.NOTE_RENAME | unix.NOTE_DELETE,
		})
	}
	if len(changes) > 0 {
		// ignored: fallback timeout bounds wait if registration fails
		_, _ = unix.Kevent(kq, changes, nil, nil)
	}
	return &kqueueWatcher{kq: kq, fds: fds}
}

func (w *kqueueWatcher) drain() {
	var evs [16]unix.Kevent_t
	var zero unix.Timespec
	for {
		n, err := unix.Kevent(w.kq, nil, evs[:], &zero)
		if err != nil || n == 0 {
			return
		}
	}
}

func (w *kqueueWatcher) wait(pids []int, d time.Duration) {
	if d <= 0 {
		return
	}
	var changes []unix.Kevent_t
	for _, pid := range pids {
		if pid > 0 {
			changes = append(changes, unix.Kevent_t{
				Ident:  uint64(pid),
				Filter: unix.EVFILT_PROC,
				Flags:  unix.EV_ADD | unix.EV_ONESHOT | unix.EV_RECEIPT,
				Fflags: unix.NOTE_EXIT,
			})
		}
	}
	if len(changes) > 0 {
		receipts := make([]unix.Kevent_t, len(changes))
		if n, err := unix.Kevent(w.kq, changes, receipts, nil); err == nil {
			for _, r := range receipts[:n] {
				if r.Flags&unix.EV_ERROR != 0 && syscall.Errno(r.Data) == syscall.ESRCH {
					return // a blocking holder already exited
				}
			}
		}
	}
	ts := unix.NsecToTimespec(min(d, 2*time.Second).Nanoseconds())
	var evs [8]unix.Kevent_t
	// ignored: EINTR or timeout returns to caller which re-checks deadline and state
	_, _ = unix.Kevent(w.kq, nil, evs[:], &ts)
}

func (w *kqueueWatcher) close() {
	for _, f := range w.fds {
		// ignored: opened read-only for EVFILT_VNODE
		_ = f.Close()
	}
	// ignored: closing kqueue descriptor at end of acquire
	_ = unix.Close(w.kq)
}
