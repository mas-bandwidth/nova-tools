package main

import (
	"context"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/presence"
)

// awake --store reaches no store: the verb takes its opener as an argument,
// so a test hands in a fake whose clock it moves. Nothing here waits for a
// wall-clock second.

var beatAt = time.Date(2026, 9, 22, 9, 41, 0, 0, time.UTC)

// fakeStoreClock is one clock for the verb and the store: the verb's sleeps
// move the store's expiry, so a 90s TTL lapse is a function call and not
// ninety seconds of test.
type fakeStoreClock struct{ st *presence.FakeStore }

func (c fakeStoreClock) Now() time.Time        { return c.st.Now() }
func (c fakeStoreClock) Sleep(d time.Duration) { c.st.Advance(d) }

func fakeOpener(st presence.Store) storeOpener {
	return func(ctx context.Context, addr, user string) (presence.Store, func() error, error) {
		return st, func() error { return nil }, nil
	}
}

// countingOpener wraps an opener and counts how many times it dials. A
// refused --store must never reach it: awake checks presence.Addr and prints
// its refusal before it ever calls open, so the count is the test's proof
// that a bad address never causes a network operation.
func countingOpener(st presence.Store, dials *int) storeOpener {
	inner := fakeOpener(st)
	return func(ctx context.Context, addr, user string) (presence.Store, func() error, error) {
		*dials++
		return inner(ctx, addr, user)
	}
}
