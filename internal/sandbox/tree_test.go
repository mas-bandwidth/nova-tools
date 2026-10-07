//go:build darwin || linux

package sandbox

import (
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
)

func pidsOf(rows []procRow) []int {
	var out []int
	for _, r := range rows {
		out = append(out, r.pid)
	}
	sort.Ints(out)
	return out
}

// TestTreeIsTheCommandsDescendantsInTheCallersGroup: the tree is found by parent pid,
// never by group, because the group is the caller's and holds the caller's other work.
func TestTreeIsTheCommandsDescendantsInTheCallersGroup(t *testing.T) {
	t.Parallel()
	rows := []procRow{
		{pid: 50, ppid: 1, pgid: 50},            // the caller, its group's leader
		{pid: 100, ppid: 50, pgid: 50},          // the tool
		{pid: 101, ppid: 100, pgid: 50},         // the command (top)
		{pid: 102, ppid: 101, pgid: 50, rss: 7}, // its child
		{pid: 103, ppid: 102, pgid: 50, rss: 5}, // a grandchild
		{pid: 104, ppid: 102, pgid: 50, zombie: true},
		{pid: 105, ppid: 102, pgid: 999}, // left the group by setsid: still a descendant
		{pid: 200, ppid: 50, pgid: 50},   // the caller's other work, in the same group
	}
	tr := &Tree{top: 101, keepTop: true, pgid: 50}
	assert.Equal(t, []int{101, 102, 103, 105}, pidsOf(tr.at(rows)), "the command and its live descendants, never the caller's other work")
}

// TestTreeKeepsAnOrphanItHasSeen: a leader that exits between two counts leaves its
// children reparented to pid 1; the ones already seen, still in the caller's group,
// are still the tree. An orphan never seen, or one in another group, is not.
func TestTreeKeepsAnOrphanItHasSeen(t *testing.T) {
	t.Parallel()
	tr := &Tree{top: 101, keepTop: true, pgid: 50}
	before := []procRow{
		{pid: 101, ppid: 100, pgid: 50},
		{pid: 102, ppid: 101, pgid: 50},
		{pid: 103, ppid: 101, pgid: 50},
	}
	assert.Equal(t, []int{101, 102, 103}, pidsOf(tr.at(before)))
	after := []procRow{
		{pid: 102, ppid: 1, pgid: 50},   // seen, orphaned, same group: still the tree
		{pid: 106, ppid: 102, pgid: 50}, // its new child
		{pid: 103, ppid: 1, pgid: 77},   // seen, but in another group now: not the caller's to reach
		{pid: 300, ppid: 1, pgid: 50},   // never seen: not the tree
	}
	assert.Equal(t, []int{102, 106}, pidsOf(tr.at(after)))
}

// TestTreeUnderTheToolIsItsChildren: on linux the tool is a subreaper and the tree hangs
// from the tool, which is not itself a member.
func TestTreeUnderTheToolIsItsChildren(t *testing.T) {
	t.Parallel()
	rows := []procRow{
		{pid: 100, ppid: 50, pgid: 50},
		{pid: 101, ppid: 100, pgid: 50},
		{pid: 102, ppid: 100, pgid: 50}, // an orphan reparented to the subreaper
		{pid: 103, ppid: 102, pgid: 50},
	}
	tr := &Tree{top: 100, pgid: 50}
	assert.Equal(t, []int{101, 102, 103}, pidsOf(tr.at(rows)))
}

// TestTreeDropsItsTopOnceReaped: once the command's own pid is reaped it may be handed
// to an unrelated process; the tree then hangs from the orphans it has seen and never
// from that number (the second read of 2026-10-06).
func TestTreeDropsItsTopOnceReaped(t *testing.T) {
	t.Parallel()
	tr := &Tree{top: 101, keepTop: true, pgid: 50}
	assert.Equal(t, []int{101, 102}, pidsOf(tr.at([]procRow{
		{pid: 101, ppid: 100, pgid: 50},
		{pid: 102, ppid: 101, pgid: 50},
	})))
	tr.Reaped()
	assert.Equal(t, []int{102}, pidsOf(tr.at([]procRow{
		{pid: 101, ppid: 7, pgid: 50},   // the number, reused by the caller's other work
		{pid: 103, ppid: 101, pgid: 50}, // and its child
		{pid: 102, ppid: 1, pgid: 50},   // the command's orphan, seen before
	})), "a reused pid is no part of the tree")
}

// TestTreeCountsWhatItListedBeforeTheReap: a tick lists the table while the command is
// alive, and the reap lands before the listing is read. The count is of the tree as
// listed: the top it hung from when the listing was taken (the third read of 2026-10-06:
// the final count found 0, want 2).
func TestTreeCountsWhatItListedBeforeTheReap(t *testing.T) {
	t.Parallel()
	tr := &Tree{top: 101, keepTop: true, pgid: 50}
	tr.list = func() ([]procRow, error) {
		rows := []procRow{
			{pid: 101, ppid: 100, pgid: 50},
			{pid: 102, ppid: 101, pgid: 50},
			{pid: 103, ppid: 101, pgid: 50},
		}
		tr.Reaped() // the command is collected between the listing and its reading
		return rows, nil
	}
	u, err := tr.Usage()
	assert.NoError(t, err)
	assert.Equal(t, 3, u.Procs, "the tree as listed")
}

// at is members of rows hung from the tree's top now.
func (t *Tree) at(rows []procRow) []procRow { return t.members(rows, t.topNow()) }
