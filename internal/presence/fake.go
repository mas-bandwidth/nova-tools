package presence

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"
)

// FakeStore is the store the tests beat against: hashes, strings and sets with
// real expiry, driven by a clock the test moves. It is strict where the real
// one is -- a hash whose TTL has passed is gone, a read of a key nobody wrote
// answers "", a hash with no TTL is not live, and a store told to fail fails --
// because a lenient fake would let a heartbeat that never expires look correct
// here and report a friend who left as up.
type FakeStore struct {
	mu      sync.Mutex
	now     time.Time
	hashes  map[string]fakeHash
	strings map[string]string
	sets    map[string]map[string]bool
	// Err, when set, is what every call answers. It is how a test sees
	// what a beat does when the store is unreachable.
	Err error
	// Hook, when set, is asked before every call what that call should
	// answer, by 1-based call number; a nil answer lets the call through.
	// It is how a test makes a store blink for a few seconds and then come
	// back, inside one beat loop, without waiting for one.
	Hook func(call int) error
	// Calls counts every call, failed ones too; Sets counts the beats that
	// landed (one WriteBeat is one write), so a test can say how many beats
	// the store actually took.
	Calls int
	Sets  int
}

type fakeHash struct {
	fields map[string]string
	until  time.Time // zero: no expiry
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

// NewFakeStore starts a fake at the given moment.
func NewFakeStore(now time.Time) *FakeStore {
	return &FakeStore{
		now:     now.UTC(),
		hashes:  map[string]fakeHash{},
		strings: map[string]string{},
		sets:    map[string]map[string]bool{},
	}
}

// Now is the fake's clock, and Advance moves it. Hashes expire against it, so
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

// hash is the live hash at key, expiring it first; the caller holds mu.
func (f *FakeStore) hash(key string) (fakeHash, bool) {
	h, ok := f.hashes[key]
	if !ok {
		return fakeHash{}, false
	}
	if !h.until.IsZero() && !f.now.Before(h.until) {
		delete(f.hashes, key)
		return fakeHash{}, false
	}
	return h, true
}

// WriteBeat is HSET key fields..., PEXPIRE key ttl and SET lastKey stamp, all
// or nothing; the friend row (a hash with the up field) or a set at key is
// refused with a *KeyTypeError and nothing is written, as the real store does.
func (f *FakeStore) WriteBeat(ctx context.Context, key string, fields []string, ttl time.Duration, lastKey, stamp string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail(); err != nil {
		return err
	}
	if len(fields) == 0 || len(fields)%2 != 0 {
		return fmt.Errorf("beat fields must be field, value pairs")
	}
	if ttl <= 0 {
		return fmt.Errorf("non-positive ttl")
	}
	if _, ok := f.sets[key]; ok {
		return &KeyTypeError{Key: key, Type: "set"}
	}
	h, ok := f.hash(key)
	if ok {
		if _, row := h.fields[FieldUp]; row {
			return &KeyTypeError{Key: key, Type: "hash", Row: true}
		}
	} else {
		h = fakeHash{fields: map[string]string{}}
	}
	// A plain string at key is a beat older than #2673: the beat's own,
	// replaced in the same write.
	delete(f.strings, key)
	for i := 0; i < len(fields); i += 2 {
		h.fields[fields[i]] = fields[i+1]
	}
	h.until = f.now.Add(ttl)
	f.hashes[key] = h
	f.strings[lastKey] = stamp
	f.Sets++
	return nil
}

// ReadBeats answers one Reading per key: the three fields, the row's up field
// and whether the hash carries it, whether a TTL is running on the hash, and
// the untimed :last string.
func (f *FakeStore) ReadBeats(ctx context.Context, keys []string) ([]Reading, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail(); err != nil {
		return nil, err
	}
	out := make([]Reading, len(keys))
	for i, k := range keys {
		if h, ok := f.hash(k); ok {
			up, row := h.fields[FieldUp]
			out[i] = Reading{At: h.fields[FieldAt], Width: h.fields[FieldWidth], Window: h.fields[FieldWindow], Up: up, Row: row, Live: !h.until.IsZero()}
		}
		out[i].Last = f.strings[k+LastSuffix]
	}
	return out, nil
}

// Members answers the set's members, sorted.
func (f *FakeStore) Members(ctx context.Context, set string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail(); err != nil {
		return nil, err
	}
	var out []string
	for m := range f.sets[set] {
		out = append(out, m)
	}
	sort.Strings(out)
	return out, nil
}

// The helpers below are the tests' hands and eyes on the fake. They are not
// Store calls: they do not count, and they never fail.

// AddMembers is SADD set members...: how a test registers friends.
func (f *FakeStore) AddMembers(set string, members ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.sets[set] == nil {
		f.sets[set] = map[string]bool{}
	}
	for _, m := range members {
		f.sets[set][m] = true
	}
}

// SetHash is HSET key fields... with a TTL, or none when ttl is 0: a table
// row with no TTL, or a beat whose at is not a stamp.
func (f *FakeStore) SetHash(key string, ttl time.Duration, fields ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	h, ok := f.hash(key)
	if !ok {
		h = fakeHash{fields: map[string]string{}}
	}
	for i := 0; i+1 < len(fields); i += 2 {
		h.fields[fields[i]] = fields[i+1]
	}
	if ttl > 0 {
		h.until = f.now.Add(ttl)
	}
	f.hashes[key] = h
}

// SetString is SET key value with no expiry: how a test writes a :last alone.
func (f *FakeStore) SetString(key, value string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.strings[key] = value
}

// Hash is HGETALL key: nil when the hash is absent or has expired.
func (f *FakeStore) Hash(key string) map[string]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	h, ok := f.hash(key)
	if !ok {
		return nil
	}
	out := make(map[string]string, len(h.fields))
	for k, v := range h.fields {
		out[k] = v
	}
	return out
}

// TTL is PTTL key as a duration: 0 when the hash is absent, expired or has
// no expiry.
func (f *FakeStore) TTL(key string) time.Duration {
	f.mu.Lock()
	defer f.mu.Unlock()
	h, ok := f.hash(key)
	if !ok || h.until.IsZero() {
		return 0
	}
	return h.until.Sub(f.now)
}

// String is GET key: "" when absent.
func (f *FakeStore) String(key string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.strings[key]
}
