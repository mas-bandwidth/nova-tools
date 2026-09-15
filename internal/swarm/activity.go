package swarm

// Child activity: what tells a working card from a dead one when neither writes a byte.
//
// Idle was a property of one file: a card whose log had not grown for --idle was killed.
// On 2026-09-15 cards 664-670 died "idle 300s" inside a `go test` that prints nothing for
// minutes (issue #593). The work was alive and the log was not, so the loop raised --idle to
// 900 s, which only delays the same kill. A card is idle when NOTHING moved: not its log and
// not its process tree.
//
// The reading here is CPU TIME OVER THE WHOLE TREE the card's runner started -- a
// grandchild's compiler and its test binary count, because they are the card's work as much
// as the harness is. A process blocked on I/O still charges system time for every completion
// it takes, so a card reading and writing advances this counter too; a card sleeping,
// waiting on a prompt that will never come, or wedged on a lock does not, and that is the
// card the idle timeout is for.
//
// Nothing here matches a process by its command line and nothing here runs `ps`: a snapshot
// is the kernel's own table, read by pid and parent pid, the way proc_*.go has always read
// it (the tripwire in the tests looks for pgrep, ps and a command-line compare).
//
// The snapshot is taken ONCE PER POLL and shared by every card, and that poll is slow beside
// the log poll -- activityInterval below -- because reading the machine's process table ten
// times a second to watch n quiet cards is a cost with no answer in it.

import "time"

// activityInterval is how often the tree is read, taken from the idle window the caller set:
// a quarter of it, never faster than half a second and never slower than five. A card is
// never killed for one quiet quarter -- the kill still needs the whole --idle -- so the
// coarse poll costs nothing but the sample's own age.
func activityInterval(idle time.Duration) time.Duration {
	d := idle / 4
	if d < 500*time.Millisecond {
		d = 500 * time.Millisecond
	}
	if d > 5*time.Second {
		d = 5 * time.Second
	}
	return d
}

// procSnapshot is one reading of this machine's process table, taken once and asked about
// each card in turn. A platform that cannot read the table hands back a snapshot that knows
// nothing, and TreeCPU's second return says so rather than pretending a zero: the monitor
// then watches the log alone, which is what it watched before this file existed.
type procSnapshot struct {
	// live is every pid the kernel listed, and children maps a pid to the pids it fathered,
	// so a tree is walked without ever reading a command line.
	live     map[int]bool
	children map[int][]int
	known    bool
}

// TreeCPU sums the CPU time of pid and every descendant of it in this snapshot, in whatever
// unit this platform counts -- nanoseconds on darwin, clock ticks on linux. The unit never
// leaves this file: the caller compares two readings of the same counter and asks only
// whether it grew. The second return is false when the platform cannot read the table, or
// when the tree holds no live process at all: neither is an activity reading.
func (s *procSnapshot) TreeCPU(pid int) (uint64, bool) {
	if s == nil || !s.known || pid <= 0 {
		return 0, false
	}
	var total uint64
	live := false
	seen := map[int]bool{}
	stack := []int{pid}
	for len(stack) > 0 {
		p := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seen[p] {
			continue
		}
		seen[p] = true
		if !s.live[p] {
			continue
		}
		live = true
		if c, ok := cpuOf(p); ok {
			total += c
		}
		stack = append(stack, s.children[p]...)
	}
	return total, live
}
