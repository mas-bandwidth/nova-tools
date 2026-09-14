package wake

import (
	"context"
	"fmt"
	"time"
)

// A lock file released -- --lock, the amendment of 2026-09-13.
//
// --lock <path> names a file some other process holds an advisory lock on:
// nova-merge's lane lock, nova-bus's checkout lock, a swarm's. The state value
// is one of held, free or absent, read by opening the file (never creating it),
// attempting a non-blocking exclusive lock and, if it was granted, RELEASING IT
// AT ONCE. Any difference is a change, and held to free or held to absent is
// the one the caller was waiting for; the line carries both ends so a
// re-acquisition is visible too.
//
// THE PROBE NEVER HOLDS, NEVER WRITES, NEVER DELETES, NEVER CREATES (rule 16).
// The duration for which this tool owns the lock is the gap between two system
// calls. A blocking contender is granted the lock no later than one probe gap
// after the holder releases, because the probe takes a given path at most once
// per --interval and holds it for the next system call.
//
// The probe is a TryLock of its own beside internal/bus's lock and NOT
// LockFile's acquire: that one writes the holder's pid into the file it takes,
// and this source may not write a byte to a file another process owns.

// Locks is the lock source.
type Locks struct {
	Paths  []string
	Every_ time.Duration
	// Prev reads the stored value, so the line can carry both ends without
	// making an unchanged poll a change (forge.go, withWas).
	Prev func(key string) (string, bool)

	read, changed, unreadable int
}

func (l *Locks) Name() string         { return "locks" }
func (l *Locks) Every() time.Duration { return l.Every_ }

// Counts are the WAKE SOURCE locks line's. There is no calls= and no login=:
// the lock source starts nothing at all, and a login is the prs source's fact.
func (l *Locks) Counts() (read, changed, unreadable int) {
	return l.read, l.changed, l.unreadable
}

func (l *Locks) Poll(ctx context.Context, now time.Time) (Result, error) {
	res := Result{}
	bad := 0
	var reason string
	for _, path := range l.Paths {
		l.read++
		key := "lock:" + path
		state, err := probeLock(path)
		var value string
		if err != nil {
			bad++
			l.unreadable++
			reason = err.Error()
			value = Compose("unreadable", oneLineOf(err.Error()))
		} else {
			value = withWas(l.Prev, key, state)
		}
		if old, had := l.Prev(key); !had || old != value {
			l.changed++
		}
		res.Items = append(res.Items, Item{Kind: KindLock, Key: key, Value: value})
	}
	// A source fails when EVERY --lock path is unreadable -- and `absent` is a
	// STATE and never a failure, because a lock file that is not there is the
	// answer, not the absence of one.
	if len(l.Paths) > 0 && bad == len(l.Paths) {
		return res, fmt.Errorf("every lock path is unreadable: %s", oneLineOf(reason))
	}
	return res, nil
}
