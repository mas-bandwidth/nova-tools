package hostload

import (
	"context"
	"sync"
	"time"
)

// RingSize is how many one-second samples the ring holds: the ten seconds the
// fleet table's load cell shows the highest of.
const RingSize = 10

// Ring is the last RingSize samples of a busy percent, each capped at
// MaxPercent. The zero Ring is empty.
type Ring struct {
	v [RingSize]float64
	n uint64 // samples ever added
}

// Add puts one sample in, in place of the oldest once the ring is full.
func (r *Ring) Add(pct float64) {
	r.v[r.n%RingSize] = capped(pct)
	r.n++
}

// Count is how many samples were ever added.
func (r *Ring) Count() uint64 { return r.n }

// MaxSince is the highest of the samples added after the first count of them, at
// most the RingSize held; false when none was added since.
func (r *Ring) MaxSince(count uint64) (float64, bool) {
	if r.n <= count {
		return 0, false
	}
	k := min(r.n-count, RingSize)
	m := 0.0
	for i := r.n - k; i < r.n; i++ {
		m = max(m, r.v[i%RingSize])
	}
	return m, true
}

// Sampler takes the machine's busy percent into a Ring. Its readings are the Source's:
// ProcStat counters (the percent between two readings, once a second), else CPUSecond (a
// meter that takes its own second and costs a process, read once every MeterEvery and
// never while the caller says the seat is idle: SetIdle). Safe for use from the goroutine
// that runs it and the one that reads it.
type Sampler struct {
	src  Source
	mu   sync.Mutex
	prev *Ticks
	ring Ring
	idle bool // the caller's word that nothing runs on this seat (SetIdle)
	// now and after are Run's clock, nil for the wall's: a test's fake.
	now   func() time.Time
	after func(time.Duration) <-chan time.Time
}

// MeterEvery is how often Run reads a CPUSecond source (darwin's top, a process that runs
// for a second): one sample every RingSize seconds, so the ten seconds the fleet's load
// cell shows the highest of hold one. Read back to back, top cost a Mac seat 0.33 s of CPU
// every 1.35 s, about a quarter of a core around the clock and 44 spawns a minute
// (a Mac bench, 2026-10-17); a beat with no new sample carries Load1Percent, a free sysctl.
const MeterEvery = RingSize * time.Second

// SetIdle is the caller's word that nothing runs on this seat (idle) or that something
// does: while idle, Run reads no CPUSecond source at all, and the beat carries the
// one-minute load average (Load1Percent). A ProcStat source is a file read and is read
// whatever the word.
func (s *Sampler) SetIdle(idle bool) {
	s.mu.Lock()
	s.idle = idle
	s.mu.Unlock()
}

// Idle is the word SetIdle last gave; false until it is given.
func (s *Sampler) Idle() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.idle
}

// NewSampler is a Sampler over src with nothing sampled yet.
func NewSampler(src Source) *Sampler { return &Sampler{src: src} }

// Step takes one reading and, when it gives a percent, adds it to the ring and returns
// it. With CPUSecond it takes about a second; with ProcStat it returns at once and the
// percent is the busy share of the ticks since the last Step (false on the first).
func (s *Sampler) Step() (float64, bool) {
	var pct float64
	switch {
	case s.src.CPUSecond != nil:
		p, err := s.src.CPUSecond()
		if err != nil {
			return 0, false
		}
		pct = p
	case s.src.ProcStat != nil:
		text, err := s.src.ProcStat()
		if err != nil {
			return 0, false
		}
		cur, good := ParseProcStat(text)
		if !good {
			return 0, false
		}
		s.mu.Lock()
		prev := s.prev
		s.prev = &cur
		s.mu.Unlock()
		if prev == nil {
			return 0, false
		}
		if pct, good = BusyPercent(*prev, cur); !good {
			return 0, false
		}
	default:
		return 0, false
	}
	s.mu.Lock()
	s.ring.Add(pct)
	s.mu.Unlock()
	return capped(pct), true
}

// Peak is the highest sample added after the first since of them (at most the last
// RingSize), and how many samples there have been in all, which the caller passes as
// since next time once its beat is written; false when none is new.
func (s *Sampler) Peak(since uint64) (pct float64, total uint64, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	pct, ok = s.ring.MaxSince(since)
	return pct, s.ring.Count(), ok
}

// Load1Percent is the one-minute load average over the logical cores as a percent
// (LoadPercent; darwin: vm.loadavg and the core count, Linux: /proc/loadavg), the beat's
// load when the ring has no new sample: the first second of the member, or a source
// that gave none. false where the machine has no load average to read.
func (s *Sampler) Load1Percent() (float64, bool) {
	if s.src.Load1 == nil {
		return 0, false
	}
	l, ok := s.src.Load1()
	if !ok {
		return 0, false
	}
	return LoadPercent(l, s.src.NCPU), true
}

// Run steps until ctx ends: a ProcStat source once a second; a CPUSecond source once
// every MeterEvery, and not at all while the seat is idle (SetIdle), when it looks again
// each second for the word to change.
func (s *Sampler) Run(ctx context.Context) {
	if s.src.CPUSecond == nil && s.src.ProcStat == nil {
		return
	}
	now, after := s.now, s.after
	if now == nil {
		now = time.Now
	}
	if after == nil {
		after = time.After
	}
	for ctx.Err() == nil {
		began := now()
		pace := time.Second
		switch {
		case s.src.CPUSecond == nil:
			s.Step()
		case !s.Idle():
			s.Step()
			pace = MeterEvery
		}
		select {
		case <-ctx.Done():
		case <-after(pace - now().Sub(began)):
		}
	}
}
