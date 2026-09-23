// Package state is the fleet-state verb's one decision: a bench is UP, DOWN or
// HELD by its heartbeat key and that key's TTL.
//
// THE KEY IS THE BENCH'S OWN WORD THAT IT IS THERE (SPEC-STATE ## Presence --
// keys with TTL: the key expires on its own and the beat renews it). The state
// follows the key and nothing else. That is the whole of the #2161 repair: a
// bench at load 21 on 16 cores used to read DOWN, because everything asked of
// it timed out under the load it was carrying, while it was writing its
// heartbeat key the entire time. Load is a fact on the row beside the state and
// is NEVER a say in it (the same law as the fill's brake, pulse/fillstore.go:
// the load only brakes placement, it never shrinks a lease). So:
//
//	UP   -- the key is within its TTL and carries no hold: the bench is there.
//	HELD -- the key is within its TTL and carries a hold: the bench is there
//	        and taking no work. A hold rides the key, so it expires with it:
//	        a hold is a state of a bench that is there, never a tombstone.
//	DOWN -- the key has expired (or was never written): no word from the bench
//	        within its own TTL.
//
// The TTL is per key, written with it: one beat may promise a minute, the beat
// before a planned sleep may promise an hour. A bench whose key expires is DOWN
// within TTL+5 s -- the decision is at the TTL itself, so any observer is
// inside the allowance.
//
// THE PAGE IS NEVER AUTHORITY. `nova-sprint fleet-state` prints one Row per
// bench and `nova-sprint fleet-live` lists Live over the same decision, both
// read from the fleet store by ReadRedis (redis.go). A page made from those
// lines (rowan-tools' queue/control/fleet-state.tsv) is never read back by
// anything in this tree (TestNoReaderOfFleetStateTSV holds the grep). A
// projection read back is a snapshot pretending to be the present -- the keys
// are the present.
package state

import (
	"time"
)

// State is what fleet-state says of one bench.
type State string

const (
	// Up says the bench is there: its heartbeat key is within its TTL.
	Up State = "UP"
	// Held says the bench is there and taking no work: a hold rode its key and
	// the key still counts.
	Held State = "HELD"
	// Down says the bench has not been heard from within its key's TTL.
	Down State = "DOWN"
)

// Key is one bench's heartbeat key: when it was last written, how long one
// write counts, and whether that write carried a hold. The TTL is the life of
// one write, not a property of the bench -- the bench chooses it per beat and
// renews by writing again. A write with no TTL expires at once.
type Key struct {
	Written time.Time
	TTL     time.Duration
	Held    bool
}

// Expired says whether the key no longer counts at now: the TTL is how long one
// write stands, so a key is expired at Written+TTL and after.
func (k Key) Expired(now time.Time) bool {
	return !now.Before(k.Written.Add(k.TTL))
}

// Bench is one machine as the fleet-state rows carry it. Load and Cores are the
// bench's load facts, kept beside the key so one caller's readings travel
// together -- and kept out of the decision entirely (the #2161 case: load 21 on
// 16 cores that still writes its heartbeat key is UP).
type Bench struct {
	Name  string
	Load  float64
	Cores int
	// Key is the bench's heartbeat key. Nil means the bench has never written
	// one, which is as down as one that expired: a bench that has said nothing
	// is not up.
	Key *Key
}

// State decides one bench's state at now, by its heartbeat key and that key's
// TTL. This is the production path both verbs reach: Live filters on it and Row
// renders it.
func (b Bench) State(now time.Time) State {
	if b.Key == nil || b.Key.Expired(now) {
		return Down
	}
	if b.Key.Held {
		return Held
	}
	return Up
}

// Live is what `nova-sprint fleet-live` lists: the names whose heartbeat key still
// counts, in the order given. It answers presence, not eligibility -- a HELD
// bench is here (that is what held means), and a DOWN bench's key has expired
// and it is not. Whether a bench may take work is State == Up, and that is the
// admission owner's question, not this one.
func Live(benches []Bench, now time.Time) []string {
	var out []string
	for _, b := range benches {
		if b.State(now) != Down {
			out = append(out, b.Name)
		}
	}
	return out
}

// Row is one bench's row as `nova-sprint fleet-state` prints it:
// `name<TAB>state`. It reads no file and writes none -- it renders the one line
// the page is built from, and the page is never read back.
func Row(b Bench, now time.Time) string {
	return b.Name + "\t" + string(b.State(now))
}
