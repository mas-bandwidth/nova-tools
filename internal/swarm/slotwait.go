package swarm

// Backpressure and idle: an idle slot asks on an event, never a poll (docs/SPEC-JOBS.md
// section 7).
//
// An idle slot whose queue is empty does not spin on its queue. It reads its own queue once
// to find it empty, then blocks in Ask -- the coordinator's nova-pulse watch event -- and
// reads the queue again only when that event says work may have arrived. A slot that read
// its queue in a loop while idle would spend a core doing nothing.

// SlotWait is one slot's wait. Pending reports how many cards the slot's own queue holds and
// is the only read of that queue; Ask blocks on the coordinator's event (nova-pulse watch)
// and returns true when the event says to look again. Both are seams so a test drives them
// with a fake queue and a fake clock.
type SlotWait struct {
	Pending func() int
	Ask     func() bool
}

// Wait returns the number of cards the slot may take. With work already queued it returns it
// and never calls Ask. With an empty queue it calls Ask exactly once and reads the queue
// once more only when the event fired; it never loops on Pending.
func (s SlotWait) Wait() int {
	n := s.Pending()
	if n > 0 {
		return n
	}
	if s.Ask() {
		return s.Pending()
	}
	return 0
}
