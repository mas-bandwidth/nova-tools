package sprint

import (
	"sync"
	"time"
)

// The server's line of control (docs/SPEC-SPRINT.md section 14, The server, "The tick's
// turn"): one holder at a time, a worker's batch, a lane's step (land's reads and report,
// decide, balance) or a tick of the run loop, so none runs during another. Batches take
// it in the order they asked. The tick does not queue behind them: when it asks, it takes
// the line as soon as no batch waits, and once its turn is due it takes the line next,
// after the holder in flight and before every batch still waiting. Its turn is due when
// the batches have had the line, since the tick before ended, for as long as that tick
// held it (TickTurnFloor at least, TickTurnCap at most). So the tick waits at most one
// turn and one batch in flight, however many batches wait, and the batches keep at least
// half the line while the tick is busy.

const (
	// TickTurnFloor is the least time the batches have the line between two ticks when
	// they wait for it: the run loop's TickFloor.
	TickTurnFloor = 100 * time.Millisecond
	// TickTurnCap is the most: with a tick and a batch each shorter, a tick of a loaded
	// server begins within 15 s (MachineSilence) of the tick before.
	TickTurnCap = 5 * time.Second
)

// lineTurns is the line's state and its rule: who holds it, the batches' tickets, and the
// tick's last turn. Its methods move no goroutine and read no clock: ControlLine moves
// them, and the tests step them through simulated time.
type lineTurns struct {
	held, tick    bool // the line is held; its holder is a tick
	since         time.Time
	next, serving uint64 // the batches' tickets: the next to give, the next to serve
	tickWants     bool
	tickEnd       time.Time     // when the tick before released the line
	tickHeld      time.Duration // how long it held it
}

// turnAt is when the tick's turn is due.
func (s *lineTurns) turnAt() time.Time {
	return s.tickEnd.Add(min(max(s.tickHeld, TickTurnFloor), TickTurnCap))
}

// batchesWait says a batch holds a ticket not yet served.
func (s *lineTurns) batchesWait() bool { return s.next != s.serving }

// tickMay says a tick that asks takes the line at now.
func (s *lineTurns) tickMay(now time.Time) bool {
	return !s.held && (!s.batchesWait() || !now.Before(s.turnAt()))
}

// batchMay says the batch holding ticket t takes the line at now.
func (s *lineTurns) batchMay(t uint64, now time.Time) bool {
	return !s.held && t == s.serving && !(s.tickWants && !now.Before(s.turnAt()))
}

// ticket is a batch's place in the order of asking.
func (s *lineTurns) ticket() uint64 {
	s.next++
	return s.next - 1
}

// takeTick and takeBatch give the line to a tick and to the batch next served.
func (s *lineTurns) takeTick(now time.Time) {
	s.held, s.tick, s.since, s.tickWants = true, true, now, false
}

func (s *lineTurns) takeBatch(now time.Time) {
	s.held, s.tick, s.since = true, false, now
	s.serving++
}

// release frees the line; a tick's release starts the batches' turn.
func (s *lineTurns) release(now time.Time) {
	if !s.held {
		panic("sprint: Unlock of a ControlLine not held")
	}
	if s.tick {
		s.tickEnd, s.tickHeld = now, now.Sub(s.since)
	}
	s.held, s.tick = false, false
}

// ControlLine is the server's line of control; its zero value is free. Lock, TryLock and
// Unlock are a batch's (and a lane's), TickLock is the run loop's.
type ControlLine struct {
	mu    sync.Mutex
	turns lineTurns
	free  chan struct{} // closed at the next release
}

// freed is the channel the next release closes; called with mu held.
func (l *ControlLine) freed() chan struct{} {
	if l.free == nil {
		l.free = make(chan struct{})
	}
	return l.free
}

// Lock takes the line for a batch, in the order batches asked.
func (l *ControlLine) Lock() {
	l.mu.Lock()
	t := l.turns.ticket()
	for !l.turns.batchMay(t, time.Now()) {
		free := l.freed()
		l.mu.Unlock()
		<-free
		l.mu.Lock()
	}
	l.turns.takeBatch(time.Now())
	l.mu.Unlock()
}

// TryLock takes the line for a batch when it is free and nothing waits for it.
func (l *ControlLine) TryLock() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	if l.turns.batchesWait() || !l.turns.batchMay(l.turns.next, now) {
		return false
	}
	l.turns.ticket()
	l.turns.takeBatch(now)
	return true
}

// TickLock takes the line for a tick and says how long the tick waited for it.
func (l *ControlLine) TickLock() time.Duration {
	l.mu.Lock()
	start := time.Now()
	l.turns.tickWants = true
	for {
		now := time.Now()
		if l.turns.tickMay(now) {
			l.turns.takeTick(now)
			l.mu.Unlock()
			return now.Sub(start)
		}
		free := l.freed()
		var due *time.Timer
		var at <-chan time.Time
		if left := l.turns.turnAt().Sub(now); left > 0 {
			due = time.NewTimer(left)
			at = due.C
		}
		l.mu.Unlock()
		select {
		case <-free:
		case <-at:
		}
		if due != nil {
			due.Stop()
		}
		l.mu.Lock()
	}
}

// Unlock frees the line.
func (l *ControlLine) Unlock() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.turns.release(time.Now())
	if l.free != nil {
		close(l.free)
		l.free = nil
	}
}
