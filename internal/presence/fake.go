package presence

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// FakeStore is the store the tests beat against: a map with real expiry, driven
// by a clock the test moves. It is strict where the real one is -- a key whose
// TTL has passed is gone, an MGet over a key nobody wrote answers "", and a
// store told to fail fails -- because a lenient fake would let a heartbeat that
// never expires look correct here and report a friend who left as up.
type FakeStore struct {
	mu   sync.Mutex
	now  time.Time
	vals map[string]fakeVal
	// Err, when set, is what every call answers. It is how a test sees
	// what a beat does when the store is unreachable.
	Err error
	// Hook, when set, is asked before every call what that call should
	// answer, by 1-based call number; a nil answer lets the call through.
	// It is how a test makes a store blink for a few seconds and then come
	// back, inside one beat loop, without waiting for one.
	Hook func(call int) error
	// Calls counts every call, failed ones too; Sets counts the writes that
	// landed, so a test can say how many beats the store actually took.
	Calls int
	Sets  int
}

// fail is the answer this call should give, Hook first and the blanket Err
// after it.
func (f *FakeStore) fail() error {
	f.Calls++
	if f.Hook != nil {
		if err := f.Hook(f.Calls); err != nil {
			return err
		}
	}
	return f.Err
}

type fakeVal struct {
	val   string
	until time.Time // zero: no expiry
}

// NewFakeStore starts a fake at the given moment.
func NewFakeStore(now time.Time) *FakeStore {
	return &FakeStore{now: now.UTC(), vals: map[string]fakeVal{}}
}

// Now is the fake's clock, and Advance moves it. Keys expire against it, so
// `Advance(91 * time.Second)` is a friend's window that went away.
func (f *FakeStore) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

func (f *FakeStore) Advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = f.now.Add(d)
}

// Set writes value at key, with an expiry when ttl is positive.
func (f *FakeStore) Set(ctx context.Context, key, value string, ttl time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail(); err != nil {
		return err
	}
	if ttl < 0 {
		return fmt.Errorf("negative ttl")
	}
	v := fakeVal{val: value}
	if ttl > 0 {
		v.until = f.now.Add(ttl)
	}
	f.vals[key] = v
	f.Sets++
	return nil
}

// MGet answers one value per key, "" for a key that is absent or has expired.
func (f *FakeStore) MGet(ctx context.Context, keys ...string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail(); err != nil {
		return nil, err
	}
	out := make([]string, len(keys))
	for i, k := range keys {
		v, ok := f.vals[k]
		if !ok {
			continue
		}
		if !v.until.IsZero() && !f.now.Before(v.until) {
			delete(f.vals, k)
			continue
		}
		out[i] = v.val
	}
	return out, nil
}
