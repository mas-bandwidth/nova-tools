package bus2

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
	streams map[string][]Entry
	groups  map[string]*fakeGroup // stream + "/" + group
	seq     int64
	// Fail, when set, is the error every command answers: a store that is down.
	Fail error
	// Trips counts the commands sent.
	Trips int
}

type fakeGroup struct {
	last    string               // last delivered entry id, "0-0" at the start
	pending map[string]time.Time // entry id -> when it was last delivered
	owner   map[string]string
}

// NewFake is an empty store at the instant start whose roster is names.
func NewFake(start time.Time, names ...string) *Fake {
	return &Fake{names: names, now: start, streams: map[string][]Entry{}, groups: map[string]*fakeGroup{}}
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

func (f *Fake) AddAll(_ context.Context, streams []string, fields map[string]string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.trip(); err != nil {
		return err
	}
	f.seq++
	id := strconv.FormatInt(f.now.UnixMilli(), 10) + "-" + strconv.FormatInt(f.seq, 10)
	for _, s := range streams {
		f.streams[s] = append(f.streams[s], Entry{Stream: s, Entry: id, Fields: maps.Clone(fields)})
	}
	return nil
}

func (f *Fake) EnsureGroup(_ context.Context, stream, group string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.trip(); err != nil {
		return err
	}
	if _, ok := f.groups[stream+"/"+group]; !ok {
		f.groups[stream+"/"+group] = &fakeGroup{last: "0-0", pending: map[string]time.Time{}, owner: map[string]string{}}
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

func (f *Fake) Claim(_ context.Context, stream, group, consumer string, minIdle time.Duration, count int) ([]Entry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.trip(); err != nil {
		return nil, err
	}
	g, err := f.group(stream, group)
	if err != nil {
		return nil, err
	}
	var out []Entry
	for _, e := range f.streams[stream] { // stream order is id order
		if at, pending := g.pending[e.Entry]; pending && f.now.Sub(at) >= minIdle && len(out) < count {
			g.pending[e.Entry] = f.now
			g.owner[e.Entry] = consumer
			out = append(out, e)
		}
	}
	return out, nil
}

func (f *Fake) Read(_ context.Context, stream, group, consumer string, _ time.Duration, count int) ([]Entry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.trip(); err != nil {
		return nil, err
	}
	g, err := f.group(stream, group)
	if err != nil {
		return nil, err
	}
	var out []Entry
	for _, e := range f.streams[stream] {
		if after(e.Entry, g.last) && len(out) < count {
			g.pending[e.Entry] = f.now
			g.owner[e.Entry] = consumer
			g.last = e.Entry
			out = append(out, e)
		}
	}
	return out, nil
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
			delete(g.owner, e)
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

func (f *Fake) Range(_ context.Context, stream, from, to string, count int) ([]Entry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.trip(); err != nil {
		return nil, err
	}
	var out []Entry
	for _, e := range f.streams[stream] {
		if inRange(e.Entry, from, to) && (count <= 0 || len(out) < count) {
			out = append(out, e)
		}
	}
	return out, nil
}

func (f *Fake) Get(_ context.Context, stream string, entries []string) ([]Entry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.trip(); err != nil {
		return nil, err
	}
	var out []Entry
	for _, e := range f.streams[stream] {
		if slices.Contains(entries, e.Entry) {
			out = append(out, e)
		}
	}
	return out, nil
}

// after reports whether entry id a is greater than b (<ms>-<seq>, numerically).
func after(a, b string) bool {
	am, as := split(a)
	bm, bs := split(b)
	return am > bm || (am == bm && as > bs)
}

func split(id string) (int64, int64) {
	ms, seq, _ := strings.Cut(id, "-")
	m, _ := strconv.ParseInt(ms, 10, 64)  // ignored: a fake id is always one this file made, or "-"/"+"
	s, _ := strconv.ParseInt(seq, 10, 64) // ignored: as above
	return m, s
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

// PendingPage follows XPENDING's numeric ID order and consumer filter.
func (f *Fake) PendingPage(_ context.Context, stream, group, consumer, cursor string, count int) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.trip(); err != nil {
		return nil, err
	}
	g, err := f.group(stream, group)
	if err != nil {
		return nil, err
	}
	var ids []string
	for id := range g.pending {
		if (consumer == "" || g.owner[id] == consumer) && (cursor == "" || after(id, cursor)) {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return after(ids[j], ids[i]) })
	if len(ids) > count {
		ids = ids[:count]
	}
	return ids, nil
}
