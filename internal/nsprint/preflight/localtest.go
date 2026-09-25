package preflight

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// 7.26 (nova-tools#3899): batch tests never run in the coordinator's
// session. The stream branch is tested by a CI request a bench claims, and
// the lander waits on ci:<repo>:<head>; a go test process under the session
// preflight runs in is a finding.

// Proc is one process of a ps snapshot.
type Proc struct {
	PID, PPID int
	Args      string
}

// ListProcs is one ps snapshot of every process: pid, parent, command line.
func ListProcs(ctx context.Context) ([]Proc, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "ps", "-A", "-ww", "-o", "pid=", "-o", "ppid=", "-o", "args=").Output()
	if err != nil {
		return nil, fmt.Errorf("ps: %w", err)
	}
	return ParseProcs(string(out)), nil
}

// ParseProcs reads ps lines of pid, ppid and the command line.
func ParseProcs(out string) []Proc {
	var procs []Proc
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) < 3 {
			continue
		}
		pid, err1 := strconv.Atoi(f[0])
		ppid, err2 := strconv.Atoi(f[1])
		if err1 != nil || err2 != nil {
			continue
		}
		procs = append(procs, Proc{PID: pid, PPID: ppid, Args: strings.Join(f[2:], " ")})
	}
	return procs
}

// isGoTest is a go test command line: the go tool with test as its verb.
func isGoTest(args string) bool {
	f := strings.Fields(args)
	return len(f) >= 2 && filepath.Base(f[0]) == "go" && f[1] == "test"
}

// sessionRoot is the root of the session self runs in: the nearest ancestor
// that is a claude process (the Claude Code session the coordinator runs
// in), else the topmost ancestor under pid 1 (a terminal's login shell).
func sessionRoot(byPID map[int]Proc, self int) int {
	root := self
	for pid, hops := self, 0; hops < 256; hops++ {
		p, ok := byPID[pid]
		if !ok || p.PPID <= 1 {
			break
		}
		pid = p.PPID
		root = pid
		if a, ok := byPID[pid]; ok {
			if f := strings.Fields(a.Args); len(f) > 0 && filepath.Base(f[0]) == "claude" {
				return pid
			}
		}
	}
	return root
}

// CheckLocalBatchTests is 7.26: RED for each go test process under self's
// session root. Self's own ancestors are not findings (a go test that runs
// preflight's control is its caller, not a batch the coordinator started).
// A ps that failed is RED: no snapshot is not an empty session.
func CheckLocalBatchTests(procs []Proc, listErr error, self int) Line {
	const n, name = "7.26", "local batch tests"
	if listErr != nil {
		return Line{N: n, Name: name, Red: true, Why: "could not list processes: " + listErr.Error()}
	}
	byPID := make(map[int]Proc, len(procs))
	for _, p := range procs {
		byPID[p.PID] = p
	}
	root := sessionRoot(byPID, self)
	mine := map[int]bool{}
	for pid, hops := self, 0; hops < 256; hops++ {
		mine[pid] = true
		p, ok := byPID[pid]
		if !ok || p.PPID <= 1 {
			break
		}
		pid = p.PPID
	}
	under := func(pid int) bool {
		for hops := 0; hops < 256; hops++ {
			if pid == root {
				return true
			}
			p, ok := byPID[pid]
			if !ok || p.PPID <= 1 {
				return false
			}
			pid = p.PPID
		}
		return false
	}
	var reds []string
	for _, p := range procs {
		if isGoTest(p.Args) && !mine[p.PID] && under(p.PID) {
			args := p.Args
			if len(args) > 80 {
				args = args[:80]
			}
			reds = append(reds, fmt.Sprintf("go test pid=%d under session root %d: %s", p.PID, root, args))
		}
	}
	if len(reds) == 0 {
		return Line{N: n, Name: name, Why: fmt.Sprintf("no go test under session root %d; batch tests are ci requests a bench claims", root)}
	}
	return Line{N: n, Name: name, Red: true, Why: limit(reds, 6) + "; remedy: stop it; the batch test is a ci request (nova-sprint ci request) " +
		"a bench runs (nova-sprint ci run), and nova-sprint land waits on ci:<repo>:<head>"}
}
