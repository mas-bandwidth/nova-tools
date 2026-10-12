package swarm

import (
	"bytes"
	"path/filepath"
)

// A LANE ENDS EVERYTHING ITS CARD STARTED (v1.2.15).
//
// Fleet member b1, 2026-10-12: a card's run backgrounded 16 shell busy loops through its slot's
// shell shim, the card finished, the epoch was cleared, and the loops held the machine at 100%
// for 13 hours. Native reaps its harness's process group (nativeEndLeftovers), and the loops
// were not in it: a harness runs its tool commands in sessions of their own, so what a tool
// command backgrounds leaves every group the lane knows. The leftovers seen on b1 that day were
// each in a session whose leader was gone, parented by systemd --user, and each still carried
// the NOVA_SWARM_JOB native had set for its harness.
//
// So a lane is two things, and the member ends both when the lane ends:
//
//   - its SESSION: the member starts native as the leader of a session of its own, so every
//     process group native and its harness make stays in it;
//   - its MARK: the member sets LaneEnv=<the launch's slot path> in native's environment,
//     native hands every NOVA_SWARM_ name to its harness, and a process inherits its parent's
//     environment, so a process that left the session for one of its own still carries it.
//
// A process is the lane's when it is in the lane's session or its environment holds the exact
// entry LaneEnv=<mark>. This is never a match on a command line (deadline.go): the mark is an
// environment entry the member wrote for this one launch, compared whole, and the process that
// asks (the member) never carries it. A process that empties its environment AND leaves the
// session escapes both; that is a daemon by intent, and the LANE END line's count does not
// include it.

// LaneEnv is the environment name a member marks each lane's process tree with; its value is
// the launch's slot directory, one per card launch on a machine.
const LaneEnv = "NOVA_SWARM_LANE"

// A LANE OUTLIVES NO MEMBER (v1.2.16).
//
// A member that dies without its drain (a SIGKILL, a crash, the OOM killer) ends none of its
// lanes: each native leads a session of its own, which no signal to the member reaches, so
// native, its harness and whatever the card started run on, owned by nobody. Adoption brought
// one back only when the same launch was dealt to the member again, and after an epoch clear or
// a redeal elsewhere it never is. So a member, when it starts, ends every lane an earlier
// member of its name left on the machine, before it takes any card (ReapOrphanLanes).
//
// The mark alone cannot say which member a lane was started by: it is the launch's slot path,
// and two members on one machine given the same --root or --slots share the slots directory.
// So a member also writes its name into each lane's environment (LaneOwnerEnv=<--as>), and an
// orphan is a process whose environment holds BOTH a mark that is a launch directly under
// this member's slots directory AND this member's name (OrphanOf), or one in the session such a
// process leads. A lane started by a member of another name, or under another slots directory,
// or by a build that wrote no owner, is never touched.

// LaneOwnerEnv is the environment name a member writes its own name (--as) under, beside the
// mark, in every lane it starts. It begins NOVA_SWARM_, so native hands it to its harness.
const LaneOwnerEnv = "NOVA_SWARM_LANE_OWNER"

// OrphanOf reports whether a process whose environment holds mark (LaneEnv's value) and owner
// (LaneOwnerEnv's value) is a lane of the member named as running its launches under slots: the
// owner is that name exactly, and the mark is one launch directory directly under slots. An
// empty name, slots or mark is never a match.
func OrphanOf(mark, owner, slots, as string) bool {
	if as == "" || slots == "" || mark == "" || owner != as {
		return false
	}
	if filepath.Clean(mark) != mark {
		return false
	}
	base := filepath.Base(mark)
	return filepath.Dir(mark) == filepath.Clean(slots) && base != "." && base != ".." && base != string(filepath.Separator)
}

// laneEntries is the values of LaneEnv and LaneOwnerEnv among an environment's entries, ""
// for one that is absent. Each entry is matched by its whole name and the "=" after it, never
// by a prefix of the name; the first entry of a name is the one a process reads.
func laneEntries(ents [][]byte) (mark, owner string) {
	markKey, ownerKey := []byte(LaneEnv+"="), []byte(LaneOwnerEnv+"=")
	var haveMark, haveOwner bool
	for _, ent := range ents {
		switch {
		case !haveMark && bytes.HasPrefix(ent, markKey):
			mark, haveMark = string(ent[len(markKey):]), true
		case !haveOwner && bytes.HasPrefix(ent, ownerKey):
			owner, haveOwner = string(ent[len(ownerKey):]), true
		}
	}
	return mark, owner
}
