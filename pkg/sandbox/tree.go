//go:build darwin || linux

package sandbox

import (
	"fmt"
	"os"
	"sync"
	"syscall"
	"time"
)

// procRow is one row of the process table, as the tree count reads it.
type procRow struct {
	pid, ppid, pgid int
	rss             int64 // bytes
	zombie          bool
	stopped         bool // by a signal: it runs nothing and forks nothing until continued
}

// Tree is the walled command's process tree, the thing the caps count and kill
// ("wall-caps-processes.w1"). Rule 12 keeps the tree in the CALLER's process group, so no
// group id of the tool's names it: it is found by parent pid, every count, from top.
// A member that is orphaned (its parent exits, it is reparented to pid 1) stays a member
// while it is still in the group it was seen in. A process orphaned between two counts
// was never seen and escapes the count on macOS (on linux the tool is a subreaper, so
// there is no such orphan); it is still in the caller's group, and the caller's group
// kill at its deadline is what reaches it: that is rule 12's reason.
type Tree struct {
	top     int  // the pid the tree hangs from
	keepTop bool // whether top is itself a member: the command is; the tool is not
	pgid    int  // the caller's group the tree was started in
	list    func() ([]procRow, error)

	mu   sync.Mutex
	seen map[int]bool
}

// Reaped is called once the command's own pid has been collected: the number may be
// handed to an unrelated process from then on, so a tree that hung from it hangs only
// from the orphans it has seen. A tree that hangs from the tool keeps its top.
func (t *Tree) Reaped() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.keepTop {
		t.top = -1
	}
}

// topNow is the pid the tree hangs from, read under the lock BEFORE a listing is taken:
// the listing is then read against the top it was taken under, so a reap that lands
// between the two drops none of the children it listed (the third read of 2026-10-06).
func (t *Tree) topNow() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.top
}

// members is the tree in rows, hung from top: top's live descendants (and top when keepTop), plus the
// orphans of the last count still in the caller's group, and their descendants. A zombie
// is dead and is not a member. It records what it found for the next count.
func (t *Tree) members(rows []procRow, top int) []procRow {
	t.mu.Lock()
	defer t.mu.Unlock()
	kids := map[int][]procRow{}
	var queue []procRow
	for _, r := range rows {
		kids[r.ppid] = append(kids[r.ppid], r)
		switch {
		case r.pid == top:
			if t.keepTop {
				queue = append(queue, r)
			}
		case r.ppid == 1 && r.pgid == t.pgid && t.seen[r.pid]:
			queue = append(queue, r)
		}
	}
	if !t.keepTop {
		queue = append(queue, kids[top]...)
	}
	var out []procRow
	next := map[int]bool{}
	for len(queue) > 0 {
		r := queue[0]
		queue = queue[1:]
		if next[r.pid] || r.pid <= 1 || r.pid == top && !t.keepTop {
			continue
		}
		next[r.pid] = true
		if !r.zombie {
			out = append(out, r)
		}
		queue = append(queue, kids[r.pid]...)
	}
	t.seen = next
	return out
}

// Usage counts the tree's live processes and sums their resident bytes.
func (t *Tree) Usage() (Usage, error) {
	top := t.topNow()
	rows, err := t.list()
	if err != nil {
		return Usage{}, err
	}
	var u Usage
	for _, r := range t.members(rows, top) {
		u.Procs++
		u.RSS += r.rss
	}
	return u, nil
}

// KillAndAwait ends the tree and answers how many of its processes are still running,
// bounded. A kill from one listing misses a child forked after it, which outlives its
// parent as an orphan never seen (the second read of 2026-10-06: 35-39 of a forking loop
// left running after "killed"). So the tree is FROZEN first: SIGSTOP to every member, and
// again on a fresh listing, until no member is new or still running; a stopped parent
// forks nothing and its children stay its descendants. Then SIGKILL to every member, and
// again until a count finds none or the bound is spent. -1 is a tree that could not be
// counted at the end, which is never reported as killed.
func (t *Tree) KillAndAwait() int {
	for i := 0; i < 200; i++ {
		running := 0
		live, _ := t.live()
		for _, r := range live {
			if !r.stopped {
				running++
				// ignored: the process may be gone between the count and the signal; the next count is the check
				_ = syscall.Kill(r.pid, syscall.SIGSTOP)
			}
		}
		if running == 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	for i := 0; i < 100; i++ {
		live, ok := t.live()
		if ok && len(live) == 0 {
			return 0
		}
		for _, r := range live {
			// ignored: the process may be gone between the count and the signal; the next count is the check
			_ = syscall.Kill(r.pid, syscall.SIGKILL)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if live, ok := t.live(); ok {
		return len(live)
	}
	return -1
}

// live is the tree's members this instant, never the tool itself, and whether the
// process table could be read at all.
func (t *Tree) live() ([]procRow, bool) {
	top := t.topNow()
	rows, err := t.list()
	if err != nil {
		return nil, false
	}
	self := os.Getpid()
	var out []procRow
	for _, r := range t.members(rows, top) {
		if r.pid > 1 && r.pid != self {
			out = append(out, r)
		}
	}
	return out, true
}

// RunawayLine is the end of the SANDBOX RUNAWAY line for what KillAndAwait answered:
// "killed" only when a count found nothing of the tree left.
func RunawayLine(left int) string {
	switch {
	case left == 0:
		return "the process tree was killed"
	case left < 0:
		return "the process tree was sent SIGKILL and could not be counted after"
	}
	return fmt.Sprintf("the process tree was sent SIGKILL and %d of its processes are still running", left)
}
