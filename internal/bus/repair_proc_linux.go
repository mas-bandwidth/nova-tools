//go:build linux

package bus

import (
	"os"
	"strings"
)

// gitProcesses lists live git processes from /proc: command line and cwd, which together
// are how a lock decides whether a git still owns this checkout. A process that exits
// between readdir and the read is skipped; it is not a git that still owns anything.
func gitProcesses() ([]gitProc, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	var procs []gitProc
	for _, e := range entries {
		pid := e.Name()
		if !allDigits(pid) {
			continue
		}
		comm, err := os.ReadFile("/proc/" + pid + "/comm")
		if err != nil {
			continue
		}
		name := strings.TrimSpace(string(comm))
		if name != "git" && name != "git.exe" {
			continue
		}
		raw, err := os.ReadFile("/proc/" + pid + "/cmdline")
		if err != nil {
			continue
		}
		args := splitNUL(raw)
		if len(args) == 0 {
			continue
		}
		cwd, _ := os.Readlink("/proc/" + pid + "/cwd")
		procs = append(procs, gitProc{command: strings.Join(args, " "), args: args, cwd: cwd})
	}
	return procs, nil
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

func splitNUL(b []byte) []string {
	parts := strings.Split(string(b), "\x00")
	var out []string
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
