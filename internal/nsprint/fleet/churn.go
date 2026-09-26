package fleet

// Churn (#4310) is the process-age sample `nova-sprint fleet churn` takes on
// every registered machine: one ps over ssh, then per machine the commands
// younger than the window with counts (the relaunch churn: a bench-row every
// second, a ci-run dead and relaunched, a grok heartbeat) and every process
// older than a day whose parent is init (the leak: a loop nobody restarted).
// The sample's own pipeline (sshd, the shell it started, ps, awk) is left
// out, since it is always young.

import (
	"fmt"
	"path"
	"sort"
	"strconv"
	"strings"
)

// Proc is one sampled process: its age in seconds, its parent and its
// command name (the base name, a login shell's leading - dropped).
type Proc struct {
	PID, PPID, Age int
	Comm           string
}

// OldAfter is the age past which a process under init is reported.
const OldAfter = 24 * 60 * 60

// DefaultWindow is the young window in seconds.
const DefaultWindow = 12

// PSCommand is the one ps line for a machine's OS: etimes (seconds) on
// linux, etime ([[dd-]hh:]mm:ss) on darwin, where etimes does not exist.
func PSCommand(goos string) (string, error) {
	switch goos {
	case "linux":
		return "ps -eo pid,etimes,ppid,comm", nil
	case "darwin":
		return "ps -Ao pid,etime,ppid,comm", nil
	}
	return "", fmt.Errorf("no ps sample for %s (linux and darwin)", goos)
}

// ParseEtime reads ps's etime: [[dd-]hh:]mm:ss, seconds.
func ParseEtime(s string) (int, error) {
	days := 0
	if d, rest, ok := strings.Cut(s, "-"); ok {
		n, err := strconv.Atoi(d)
		if err != nil {
			return 0, fmt.Errorf("etime %q: bad days", s)
		}
		days, s = n, rest
	}
	parts := strings.Split(s, ":")
	if len(parts) < 2 || len(parts) > 3 {
		return 0, fmt.Errorf("etime %q: want [[dd-]hh:]mm:ss", s)
	}
	if len(parts) == 2 && days > 0 {
		// dd-mm:ss never happens; ps prints dd-hh:mm:ss.
		return 0, fmt.Errorf("etime %q: days without hours", s)
	}
	hms := 0
	for _, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return 0, fmt.Errorf("etime %q: bad field %q", s, p)
		}
		hms = hms*60 + n
	}
	return days*86400 + hms, nil
}

// ParsePS reads the sample PSCommand(goos) printed: pid, age, ppid, comm per
// line, the header skipped. darwin's comm is a path (/usr/sbin/sshd) and a
// login shell is -bash; both are reduced to the base name.
func ParsePS(goos, out string) ([]Proc, error) {
	var procs []Proc
	for i, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) == 0 || (i == 0 && f[0] == "PID") {
			continue
		}
		if len(f) < 4 {
			return nil, fmt.Errorf("ps line %d %q: want pid, age, ppid, comm", i+1, line)
		}
		pid, err1 := strconv.Atoi(f[0])
		ppid, err2 := strconv.Atoi(f[2])
		if err1 != nil || err2 != nil {
			return nil, fmt.Errorf("ps line %d %q: pid and ppid are numbers", i+1, line)
		}
		var age int
		var err error
		if goos == "darwin" {
			age, err = ParseEtime(f[1])
		} else {
			age, err = strconv.Atoi(f[1])
		}
		if err != nil {
			return nil, fmt.Errorf("ps line %d: %v", i+1, err)
		}
		comm := strings.TrimPrefix(path.Base(strings.Join(f[3:], " ")), "-")
		procs = append(procs, Proc{PID: pid, PPID: ppid, Age: age, Comm: comm})
	}
	return procs, nil
}

// Churn is one machine's reading.
type Churn struct {
	Young      map[string]int // command -> processes younger than the window
	YoungTotal int
	Old        []Proc // older than OldAfter with parent 1
}

// sampleOwn names the sample's own pipeline and the daemon that started it.
var sampleOwn = map[string]bool{"ps": true, "awk": true, "sshd": true, "sshd-session": true}

// Sample reads procs against window seconds. The sample's own pipeline is
// left out: ps, awk, sshd, and the shell ps runs under (its parent).
func Sample(procs []Proc, window int) Churn {
	c := Churn{Young: map[string]int{}}
	shells := map[int]bool{}
	for _, p := range procs {
		if p.Comm == "ps" {
			shells[p.PPID] = true
		}
	}
	for _, p := range procs {
		if sampleOwn[p.Comm] || shells[p.PID] {
			continue
		}
		if p.Age < window {
			c.Young[p.Comm]++
			c.YoungTotal++
		}
		if p.Age >= OldAfter && p.PPID == 1 {
			c.Old = append(c.Old, p)
		}
	}
	sort.Slice(c.Old, func(i, j int) bool {
		if c.Old[i].Age != c.Old[j].Age {
			return c.Old[i].Age > c.Old[j].Age
		}
		return c.Old[i].PID < c.Old[j].PID
	})
	return c
}

// Lines is the report for machine: one line per young command (most first),
// one per old process, and the CHURN summary last.
func (c Churn) Lines(machine string) []string {
	names := make([]string, 0, len(c.Young))
	for n := range c.Young {
		names = append(names, n)
	}
	sort.Slice(names, func(i, j int) bool {
		if c.Young[names[i]] != c.Young[names[j]] {
			return c.Young[names[i]] > c.Young[names[j]]
		}
		return names[i] < names[j]
	})
	var out []string
	for _, n := range names {
		out = append(out, fmt.Sprintf("%s young %s %d", machine, n, c.Young[n]))
	}
	for _, p := range c.Old {
		out = append(out, fmt.Sprintf("%s old pid=%d age=%dd comm=%s", machine, p.PID, p.Age/86400, p.Comm))
	}
	return append(out, fmt.Sprintf("CHURN %s young=%d old=%d", machine, c.YoungTotal, len(c.Old)))
}
