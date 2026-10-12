//go:build unix

package swarm

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// The unit tier of the orphan reap (lanereap.go, "A LANE OUTLIVES NO MEMBER"): the selection
// on fakes, no real process and no real time. The reap of a real orphaned lane is the
// functional tier (lanereap_orphan_functional_test.go).

// TestOrphanOfIsTheOwnerAndALaunchDirectlyUnderTheSlots pins which lane entries make a process
// a lane of the member named m1 running under /r/slots: the owner exactly, and a mark that is
// one launch directly under the slots directory. Every other shape is another member's, a
// build's that wrote no owner, or no lane.
func TestOrphanOfIsTheOwnerAndALaunchDirectlyUnderTheSlots(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		mark, owner, slots, as string
		want                   bool
	}{
		{"/r/slots/a-1.w1.g1.e1", "m1", "/r/slots", "m1", true},
		{"/r/slots/a-1.w1.g1.e1", "m1", "/r/slots/", "m1", true},
		{"/r/slots/a-1.w1.g1.e1", "m2", "/r/slots", "m1", false},     // a member of another name
		{"/r/slots/a-1.w1.g1.e1", "", "/r/slots", "m1", false},       // a build that wrote no owner
		{"/r/slots/a-1.w1.g1.e1", "m1", "/r/other", "m1", false},     // another slots directory
		{"/r/slots/sub/a-1.w1.g1.e1", "m1", "/r/slots", "m1", false}, // not directly under
		{"/r/slots", "m1", "/r/slots", "m1", false},                  // the slots directory itself
		{"/r/slots/../slots/a-1", "m1", "/r/slots", "m1", false},     // not a clean path
		{"/r/slots-other/a-1", "m1", "/r/slots", "m1", false},        // a sibling sharing the prefix
		{"", "m1", "/r/slots", "m1", false},                          // no mark
		{"/r/slots/a-1.w1.g1.e1", "", "/r/slots", "", false},         // no name
		{"/r/slots/a-1.w1.g1.e1", "m1", "", "m1", false},             // no slots
		{"/r/slots/a-1.w1.g1.e1", "m1x", "/r/slots", "m1", false},    // a name that only begins with this one
		{"slots/a-1.w1.g1.e1", "m1", "/r/slots", "m1", false},        // a relative mark never matches an absolute slots
	} {
		require.Equal(t, c.want, OrphanOf(c.mark, c.owner, c.slots, c.as), "mark %q owner %q slots %q as %q", c.mark, c.owner, c.slots, c.as)
	}
}

// TestLaneOwnerEnvIsItsWireName pins the owner's environment name: lanes a member of an
// earlier build started carry it under this name, and cmd/nova-swarm's functional test spells
// it out (laneOwnerEnvName). It begins NOVA_SWARM_, so native hands it to its harness.
func TestLaneOwnerEnvIsItsWireName(t *testing.T) {
	t.Parallel()
	require.Equal(t, "NOVA_SWARM_LANE_OWNER", LaneOwnerEnv)
	require.True(t, strings.HasPrefix(LaneOwnerEnv, "NOVA_SWARM_"))
}

// TestLaneEntriesMatchesWholeNames: the mark and the owner are read by their whole names, and
// the first entry of a name wins, as a process reads its environment.
func TestLaneEntriesMatchesWholeNames(t *testing.T) {
	t.Parallel()
	mark, owner := laneEntries([][]byte{
		[]byte("NOVA_SWARM_LANEX=/no"), []byte(LaneOwnerEnv + "=m1"), []byte(LaneEnv + "=/r/slots/a"),
		[]byte(LaneEnv + "=/r/slots/b"), []byte(LaneOwnerEnv + "=m2"),
	})
	require.Equal(t, "/r/slots/a", mark)
	require.Equal(t, "m1", owner)
	mark, owner = laneEntries([][]byte{[]byte("PATH=/bin")})
	require.Empty(t, mark)
	require.Empty(t, owner)
}

// TestOrphanProcsKeepsToTheMembersLanes pins the selection over one scan: an orphan of this
// member is ended with every process of the session it leads; a process of a session whose
// leader is not this member's orphan, one in the asking process's own session, a lane this
// member runs now, and pid 1 are never selected.
func TestOrphanProcsKeepsToTheMembersLanes(t *testing.T) {
	t.Parallel()
	const own = 500
	lane, other := "/r/slots/a-1", "/r/slots/a-2"
	procs := []laneProcInfo{
		{pid: 100, started: "s100", sid: 100, mark: lane, owner: "m1"},  // an orphaned native, its session's leader
		{pid: 101, started: "s101", sid: 100},                           // in that session, its environment emptied
		{pid: 102, started: "s102", sid: 102, mark: lane, owner: "m1"},  // left the session, still marked
		{pid: 200, started: "s200", sid: 200, mark: lane, owner: "m2"},  // another member's
		{pid: 201, started: "s201", sid: 200},                           // in another member's session
		{pid: 300, started: "s300", sid: 300, mark: other, owner: "m1"}, // a lane this member runs now
		{pid: 301, started: "s301", sid: 300},                           // in that lane's session
		{pid: 400, started: "s400", sid: own, mark: lane, owner: "m1"},  // in the asker's own session
		{pid: own, started: "s500", sid: own},                           // the asker's session leader
		{pid: 1, started: "s1", sid: 1, mark: lane, owner: "m1"},        // init
		{pid: 600, started: "s600", sid: 600},                           // a stranger
	}
	marks := map[string]bool{}
	got := orphanProcs(procs, "/r/slots", "m1", own, func(m string) bool { return m == other }, marks)
	require.ElementsMatch(t, []laneProcess{{pid: 100, started: "s100"}, {pid: 101, started: "s101"}, {pid: 102, started: "s102"}}, got)
	require.Equal(t, map[string]bool{lane: true}, marks)
	// the asker's session unknown: only the marked processes, no session is ended
	got = orphanProcs(procs, "/r/slots", "m1", -1, nil, map[string]bool{})
	require.ElementsMatch(t, []laneProcess{{pid: 100, started: "s100"}, {pid: 102, started: "s102"}, {pid: 300, started: "s300"}, {pid: 400, started: "s400"}}, got)
}
