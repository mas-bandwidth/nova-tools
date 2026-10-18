package hostload

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// paceBench is a Sampler under a fake clock: the clock starts at 1000 s, a CPUSecond
// reading takes its one second of it, and every wait Run asks for is recorded and passes
// at once. The run ends after waits waits; nothing here waits on the wall.
type paceBench struct {
	clock time.Time
	reads int
	waits []time.Duration
}

func (b *paceBench) run(t *testing.T, src Source, idle bool, waits int) *Sampler {
	t.Helper()
	b.clock = time.Unix(1000, 0)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := NewSampler(src)
	s.SetIdle(idle)
	s.now = func() time.Time { return b.clock }
	s.after = func(d time.Duration) <-chan time.Time {
		b.waits = append(b.waits, d)
		b.clock = b.clock.Add(d)
		if len(b.waits) >= waits {
			cancel()
		}
		c := make(chan time.Time, 1)
		c <- b.clock
		return c
	}
	s.Run(ctx)
	return s
}

func (b *paceBench) meter() Source {
	return Source{CPUSecond: func() (float64, error) {
		b.reads++
		b.clock = b.clock.Add(time.Second) // top -l 2 -s 1 takes its second
		return 40, nil
	}}
}

// TestSamplerReadsTopOnceARingAndNeverWhileIdle (2026-10-17): darwin's meter is a top that
// runs for a second, and Run read it back to back forever, a quarter of a core of every
// Mac seat. While the seat is busy it is read once every MeterEvery (the read's own second
// counted in the pace); while the member says nothing runs it is not read at all, and the
// beat carries the load average; the word changing back starts the readings again.
func TestSamplerReadsTopOnceARingAndNeverWhileIdle(t *testing.T) {
	t.Parallel()

	busy := &paceBench{}
	s := busy.run(t, busy.meter(), false, 6)
	assert.Equal(t, 6, busy.reads, "one reading a pass")
	for i, w := range busy.waits {
		assert.Equal(t, MeterEvery-time.Second, w, "wait %d: the rest of MeterEvery after the read's own second", i)
	}
	assert.Equal(t, time.Unix(1000, 0).Add(6*MeterEvery), busy.clock, "six readings span six rings, not six seconds")
	pct, count, ok := s.Peak(0)
	require.True(t, ok)
	assert.Equal(t, uint64(6), count)
	assert.Equal(t, 40.0, pct)

	idle := &paceBench{}
	s = idle.run(t, idle.meter(), true, 30)
	assert.Zero(t, idle.reads, "an idle seat runs no top")
	for _, w := range idle.waits {
		assert.Equal(t, time.Second, w, "an idle seat looks for the word once a second")
	}
	_, _, ok = s.Peak(0)
	assert.False(t, ok, "no sample while idle: the beat takes Load1Percent")

	// the word changes mid-run: idle for three looks, then busy
	turn := &paceBench{}
	looks := 0
	var sampler *Sampler
	src := Source{CPUSecond: func() (float64, error) {
		turn.reads++
		turn.clock = turn.clock.Add(time.Second)
		return 70, nil
	}}
	turn.clock = time.Unix(1000, 0)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sampler = NewSampler(src)
	sampler.SetIdle(true)
	sampler.now = func() time.Time { return turn.clock }
	sampler.after = func(d time.Duration) <-chan time.Time {
		turn.waits = append(turn.waits, d)
		turn.clock = turn.clock.Add(d)
		if looks++; looks == 3 {
			sampler.SetIdle(false)
		}
		if looks == 5 {
			cancel()
		}
		c := make(chan time.Time, 1)
		c <- turn.clock
		return c
	}
	sampler.Run(ctx)
	assert.Equal(t, 2, turn.reads, "readings begin once the seat is busy")
	assert.Equal(t, []time.Duration{time.Second, time.Second, time.Second, MeterEvery - time.Second, MeterEvery - time.Second}, turn.waits)
}

// TestSamplerReadsProcStatEverySecondWhateverTheWord: a ProcStat source is a file read, and
// the idle word does not stop it.
func TestSamplerReadsProcStatEverySecondWhateverTheWord(t *testing.T) {
	t.Parallel()
	b := &paceBench{}
	busy := uint64(1000)
	s := b.run(t, Source{ProcStat: func() (string, error) {
		b.reads++
		busy += 50
		return procStat(busy, 1000), nil
	}}, true, 4)
	assert.Equal(t, 4, b.reads)
	for _, w := range b.waits {
		assert.Equal(t, time.Second, w)
	}
	_, count, ok := s.Peak(0)
	assert.True(t, ok)
	assert.Equal(t, uint64(3), count, "the first reading has no interval")
}
