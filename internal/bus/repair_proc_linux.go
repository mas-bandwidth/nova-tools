//go:build linux

package bus

import (
	"fmt"
	"os"
	"strings"
	"syscall"
)

// gitProcesses lists live git processes from /proc. A pid that disappears between
// readdir and the read is skipped: it is not an owner. A zombie git has an empty
// cmdline and is skipped the same way. A pid that is still live and whose metadata
// cannot be read does not stop the scan; the lock stays only when the finished
// scan has no known owner of this checkout. A pid whose /proc entry another account owns
// is skipped before its cwd is read (see ownershipUnknown in repair.go).
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
		views = append(views, readProcView(pid, self))
	}
	return classifyViews(views, self)
}

// readProcView reads one pid. The owner of /proc/<pid> is the process's effective uid; a
// process of another account is returned with only its owner, and nothing else of it is
// read: its cwd would answer EACCES, which is not an unreadable owner of this checkout.
// A stat that fails because the pid is gone is the vanished case; any other stat failure
// leaves the owner unknown and the process is read and classified as before.
func readProcView(pid string, self uint32) procView {
	var v procView
	if fi, err := os.Stat("/proc/" + pid); err != nil {
		if procGone(err) {
			v.commErr = err
			return v
		}
	} else if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		v.owner, v.ownerKnown = st.Uid, true
		if st.Uid != self {
			return v
		}
	}
	comm, err := os.ReadFile("/proc/" + pid + "/comm")
	if err != nil {
		v.commErr = err
		return v
	}
	v.comm = string(comm)
	name := strings.TrimSpace(v.comm)
	if name != "git" && name != "git.exe" {
		return v
	}
	raw, err := os.ReadFile("/proc/" + pid + "/cmdline")
	if err != nil {
		v.cmdErr = err
		return v
	}
	v.cmdline = raw
	// An empty cmdline is how a zombie git looks. State Z or X is dead, and a
	// pid that vanished while we were reading it is dead too. A live process
	// with an empty cmdline is not: that stays unclassified unless its cwd places it.
	if len(splitNUL(raw)) == 0 {
		dead, derr := linuxProcDead(pid)
		if procGone(derr) {
			v.dead = true
		} else if derr == nil {
			v.dead = dead
		}
	}
	cwd, err := os.Readlink("/proc/" + pid + "/cwd")
	if err != nil {
		v.cwdErr = err
		return v
	}
	v.cwd = cwd
	return v
}

// linuxProcDead reports whether pid is a zombie or already dead. ENOENT or ESRCH
// (procGone) means the pid vanished, which the caller treats as dead. Any other error means the
// state could not be read; the caller must not invent "dead" from that.
func linuxProcDead(pid string) (bool, error) {
	raw, err := os.ReadFile("/proc/" + pid + "/stat")
	if err != nil {
		return false, err
	}
	dead, ok := procStatDead(string(raw))
	if !ok {
		return false, fmt.Errorf("stat")
	}
	return dead, nil
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
