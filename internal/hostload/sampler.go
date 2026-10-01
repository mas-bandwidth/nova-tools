package hostload

import (
	"context"
	"strconv"
	"strings"
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

// Max is the highest sample held, 0 when there is none.
func (r *Ring) Max() float64 {
	m, _ := r.MaxSince(0)
	return m
}

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

// Sampler takes the machine's busy percent once a second into a Ring. Its readings
// are the Source's: ProcStat counters (the percent between two readings), else
// CPUSecond (a meter that takes its own second). Safe for use from the goroutine that
// runs it and the one that reads it.
type Sampler struct {
	src  Source
	mu   sync.Mutex
	prev *Ticks
	ring Ring
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

// Run steps once a second until ctx ends. A source that takes its own second sets the
// pace; one that does not is waited on for the rest of the second.
func (s *Sampler) Run(ctx context.Context) {
	if s.src.CPUSecond == nil && s.src.ProcStat == nil {
		return
	}
	for ctx.Err() == nil {
		began := time.Now()
		s.Step()
		select {
		case <-ctx.Done():
		case <-time.After(time.Second - time.Since(began)):
		}
	}
}

// ParseIostat is the busy percent of the last line of darwin's `iostat -c 2 -w 1 -n 0`,
// 100 minus the id column of the second reading (the first is the average since boot,
// so one reading alone is not a second).
func ParseIostat(s string) (float64, bool) {
	var idle float64
	n := 0
	for _, line := range strings.Split(s, "\n") {
		id, ok := iostatIdle(line)
		if !ok {
			continue
		}
		idle = id
		n++
	}
	if n < 2 {
		return 0, false
	}
	return 100 - idle, true
}

// iostatIdle is the id column of one data row. iostat prints us, sy and id each
// %3.0f, so an idle of 100 runs into the column before it ("  0  0100") and the row
// does not split on spaces; the first nine characters are cut in threes, and a row
// that is not so laid out is split on spaces.
func iostatIdle(line string) (float64, bool) {
	cols := make([]string, 0, 3)
	if len(line) >= 9 {
		for i := 0; i < 9; i += 3 {
			cols = append(cols, strings.TrimSpace(line[i:i+3]))
		}
	}
	if !iostatCols(cols) {
		if cols = strings.Fields(line); len(cols) < 3 || !iostatCols(cols[:3]) {
			return 0, false
		}
	}
	id, _ := strconv.Atoi(cols[2])
	return float64(id), true
}

// iostatCols says three words are the us, sy and id of a row: whole percents, id at most 100.
func iostatCols(c []string) bool {
	if len(c) < 3 {
		return false
	}
	for _, w := range c[:3] {
		if v, err := strconv.Atoi(w); err != nil || v < 0 || v > 100 {
			return false
		}
	}
	return true
}
