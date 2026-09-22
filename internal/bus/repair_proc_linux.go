//go:build linux

package bus

import (
	"os"
	"strings"
)

// gitProcesses lists live git processes from /proc. A pid that disappears between
// readdir and the read is skipped: it is not an owner. A pid that is still there
// and whose comm, cmdline, or cwd cannot be read is an incomplete scan, not an
// empty process, and the lock stays.
func gitProcesses() ([]gitProc, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, ownershipUnknownErr("proc unreadable")
	}
	var procs []gitProc
	for _, e := range entries {
		pid := e.Name()
		if !allDigits(pid) {
			continue
		}
		p, skip, perr := gitProcFromView(readProcView(pid))
		if perr != nil {
			return nil, perr
		}
		if skip {
			continue
		}
		procs = append(procs, p)
	}
	return procs, nil
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
	cwd, err := os.Readlink("/proc/" + pid + "/cwd")
	if err != nil {
		v.cwdErr = err
		return v
	}
	v.cwd = cwd
	return v
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
