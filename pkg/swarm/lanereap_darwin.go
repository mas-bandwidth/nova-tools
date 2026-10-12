//go:build darwin

package swarm

import (
	"bytes"
	"encoding/binary"
	"os"

	"golang.org/x/sys/unix"
)

// darwinZombie is SZOMB, kinfo_proc's p_stat of a process that has ended and is not yet
// reaped (sys/proc.h).
const darwinZombie = 5

// laneProcs lists the live processes of a lane other than this one (lanereap.go): those in
// session sid (sid > 0, getsid), and those whose environment holds the entry LaneEnv=mark
// (mark != "", the environment half of sysctl kern.procargs2, one exact entry, never the
// command line before it). A zombie is not live. A process whose records this user may not
// read is not listed.
func laneProcs(sid int, mark string) ([]laneProcess, bool) {
	procs, err := unix.SysctlKinfoProcSlice("kern.proc.all")
	if err != nil {
		return nil, false
	}
	self := os.Getpid()
	var want []byte
	if mark != "" {
		want = []byte(LaneEnv + "=" + mark)
	}
	var out []laneProcess
	for i := range procs {
		pid := int(procs[i].Proc.P_pid)
		if pid <= 1 || pid == self || procs[i].Proc.P_stat == darwinZombie {
			continue
		}
		started := LaneStartStamp(pid)
		if sid > 0 {
			if s, err := unix.Getsid(pid); err == nil && s == sid {
				out = append(out, laneProcess{pid: pid, started: started})
				continue
			}
		}
		if want != nil && procargsEnvHolds(pid, want) {
			out = append(out, laneProcess{pid: pid, started: started})
		}
	}
	return out, true
}

// procargsEnvHolds is whether a process's environment holds the exact entry want, read from
// kern.procargs2: argc, the exec path, its NUL padding, argc arguments, then the environment,
// each NUL-terminated. The arguments are skipped, never compared.
func procargsEnvHolds(pid int, want []byte) bool {
	buf, err := unix.SysctlRaw("kern.procargs2", pid)
	if err != nil || len(buf) < 4 {
		return false
	}
	argc := int(binary.NativeEndian.Uint32(buf[:4]))
	rest := buf[4:]
	i := bytes.IndexByte(rest, 0)
	if i < 0 {
		return false
	}
	rest = rest[i:]
	for len(rest) > 0 && rest[0] == 0 {
		rest = rest[1:]
	}
	for n := 0; n < argc; n++ {
		i := bytes.IndexByte(rest, 0)
		if i < 0 {
			return false
		}
		rest = rest[i+1:]
	}
	for len(rest) > 0 {
		ent := rest
		if i := bytes.IndexByte(rest, 0); i >= 0 {
			ent, rest = rest[:i], rest[i+1:]
		} else {
			rest = nil
		}
		if len(ent) == 0 {
			break
		}
		if bytes.Equal(ent, want) {
			return true
		}
	}
	return false
}
