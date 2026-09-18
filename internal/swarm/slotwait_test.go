package swarm

import (
	"sync/atomic"
	"testing"
	"time"
)

// Red test: an-idle-slot-asks-on-an-event-not-a-poll (docs/SPEC-JOBS.md section 7).
//
// An idle slot whose queue is empty does not poll; it asks the coordinator for work by an
// event (nova-pulse watch). The slot reads its own queue once to find it empty, blocks in
// Ask, and reads it again only after the event fires -- it never spins on the queue.
func TestAnIdleSlotAsksOnAnEventNotAPoll(t *testing.T) {
	var reads, asks int32
	event := make(chan struct{})
	pending := func() int { atomic.AddInt32(&reads, 1); return 0 }
	ask := func() bool {
		atomic.AddInt32(&asks, 1)
		<-event
		return true
	}
	slot := SlotWait{Pending: pending, Ask: ask}

	done := make(chan int, 1)
	go func() { done <- slot.Wait() }()

	// Wait until the slot has asked once; it is now blocked on the event.
	deadline := time.Now().Add(2 * time.Second)
	for atomic.LoadInt32(&asks) == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := atomic.LoadInt32(&asks); got != 1 {
		t.Fatalf("idle slot asked %d times, want exactly 1", got)
	}

	// While blocked on the event the slot must read its queue exactly once -- the read
	// that found it empty -- and never again. More reads is a poll.
	time.Sleep(100 * time.Millisecond)
	if got := atomic.LoadInt32(&reads); got != 1 {
		t.Fatalf("idle slot read its empty queue %d times while blocked, want 1: it is polling", got)
	}
	select {
	case <-done:
		t.Fatal("the slot returned before any event fired")
	default:
	}

	close(event)
	select {
	case n := <-done:
		if n != 0 {
			t.Fatalf("after the event the empty queue yielded %d, want 0", n)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the slot did not return after its event fired")
	}
	if got := atomic.LoadInt32(&reads); got != 2 {
		t.Fatalf("after the event the slot read its queue %d times, want 2 (empty, then the event's answer)", got)
	}
}

// A slot with work queued never asks: it takes what its own queue already holds.
func TestASlotWithWorkQueuedNeverAsks(t *testing.T) {
	var asks int32
	slot := SlotWait{
		Pending: func() int { return 2 },
		Ask:     func() bool { atomic.AddInt32(&asks, 1); return true },
	}
	if got := slot.Wait(); got != 2 {
		t.Fatalf("a queued slot took %d, want 2", got)
	}
	if got := atomic.LoadInt32(&asks); got != 0 {
		t.Fatalf("a queued slot asked the coordinator %d times, want 0", got)
	}
}
