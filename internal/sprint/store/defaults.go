package store

import (
	"crypto/rand"
	"encoding/hex"
	mrand "math/rand/v2"
	"os"
	"strconv"
	"time"
)

// The retry budget of a read that other writers keep moving: the tables read
// again because one changed while it was read (Load), and the fence read
// again because another operation moved it (Fenced, and a step planned again
// after its fence was taken). A try after the first waits first; the wait is
// drawn at random between zero and a step that doubles from backoffBase up to
// backoffCap (full jitter), so writers that collided spread apart instead of
// colliding again. The tries stop at their count or when the next wait would
// pass RetryBudget of time asleep, whichever comes first.
const (
	LoadTries   = 12              // reads of the tables per load
	FenceTries  = 12              // reads of the fence per fenced read, and plans per step
	RetryBudget = 5 * time.Second // time asleep between the tries of one retry loop
	backoffBase = 10 * time.Millisecond
	backoffCap  = time.Second
)

// retry is one retry loop's tries, waits and the time it has slept.
type retry struct {
	st    *Store
	tries int
	waits int
	slept time.Duration
}

func (st *Store) retry() *retry { return &retry{st: st} }

// next is asked before every try: the first goes at once, a later one waits
// first. It is false, with nothing slept, when the tries are spent or the
// wait would pass the budget.
func (r *retry) next(tries int) bool {
	if r.tries >= tries || (r.tries > 0 && !r.wait()) {
		return false
	}
	r.tries++
	return true
}

// wait sleeps a jittered wait: a random time below a step that doubles with
// every wait of the loop, from backoffBase to backoffCap. It is false, with
// nothing slept, when the wait would pass the budget.
func (r *retry) wait() bool {
	step := backoffCap
	if r.waits < 30 && backoffBase<<r.waits < backoffCap {
		step = backoffBase << r.waits
	}
	d := time.Duration(r.st.jitter(int64(step)))
	if r.slept+d > RetryBudget {
		return false
	}
	r.waits++
	r.slept += d
	r.st.sleep(d)
	return true
}

// jitter is a number in [0, n): Store.Rand when set, else math/rand/v2.
func (st *Store) jitter(n int64) int64 {
	if n <= 0 {
		return 0
	}
	if st.Rand != nil {
		return st.Rand(n)
	}
	return mrand.Int64N(n)
}

// sleep is Store.Sleep when set, else time.Sleep: a store left without one
// still waits between its tries.
func (st *Store) sleep(d time.Duration) {
	if st.Sleep != nil {
		st.Sleep(d)
		return
	}
	time.Sleep(d)
}

// newID is Store.NewID when set, else NewID.
func (st *Store) newID() string {
	if st.NewID != nil {
		return st.NewID()
	}
	return NewID()
}

// NewID is a fresh operation id family, unique across processes and across
// restarts of one: the time to the nanosecond, the process id and eight
// random bytes, in letters, digits and '-'.
func NewID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return strconv.FormatInt(time.Now().UnixNano(), 36) + "-" + strconv.Itoa(os.Getpid()) + "-" + hex.EncodeToString(b[:])
}
