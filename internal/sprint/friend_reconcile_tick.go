package sprint

import "fmt"

// The run loop's friend reconcile (docs/SPEC-SPRINT.md section 1,
// friend-reconcile-every-tick-r.w1; 2026-10-04, a friend's row read working=4 with nothing
// running, and stayed so until the coordinator typed friend reconcile): after each tick
// the server's run loop reconciles every friend with the plan friend reconcile applies
// (FriendReconcileOf, FriendReturn), so a phantom working count is gone by the next tick.
// What it adds is said, never decided: each card it moves is one note to the coordinator,
// and a friend whose row still disagrees with her QUEUE.json after the pass is one note
// an episode, the episode kept on the fleet table's properties as the idle alarm keeps
// its own (idle.go), so it lives across ticks and run loops.

// The run loop's reconcile notes: happened, addressed to the coordinator (the tick end
// wakes them, and inbox --push carries them to the bus).
const (
	NFriendCollected = "a friend's card collected by reconcile"
	NFriendDisagrees = "a friend's row disagrees with her QUEUE.json"
	NFriendAgrees    = "a friend's row agrees with her QUEUE.json again"
	// NFriendDirStalled is the run loop's reconcile passing a friend over because a read
	// of her directory did not answer within its deadline (cmd/nova-sprint
	// friendreconcile_tick.go), once an episode
	NFriendDirStalled = "a friend's directory did not answer the reconcile"
)

// PropFriendDisagree is the fleet table's property of a friend's disagreement episode:
// when its note was pushed ("" or absent: none).
func PropFriendDisagree(friend string) string { return "friend_disagree_" + friend }

// FriendQueueWorking is how many tasks of her account say working.
func FriendQueueWorking(a FriendAccount) int {
	n := 0
	for _, state := range a.Tasks {
		if state == FriendTaskWorking {
			n++
		}
	}
	return n
}

// FriendCollectedReq is one card the run loop's reconcile collected from a friend's outbox.
type FriendCollectedReq struct {
	Friend, Card, Primary, Stream, Who, Why string
}

// FriendCollected is the one note a collect by the run loop's reconcile pushes the
// coordinator, naming the card and why: the finish itself is friend sync's, its own notes
// its own.
func FriendCollected(s *Snapshot, r FriendCollectedReq) Plan {
	n := happened(NFriendCollected, r.Stream, s.Now, r.Primary)
	n.Who, n.To = r.Who, s.Coordinator
	n.What = fmt.Sprintf("%s collected from friend %s by the run loop's reconcile: %s", r.Card, r.Friend, r.Why)
	n.Hint = "run: nova-sprint card " + r.Primary
	return Plan{Notes: []Note{n}}
}

// FriendDisagreeReq is a friend's row and her account after the run loop's pass: the
// cards working on her row, and the tasks her QUEUE.json says working.
type FriendDisagreeReq struct {
	Friend, Who string
	Row, Queue  int
}

// FriendDisagree is a friend's disagreement episode: when her row's working count is not
// her QUEUE.json's and no note was pushed, one note to the coordinator and the episode
// marked said (PropFriendDisagree); when they agree again and one was, the mark is taken
// off and one note says so. Anything else plans nothing, so a lasting disagreement is one
// note, never one a tick.
func FriendDisagree(s *Snapshot, r FriendDisagreeReq) Plan {
	var p Plan
	if s.Fleet == nil {
		return p
	}
	name := PropFriendDisagree(r.Friend)
	said, had := s.Fleet.Prop(name)
	write := func(value string) {
		p.Props = append(p.Props, PropWrite{Table: Fleet, Name: name, Value: value, Was: said, WasAbsent: !had})
	}
	counts := fmt.Sprintf("her row working=%d, her QUEUE.json working=%d", r.Row, r.Queue)
	switch {
	case r.Row != r.Queue && said == "":
		n := happened(NFriendDisagrees, "", s.Now)
		n.Who, n.To = r.Who, s.Coordinator
		n.What = fmt.Sprintf("friend %s after the run loop's reconcile: %s", r.Friend, counts)
		n.Hint = "run: nova-sprint friend reconcile " + r.Friend + " --dry-run (what each card gets), nova-sprint card <card>"
		write(stamp(s.Now))
		p.Units = append(p.Units, Unit{Key: name, Notes: []Note{n}, Moved: "friend " + r.Friend + " disagrees with her QUEUE.json: " + counts + ": told " + orDash(s.Coordinator)})
	case r.Row == r.Queue && said != "":
		n := happened(NFriendAgrees, "", s.Now)
		n.Who, n.To = r.Who, s.Coordinator
		n.What = fmt.Sprintf("friend %s: %s, agreeing again after disagreeing since %s", r.Friend, counts, said)
		write("")
		p.Units = append(p.Units, Unit{Key: name, Notes: []Note{n}, Moved: "friend " + r.Friend + " agrees with her QUEUE.json again: " + counts})
	}
	return p
}
