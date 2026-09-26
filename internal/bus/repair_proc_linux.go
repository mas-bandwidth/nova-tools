//go:build linux

package bus

import (
	"os"
	"syscall"
)

// gitProcesses lists live git processes from /proc. A pid that disappears between
// readdir and the read is skipped: it is not an owner. A zombie git has an empty
// cmdline and is skipped the same way. A pid that is still live and whose metadata
// cannot be read does not stop the scan; the lock stays only when the finished
// scan has no known owner of this checkout. A git of another account that cannot be
// placed is skipped (see ownershipUnknown in repair.go).
func gitProcesses() ([]gitProc, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, ownershipUnknownErr("proc unreadable")
	}
	self := uint32(os.Geteuid())
	var views []procView
	for _, e := range entries {
		pid := e.Name()
		if !allDigits(pid) {
			continue
		}
		views = append(views, readProcView(pid, linuxProcReader))
	}
	return classifyViews(views, self)
}

// linuxProcReader is readProcView's /proc: the entry's owner from its stat, and the
// files and cwd link read as they are.
var linuxProcReader = procReader{owner: linuxProcOwner, readFile: os.ReadFile, readlink: os.Readlink}

// linuxProcOwner is the owner uid of /proc/<pid>, which is the process's effective uid.
// ok is false when the stat has no uid to give.
func linuxProcOwner(pid string) (uint32, bool, error) {
	fi, err := os.Stat("/proc/" + pid)
	if err != nil {
		return 0, false, err
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false, nil
	}
	return st.Uid, true, nil
}

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
