package swarm

import (
	"os"
	"testing"
	"time"
)

// THE LAUNCH TIMEOUT IS THE WHOLE HANDSHAKE, RETRIES INCLUDED (Stella, #126).
//
// The collision wait in fileretry.go is a ceiling on ONE read. The handshake reads the slot
// file every 20ms for its whole launch timeout, so a retry paid per turn is a retry
// multiplied: 500 turns x SteadyWindow is 1010s spent inside a 10s bound, with run.lock
// held and no line printed until it ends. The old loop could not see this, because it
// counted its own sleeps (`waited += 20ms`) instead of reading a clock, and handed each
// read the full window regardless of what was left.
//
// This is the fixture for the LIVE supervisor -- the one the early-exit channel does not
// help -- with every read of the slot colliding. It fails against the loop this repair
// replaced (there, roughly ceil(timeout/20ms) x SteadyWindow) and passes here at the
// configured bound, with no Windows and no race: the collision is produced by the
// forceTransientIO seam over a slot file that is a directory, so every read fails and every
// failure is called transient.
func TestTheLaunchHandshakeEndsAtItsOwnTimeoutWhenEveryReadCollides(t *testing.T) {
	dir := t.TempDir()
	p, err := OpenPool(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(p.Path(Slots, "1.json"), 0o755); err != nil {
		t.Fatal(err)
	}
	forceTransientIO = func(err error) bool { return err != nil }
	t.Cleanup(func() { forceTransientIO = nil })

	// One read on its own still gets no more than the budget its caller has left: this is
	// the min(window, remaining) that makes the loop below add up.
	short := 50 * time.Millisecond
	oneStart := time.Now()
	if _, err := p.ReadSlotBy(1, oneStart.Add(short)); err == nil {
		t.Fatal("this fixture wants a slot file that cannot be read")
	}
	if one := time.Since(oneStart); one > short+SteadyWindow/4 {
		t.Fatalf("one read given %s of budget waited %s: the collision window is being spent past the caller's deadline", short, one)
	}

	// THE HANDSHAKE, with a supervisor that is ALIVE: `gone` is never closed, so the early
	// exit does not fire and the clock is the only bound there is.
	const timeout = 300 * time.Millisecond
	in := RunInput{Pool: p}
	gone := make(chan struct{})
	start := time.Now()
	_, identified, supervisorGone := in.awaitIdentity(1, "abc123", start.Add(timeout), gone)
	elapsed := time.Since(start)
	if identified {
		t.Fatal("no identity was ever written, and the handshake claimed one")
	}
	if supervisorGone {
		t.Fatal("this fixture's supervisor is alive: the handshake must end on the clock, not on an exit")
	}
	if elapsed < timeout {
		t.Fatalf("the handshake is given %s and returned after %s: it did not wait out its own bound", timeout, elapsed)
	}
	// The slack is one read's overhead, not one collision window: SteadyWindow is 2s and a
	// single unbounded read would blow this on its own.
	if slack := 500 * time.Millisecond; elapsed > timeout+slack {
		t.Fatalf("the handshake is given %s and took %s: a retrying read is being ADDED to the launch timeout instead of living inside it", timeout, elapsed)
	}
}
