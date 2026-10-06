package sprint

import (
	"context"
	"fmt"
	"time"
)

// AlarmJudgment represents an alarm judgment.
type AlarmJudgment struct {
	Median  time.Duration
	Reason  string
}

// Pinger interface for RTT measurement.
type Pinger interface {
	Ping(context.Context) (time.Duration, error)
}

// fakePinger simulates RTT measurements with injected delays.
type fakePinger struct {
	rtts []time.Duration
	idx  int
}

func (f *fakePinger) Ping(_ context.Context) (time.Duration, error) {
	if len(f.rtts) == 0 {
		return 0, nil
	}
	if f.idx >= len(f.rtts) {
		return f.rtts[len(f.rtts)-1], nil
	}
	d := f.rtts[f.idx]
	f.idx++
	return d, nil
}

func (f *fakePinger) Reset() {
	f.idx = 0
}

// Clock interface for time.
type Clock interface {
	Now() time.Time
}

// fakeClock provides deterministic time.
type fakeClock struct {
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	return c.now
}

// storeRTTMeter measures store round-trip time and tracks alarm state.
type storeRTTMeter struct {
	rtts      []time.Duration
	median    time.Duration
	alarm     bool
	episode   bool
	pinger    Pinger
	clock     Clock
	threshold time.Duration
}

func newStoreRTTMeter(pinger Pinger, clock Clock, threshold time.Duration) *storeRTTMeter {
	return &storeRTTMeter{
		rtts:      make([]time.Duration, 0, 20),
		pinger:    pinger,
		clock:     clock,
		threshold: threshold,
	}
}

func (m *storeRTTMeter) reset() {
	m.rtts = m.rtts[:0]
	m.alarm = false
	m.episode = false
}

// measure performs 20 pings and computes the median.
func (m *storeRTTMeter) measure(ctx context.Context) (time.Duration, error) {
	m.rtts = m.rtts[:0]
	for i := 0; i < 20; i++ {
		rtt, err := m.pinger.Ping(ctx)
		if err != nil {
			return 0, err
		}
		m.rtts = append(m.rtts, rtt)
	}
	m.median = median(m.rtts)
	return m.median, nil
}

func median(d []time.Duration) time.Duration {
	if len(d) == 0 {
		return 0
	}
	// Sort (simple bubble sort for small slice)
	for i := 0; i < len(d); i++ {
		for j := i + 1; j < len(d); j++ {
			if d[i] > d[j] {
				d[i], d[j] = d[j], d[i]
			}
		}
	}
	return d[len(d)/2]
}

// Tick advances the alarm state based on current median.
func (m *storeRTTMeter) Tick(ctx context.Context) (raised bool, median time.Duration, err error) {
	median, err = m.measure(ctx)
	if err != nil {
		return false, 0, err
	}
	// Check if we should be in alarm mode
	if median > m.threshold {
		if !m.alarm {
			m.alarm = true
			m.episode = true
			raised = true
		}
	} else if median < m.threshold/2 && m.episode {
		// End episode
		m.episode = false
		m.alarm = false
	}
	return raised, median, nil
}

// StepsTick manages the tick-based alarm logic.
type StepsTick struct {
	rttMeter *storeRTTMeter
}

// NewStepsTick creates a new steps tick manager.
func NewStepsTick(pinger Pinger, clock Clock, threshold time.Duration) *StepsTick {
	return &StepsTick{
		rttMeter: newStoreRTTMeter(pinger, clock, threshold),
	}
}

// Tick advances the alarm state and returns any judgments raised.
func (s *StepsTick) Tick(ctx context.Context) ([]AlarmJudgment, time.Duration, error) {
	raised, median, err := s.rttMeter.Tick(ctx)
	if err != nil {
		return nil, 0, err
	}

	var judgments []AlarmJudgment
	if raised {
		judgments = append(judgments, AlarmJudgment{
			Median: median,
			Reason: fmt.Sprintf("store is slow: %d ms", median/time.Millisecond),
		})
	}

	return judgments, median, nil
}

// IsEpisodeActive returns whether an alarm episode is currently active.
func (s *StepsTick) IsEpisodeActive() bool {
	return s.rttMeter.episode
}

// Reset resets the alarm state.
func (s *StepsTick) Reset() {
	s.rttMeter.reset()
}
