package wake

import "time"

// The clock is injected, everywhere, for two reasons. The first is rule 9: every
// stamp this tool prints or stores is the TOOL's own clock, read at the moment
// of writing, and a time that reaches it inside text -- a note's body, a
// report's contents, a caller's flag -- is data and never used to order, age or
// deduplicate anything. There is no flag that sets a stamp and none will be
// added. The second is that a watcher's whole subject is elapsed time, and a
// test that has to wait sixteen real minutes to prove an eight-minute entry
// interval is a test nobody runs (the two-minute rule).
type Clock interface {
	Now() time.Time
	// Sleep advances to now+d. A real clock waits; a fake one moves its own
	// hands, so a sixteen-minute watch is a millisecond of test.
	Sleep(d time.Duration)
}

// Real is the clock a watch runs on.
type Real struct{}

func (Real) Now() time.Time { return time.Now() }
func (Real) Sleep(d time.Duration) {
	if d > 0 {
		time.Sleep(d)
	}
}

// Fake is the injected clock. It is here rather than in a _test.go file because
// two packages' tests need the same one -- cmd/nova-wake's and this package's --
// and two nearly-identical fake clocks is two chances to prove different things.
type Fake struct {
	at      time.Time
	Slept   time.Duration
	Sleeps  int
	OnSleep func(now time.Time) // a hook for a test that wants to move the world mid-watch
}

// NewFake starts a fake clock at the instant given.
func NewFake(at time.Time) *Fake { return &Fake{at: at} }

func (f *Fake) Now() time.Time { return f.at }

func (f *Fake) Sleep(d time.Duration) {
	if d <= 0 {
		return
	}
	f.at = f.at.Add(d)
	f.Slept += d
	f.Sleeps++
	if f.OnSleep != nil {
		f.OnSleep(f.at)
	}
}

// Advance moves a fake clock without counting a sleep, for a test that wants to
// put a gap BETWEEN two calls.
func (f *Fake) Advance(d time.Duration) { f.at = f.at.Add(d) }
