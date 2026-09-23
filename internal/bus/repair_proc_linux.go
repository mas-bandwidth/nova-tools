//go:build linux

package bus

import (
	"fmt"
	"os"
	"strings"
)

// gitProcesses lists live git processes from /proc. A pid that disappears between
// readdir and the read is skipped: it is not an owner. A zombie git has an empty
// cmdline and is skipped the same way. A pid that is still live and whose metadata
// cannot be read does not stop the scan; the lock stays only when the finished
// scan has no known owner of this checkout.
func gitProcesses() ([]gitProc, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, ownershipUnknownErr("proc unreadable")
	}
	var views []procView
	for _, e := range entries {
		pid := e.Name()
		if !allDigits(pid) {
			continue
		}
		views = append(views, readProcView(pid))
	}
	return classifyViews(views)
}

func readProcView(pid string) procView {
	var v procView
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
