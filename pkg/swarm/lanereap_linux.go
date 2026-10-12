//go:build linux

package swarm

import (
	"bytes"
	"os"
	"strconv"
	"strings"
	"time"
)

// environBound is the most a listing waits for one process's /proc/<pid>/environ. The read
// takes the target's memory-map lock, and a process that holds it and does not let go (one
// paging under memory pressure, one stopped mid-exec, one in an uninterruptible wait) would
// hold the listing, and the member's start with it, for as long as it stays so. Past the
// bound the process is listed with no lane entries, as one whose environment this user may
// not read is, and its pid goes into the reap's stuck set so no later round asks it again.
const environBound = 2 * time.Second

// readEnviron is /proc/<pid>/environ split into its NUL-separated entries, nil when it cannot
// be read, or when the read did not return within environBound (readWithin), or when pid is in
// stuck already. stuck is one reap's record of the pids whose read ran out, filled here; nil
// records nothing and asks every time.
func readEnviron(pid int, stuck map[int]bool) [][]byte {
	if stuck[pid] {
		return nil
	}
	expiry := time.NewTimer(environBound)
	defer expiry.Stop()
	raw, ok := readWithin(func() ([]byte, error) {
		return os.ReadFile("/proc/" + strconv.Itoa(pid) + "/environ")
	}, expiry.C)
	if !ok {
		if stuck != nil {
			stuck[pid] = true
		}
		return nil
	}
	if raw == nil {
		return nil
	}
	return bytes.Split(raw, []byte{0})
}

// laneProcs lists the live processes of a lane other than this one (lanereap.go): those in
// session sid (sid > 0), and those whose environment holds the entry LaneEnv=mark (mark !=
// ""). It reads the kernel's own records: field 3 (state) and field 6 (session) of
// /proc/<pid>/stat, and /proc/<pid>/environ, one exact NUL-separated entry, never a command
// line. A zombie is not live. A process whose records this user may not read is not listed,
// nor by its environment one whose environ read ran past environBound (stuck, readEnviron).
func laneProcs(sid int, mark string, stuck map[int]bool) ([]laneProcess, bool) {
	ents, err := os.ReadDir("/proc")
	if err != nil {
		return nil, false
	}
	self := os.Getpid()
	var want []byte
	if mark != "" {
		want = []byte(LaneEnv + "=" + mark)
	}
	var out []laneProcess
	for _, e := range ents {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid <= 1 || pid == self {
			continue
		}
		started := LaneStartStamp(pid)
		state, session, ok := procStateSession(pid)
		if !ok || state == "Z" || state == "X" {
			continue
		}
		if (sid > 0 && session == sid) || (want != nil && environHolds(pid, want, stuck)) {
			out = append(out, laneProcess{pid: pid, started: started})
		}
	}
	return out, true
}

// scanLaneProcs lists every live process other than this one with its birth, its session and
// the lane entries of its environment (ReapOrphanLanes), from the kernel's own records: field 3
// (state) and field 6 (session) of /proc/<pid>/stat and /proc/<pid>/environ, each entry
// compared by its name up to the first "=", never a command line. A zombie is not live; a
// process whose environment this user may not read, or whose environ read ran past
// environBound (stuck, readEnviron), is listed with no lane entries.
func scanLaneProcs(stuck map[int]bool) ([]laneProcInfo, bool) {
	ents, err := os.ReadDir("/proc")
	if err != nil {
		return nil, false
	}
	self := os.Getpid()
	var out []laneProcInfo
	for _, e := range ents {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid <= 1 || pid == self {
			continue
		}
		started := LaneStartStamp(pid)
		state, session, ok := procStateSession(pid)
		if !ok || state == "Z" || state == "X" {
			continue
		}
		p := laneProcInfo{pid: pid, started: started, sid: session}
		if ents := readEnviron(pid, stuck); ents != nil {
			p.mark, p.owner = laneEntries(ents)
		}
		out = append(out, p)
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
// holds the exact entry want (readEnviron: within environBound, once per stuck pid).
func environHolds(pid int, want []byte, stuck map[int]bool) bool {
	for _, ent := range readEnviron(pid, stuck) {
		if bytes.Equal(ent, want) {
			return true
		}
	}
	return false
}
