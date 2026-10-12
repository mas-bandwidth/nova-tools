package swarm

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
