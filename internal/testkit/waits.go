package testkit

import (
	"sync"
	"time"
)

// Waits is a fake for a wait seam of the shape
// func(stop <-chan struct{}, d time.Duration) bool: "true once d has passed,
// false when stop is observed closed" (delayproxy.Clock.Wait). It is for
// code whose tests cannot run in a testing/synctest bubble because the code
// blocks on real I/O (a loopback socket), and it is not a clock: it records
// every d asked for and returns true at once, unless the wait is held.
//
// A held wait is the test's hand inside the code: the code stops in its wait,
// the test looks at what it has done so far (Holding tells it when), and then
// releases the waits (true) or closes stop (false). An already-closed stop
// makes even an unheld wait return false. If stop and release are both ready
// when a held wait chooses, stop wins; Wait does not infer which channel was
// closed first. What a held wait waits for is the test, never the time, so the
// first of two waits of the same length can be held while the second goes
// through, which no clock can do.
type Waits struct {
	mu      sync.Mutex
	held    *sync.Cond // broadcast when a wait starts being held
	asked   []time.Duration
	hold    int // the first hold waits are held; -1 holds every one
	holding int // waits held at this moment
	release chan struct{}
}

// NewWaits is a Waits that holds none of its waits.
func NewWaits() *Waits {
	w := &Waits{release: make(chan struct{})}
	w.held = sync.NewCond(&w.mu)
	return w
}

// Hold makes the first n waits held (every wait when n is negative) until
// Release or their stop. It is set before the code runs, and returns w.
func (w *Waits) Hold(n int) *Waits {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.hold = n
	return w
}

// Wait is the seam: it records d, and returns true at once unless stop is
// already closed. For a held wait, stop wins if it is ready when the wait
// chooses between stop and Release.
func (w *Waits) Wait(stop <-chan struct{}, d time.Duration) bool {
	w.mu.Lock()
	w.asked = append(w.asked, d)
	held := w.hold < 0 || len(w.asked) <= w.hold
	if held {
		w.holding++
		w.held.Broadcast()
	}
	w.mu.Unlock()
	if !held {
		select {
		case <-stop:
			return false
		default:
			return true
		}
	}
	defer func() {
		w.mu.Lock()
		w.holding--
		w.mu.Unlock()
	}()
	select {
	case <-stop:
		return false
	case <-w.release:
		select {
		case <-stop:
			return false
		default:
			return true
		}
	}
}

// Holding returns once at least n waits are held at the same moment.
func (w *Waits) Holding(n int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for w.holding < n {
		w.held.Wait()
	}
}

// Release ends every held wait with true, and lets every later wait through.
// It is called once.
func (w *Waits) Release() { close(w.release) }

// Asked is every d a wait was asked for, in the order asked.
func (w *Waits) Asked() []time.Duration {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]time.Duration(nil), w.asked...)
}
