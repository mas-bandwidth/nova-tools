package pulse

// The process table `wait --until process-gone` reads, and the two self-match bugs that
// made bin/wait-for's `gone` mode wait forever (nova-tools #2546).
//
// BUG ONE, the descendant. bin/wait-for polls with `pgrep -f <pattern>`, and bash forks an
// identical copy of the running script for each stage of a pipeline: same argv, different
// pid, a child of the waiter. That copy carries the pattern in its own argv, so it matched
// every poll and `wait-for gone <pattern>` could never return for any pattern naming the
// thing it was waiting on. Measured on darwin 2026-09-22.
//
// BUG TWO, the ancestor. The shell one level UP carries the pattern too --
// `bash -c "wait-for gone bin/harvest-priority && ..."` -- and procps `pgrep -f` on Linux
// matches an ancestor of the pgrep process where the BSD pgrep on darwin does not. That is
// the self-match that killed the coordinator's shell four times on 2026-09-21.
//
// Two rules end both, and neither of them is a platform:
//
//  1. The pattern is matched against argv[0] and argv[1] ONLY -- the program and the script
//     path it was handed -- never a later argument. A waiter carries the pattern it is
//     waiting on as an argument, so a match on the whole command line always finds itself.
//  2. This process, every ancestor of it, and every descendant of it are excluded, walked
//     over ONE snapshot of the table rather than by shelling `ps` per pid.
//
// The snapshot is a seam: the tests hand in a table, so the two bugs above are asserted
// without spawning a pipeline or a shell, and one integration test spawns a real process to
// prove the reader behind the seam reads a real machine.

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// WaitProc is one row of the process table: the pid, its parent, and the argv as the kernel
// shows it. Args is never empty for a row this package keeps.
type WaitProc struct {
	Pid  int
	PPid int
	Args []string
}

// maxAncestorHops bounds every walk up the parent chain. A table with a cycle in it is not
// a thing the kernel makes, but a snapshot raced against process exit can be reused pid
// against a reaped parent, and a walk with no bound would spin instead of answering.
const maxAncestorHops = 64

// ReadWaitProcs is the live process table: `ps -axo pid=,ppid=,args=`, one snapshot, parsed.
// The three fields with `=` suffixes suppress the header on both procps and the BSD ps, so
// no line has to be thrown away, and args= is last because it holds the spaces.
func ReadWaitProcs() ([]WaitProc, error) {
	out, err := exec.Command("ps", "-axo", "pid=,ppid=,args=").Output()
	if err != nil {
		return nil, fmt.Errorf("ps -axo pid=,ppid=,args=: %w", err)
	}
	return parseWaitProcs(string(out)), nil
}

// parseWaitProcs reads what ps printed. A row whose first two fields are not numbers, or that
// carries no argv at all, is dropped: it is a row nobody can match on, and keeping it as a
// zero pid would make every walk think it had reached the root.
func parseWaitProcs(out string) []WaitProc {
	var procs []WaitProc
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}
		ppid, err := strconv.Atoi(fields[1])
		if err != nil {
			continue
		}
		procs = append(procs, WaitProc{Pid: pid, PPid: ppid, Args: fields[2:]})
	}
	return procs
}

// WaitMatchesPattern reports whether pat names this process, by rule 1 above: pat is a
// substring of argv[0] or argv[1], each tested whole and as its basename. Whole so that
// `bin/harvest-priority` matches a process started by that path; basename so that
// `harvest-priority` matches it too. argv[2] onward is never read, which is what keeps a
// waiter from finding itself in the pattern it was given.
func WaitMatchesPattern(p WaitProc, pat string) bool {
	if pat == "" {
		return false
	}
	for i := 0; i < len(p.Args) && i < 2; i++ {
		tok := p.Args[i]
		if strings.Contains(tok, pat) || strings.Contains(filepath.Base(tok), pat) {
			return true
		}
	}
	return false
}

// WaitLiveMatches are the processes in the snapshot that pat names and that are not this
// waiter's own business: self, its ancestors and its descendants are dropped by rule 2.
// The result is in table order, so a receipt naming the first one names the same process
// twice running.
func WaitLiveMatches(procs []WaitProc, pat string, self int) []WaitProc {
	parent := make(map[int]int, len(procs))
	for _, p := range procs {
		parent[p.Pid] = p.PPid
	}
	mine := ancestorsOf(parent, self)
	mine[self] = true

	var out []WaitProc
	for _, p := range procs {
		if !WaitMatchesPattern(p, pat) {
			continue
		}
		if mine[p.Pid] || isDescendantOf(parent, p.Pid, self) {
			continue
		}
		out = append(out, p)
	}
	return out
}

// ancestorsOf is the set of pids strictly above pid in the snapshot, bounded and
// cycle-safe. It stops at pid 1 and at any pid the walk has already seen.
func ancestorsOf(parent map[int]int, pid int) map[int]bool {
	seen := map[int]bool{}
	cur := pid
	for hops := 0; hops < maxAncestorHops; hops++ {
		up, ok := parent[cur]
		if !ok || up <= 0 || up == cur || seen[up] {
			break
		}
		seen[up] = true
		cur = up
	}
	return seen
}

// isDescendantOf walks up from pid looking for root. It is the other half of rule 2: the
// pipeline copy of a script is a child of the script, with an argv the pattern matches.
func isDescendantOf(parent map[int]int, pid, root int) bool {
	cur := pid
	seen := map[int]bool{pid: true}
	for hops := 0; hops < maxAncestorHops; hops++ {
		up, ok := parent[cur]
		if !ok || up <= 0 || up == cur || seen[up] {
			return false
		}
		if up == root {
			return true
		}
		seen[up] = true
		cur = up
	}
	return false
}
