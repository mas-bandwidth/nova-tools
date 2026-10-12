//go:build linux

package swarm

import (
	"bytes"
	"os"
	"strconv"
	"strings"
)

// laneProcs lists the live processes of a lane other than this one (lanereap.go): those in
// session sid (sid > 0), and those whose environment holds the entry LaneEnv=mark (mark !=
// ""). It reads the kernel's own records: field 3 (state) and field 6 (session) of
// /proc/<pid>/stat, and /proc/<pid>/environ, one exact NUL-separated entry, never a command
// line. A zombie is not live. A process whose records this user may not read is not listed.
func laneProcs(sid int, mark string) ([]int, bool) {
	ents, err := os.ReadDir("/proc")
	if err != nil {
		return nil, false
	}
	self := os.Getpid()
	var want []byte
	if mark != "" {
		want = []byte(LaneEnv + "=" + mark)
	}
	var out []int
	for _, e := range ents {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid <= 1 || pid == self {
			continue
		}
		state, session, ok := procStateSession(pid)
		if !ok || state == "Z" || state == "X" {
			continue
		}
		if (sid > 0 && session == sid) || (want != nil && environHolds(pid, want)) {
			out = append(out, pid)
		}
	}
	return out, true
}

// procStateSession is a process's state and session from /proc/<pid>/stat. The comm field is
// in parentheses and may hold spaces and parentheses of its own, so the split starts after the
// LAST ")" (StartStamp).
func procStateSession(pid int) (state string, session int, ok bool) {
	raw, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return "", 0, false
	}
	i := strings.LastIndex(string(raw), ")")
	if i < 0 {
		return "", 0, false
	}
	// fields[0] is field 3 (state), so field 6 (session) is fields[3]
	fields := strings.Fields(string(raw)[i+1:])
	if len(fields) < 4 {
		return "", 0, false
	}
	session, err = strconv.Atoi(fields[3])
	if err != nil {
		return "", 0, false
	}
	return fields[0], session, true
}

// environHolds is whether a process's environment, as the kernel recorded it at its exec,
// holds the exact entry want.
func environHolds(pid int, want []byte) bool {
	raw, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/environ")
	if err != nil {
		return false
	}
	for _, ent := range bytes.Split(raw, []byte{0}) {
		if bytes.Equal(ent, want) {
			return true
		}
	}
	return false
}
