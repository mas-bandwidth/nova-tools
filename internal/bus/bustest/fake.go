package bustest

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
)

// Fake is the Store in memory: the streams, the groups with their pending
// entries, the roster and a clock that moves one second per Roster
// call, so no test reads the real clock. It refuses what Redis refuses
// (a read of a group that is not there) and keeps the same idle-free claim
// order. A test makes its own with NewFake; nothing is shared.
type Fake struct {
	mu      sync.Mutex
	names   []string
	now     time.Time
	streams map[string][]bus.Entry
	groups  map[string]*fakeGroup // stream + "/" + group
	seq     int64
	hashes  map[string]map[string]string
	// Friends is which names of the roster are friends (the set `friends`);
	// the rest are machines. A test sets it before the first send.
	Friends []string
	// Fail, when set, is the error every command answers: a store that is down.
	Fail error
	// Trips counts the commands sent.
	Trips int
}

var _ bus.Store = (*Fake)(nil)

type fakeGroup struct {
	last    string               // last delivered entry id, "0-0" at the start
	pending map[string]time.Time // entry id -> when it was last delivered
}

// NewFake is an empty store at the instant start whose roster is names.
func NewFake(start time.Time, names ...string) *Fake {
	return &Fake{names: names, now: start, streams: map[string][]bus.Entry{}, groups: map[string]*fakeGroup{}}
}

func (f *Fake) trip() error {
	f.Trips++
	return f.Fail
}

// Advance moves the clock by d: what a reader's idle time grows by.
func (f *Fake) Advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = f.now.Add(d)
}

// Len is how many entries the stream holds.
func (f *Fake) Len(stream string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.streams[stream])
}

func (f *Fake) Roster(context.Context) ([]string, time.Time, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.trip(); err != nil {
		return nil, time.Time{}, err
	}
	f.now = f.now.Add(time.Second)
	return slices.Clone(f.names), f.now, nil
}

func (f *Fake) Members(context.Context) ([]string, []string, time.Time, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.trip(); err != nil {
		return nil, nil, time.Time{}, err
	}
	f.now = f.now.Add(time.Second)
	var friends, machines []string
	for _, n := range f.names {
		if slices.Contains(f.Friends, n) {
			friends = append(friends, n)
		} else {
			machines = append(machines, n)
		}
	}
	return friends, machines, f.now, nil
}

func (f *Fake) AddAll(_ context.Context, streams []string, fields map[string]string, marks ...bus.Mark) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.trip(); err != nil {
		return err
	}
	f.seq++
	id := strconv.FormatInt(f.now.UnixMilli(), 10) + "-" + strconv.FormatInt(f.seq, 10)
	for _, s := range streams {
		f.streams[s] = append(f.streams[s], bus.Entry{Stream: s, Entry: id, Fields: maps.Clone(fields)})
	}
	for _, m := range marks {
		if m.Forward != "" {
			f.forward(m.Key, m.Forward, m.Field)
			continue
		}
		if m.Clear {
			delete(f.hashes[m.Key], m.Field)
			continue
		}
		if f.hashes == nil {
			f.hashes = map[string]map[string]string{}
		}
		if f.hashes[m.Key] == nil {
			f.hashes[m.Key] = map[string]string{}
		}
		f.hashes[m.Key][m.Field] = m.Value
	}
	return nil
}

func (f *Fake) Unmark(_ context.Context, key string, fields ...string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.trip(); err != nil {
		return 0, err
	}
	var n int64
	for _, k := range fields {
		if _, ok := f.hashes[key][k]; ok {
			delete(f.hashes[key], k)
			n++
		}
	}
	return n, nil
}

func (f *Fake) AdvanceReceipts(_ context.Context, key, state string, ids ...string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.trip(); err != nil {
		return 0, err
	}
	return f.forward(key, state, ids...), nil
}

// forward moves each id's receipt in the hash at key to state when bus.Forward
// says so, at the clock's now, and counts those that moved; the caller holds
// the lock.
func (f *Fake) forward(key, state string, ids ...string) int64 {
	var n int64
	for _, id := range ids {
		cur, _, _ := bus.ParseReceiptValue(f.hashes[key][id]) // ignored: a value that is no receipt is a field not there
		if !bus.Forward(cur, state) {
			continue
		}
		if f.hashes == nil {
			f.hashes = map[string]map[string]string{}
		}
		if f.hashes[key] == nil {
			f.hashes[key] = map[string]string{}
		}
		f.hashes[key][id] = bus.ReceiptValue(state, f.now)
		n++
	}
	return n
}

func (f *Fake) Marks(_ context.Context, keys ...string) ([]map[string]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.trip(); err != nil {
		return nil, err
	}
	out := make([]map[string]string, len(keys))
	for i, k := range keys {
		out[i] = maps.Clone(f.hashes[k])
		if out[i] == nil {
			out[i] = map[string]string{}
		}
	}
	return out, nil
}

func (f *Fake) EnsureGroup(_ context.Context, stream, group string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.trip(); err != nil {
		return err
	}
	if _, ok := f.groups[stream+"/"+group]; !ok {
		f.groups[stream+"/"+group] = &fakeGroup{last: "0-0", pending: map[string]time.Time{}}
		if _, ok := f.streams[stream]; !ok {
			f.streams[stream] = nil
		}
	}
	return nil
}

func (f *Fake) group(stream, group string) (*fakeGroup, error) {
	g, ok := f.groups[stream+"/"+group]
	if !ok {
		return nil, fmt.Errorf("NOGROUP No such key '%s' or consumer group '%s'", stream, group)
	}
	return g, nil
}

func (f *Fake) Claim(_ context.Context, stream, group, _ string, minIdle time.Duration, count int) ([]bus.Entry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.trip(); err != nil {
		return nil, err
	}
	g, err := f.group(stream, group)
	if err != nil {
		return nil, err
	}
	var out []bus.Entry
	for _, e := range f.streams[stream] { // stream order is id order
		if at, pending := g.pending[e.Entry]; pending && f.now.Sub(at) >= minIdle && len(out) < count {
			g.pending[e.Entry] = f.now
			out = append(out, e)
		}
	}
	return out, nil
}

func (f *Fake) Read(_ context.Context, stream, group, _ string, _ time.Duration, count int) ([]bus.Entry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.trip(); err != nil {
		return nil, err
	}
	g, err := f.group(stream, group)
	if err != nil {
		return nil, err
	}
	var out []bus.Entry
	for _, e := range f.streams[stream] {
		if after(e.Entry, g.last) && len(out) < count {
			g.pending[e.Entry] = f.now
			g.last = e.Entry
			out = append(out, e)
		}
	}
	return out, nil
}

func (f *Fake) Release(_ context.Context, stream, group string, entries ...string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.trip(); err != nil {
		return err
	}
	g, err := f.group(stream, group)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if _, pending := g.pending[e]; pending {
			g.pending[e] = f.now.Add(-bus.ClaimAfter)
		}
	}
	return nil
}

func (f *Fake) Ack(_ context.Context, stream, group string, entries ...string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.trip(); err != nil {
		return 0, err
	}
	g, ok := f.groups[stream+"/"+group]
	if !ok {
		return 0, nil // XACK of a missing group is 0, as Redis answers
	}
	var n int64
	for _, e := range entries {
		if _, pending := g.pending[e]; pending {
			delete(g.pending, e)
			n++
		}
	}
	return n, nil
}

func (f *Fake) Pending(_ context.Context, stream, group string, count int) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.trip(); err != nil {
		return nil, err
	}
	g, err := f.group(stream, group)
	if err != nil {
		return nil, err
	}
	ids := slices.Collect(maps.Keys(g.pending))
	sort.Slice(ids, func(i, j int) bool { return after(ids[j], ids[i]) })
	if len(ids) > count {
		ids = ids[:count]
	}
	return ids, nil
}

func (f *Fake) Group(_ context.Context, stream, group string) (string, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.trip(); err != nil {
		return "", false, err
	}
	g, ok := f.groups[stream+"/"+group]
	if !ok {
		return "", false, nil
	}
	return g.last, true, nil
}

func (f *Fake) Range(_ context.Context, stream, from, to string, count int) ([]bus.Entry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.trip(); err != nil {
		return nil, err
	}
	var out []bus.Entry
	for _, e := range f.streams[stream] {
		if inRange(e.Entry, from, to) && (count <= 0 || len(out) < count) {
			out = append(out, e)
		}
	}
	return out, nil
}

func (f *Fake) Get(_ context.Context, stream string, entries []string) ([]bus.Entry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.trip(); err != nil {
		return nil, err
	}
	var out []bus.Entry
	for _, e := range f.streams[stream] {
		if slices.Contains(entries, e.Entry) {
			out = append(out, e)
		}
	}
	return out, nil
}

// after reports whether entry id a is greater than b (<ms>-<seq>, numerically).
func after(a, b string) bool {
	num := func(id string) (int64, int64) {
		ms, seq, _ := strings.Cut(id, "-")
		m, _ := strconv.ParseInt(ms, 10, 64)  // ignored: a fake id is one this file made, and a range end that is no id reads as 0
		s, _ := strconv.ParseInt(seq, 10, 64) // ignored: as above
		return m, s
	}
	am, as := num(a)
	bm, bs := num(b)
	return am > bm || (am == bm && as > bs)
}

// inRange is XRANGE's test: "-" and "+" are the ends, "(<id>" is exclusive,
// and a bare "<ms>-<seq>" or "<ms>" is inclusive.
func inRange(id, from, to string) bool {
	if from != "-" {
		if excl, ok := strings.CutPrefix(from, "("); ok {
			if !after(id, excl) {
				return false
			}
		} else if after(from, id) {
			return false
		}
	}
	if to != "+" && after(id, to) {
		return false
	}
	return true
}
