package hostload

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSamplerCoverRunStepsUntilContextEnds: Sampler.Run steps once a pass until the
// context ends. The fake source ends the context on the first reading, so Run returns
// at once and the test waits on nothing: a CPUSecond meter's sample lands in the ring;
// a ProcStat reader's first reading has no interval, so it keeps the counters and adds
// no sample, and Run still ends.
func TestSamplerCoverRunStepsUntilContextEnds(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		source    func(cancel context.CancelFunc) Source
		wantPct   float64
		wantCount uint64
		wantOk    bool
	}{
		{
			name: "a cpu second meter adds its sample",
			source: func(cancel context.CancelFunc) Source {
				return Source{CPUSecond: func() (float64, error) {
					cancel()
					return 30, nil
				}}
			},
			wantPct: 30, wantCount: 1, wantOk: true,
		},
		{
			name: "a proc stat first reading adds nothing",
			source: func(cancel context.CancelFunc) Source {
				return Source{ProcStat: func() (string, error) {
					cancel()
					return procStat(1000, 1000), nil
				}}
			},
			wantPct: 0, wantCount: 0, wantOk: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			s := NewSampler(tc.source(cancel))
			s.Run(ctx)
			require.ErrorIs(t, ctx.Err(), context.Canceled, "the fake source ends the run through the context")
			pct, count, ok := s.Peak(0)
			assert.Equal(t, tc.wantCount, count, "samples the run added")
			assert.Equal(t, tc.wantOk, ok, "Peak reports a new sample")
			assert.Equal(t, tc.wantPct, pct, "the ring's highest")
		})
	}
}

// TestSamplerCoverRunRefusesWithoutAReading: Run takes no reading and adds no sample
// when the source has nothing it can step (neither CPUSecond nor ProcStat: a load
// average alone is Measure's fallback, not a sampler reading) or when the context has
// already ended before the first pass.
func TestSamplerCoverRunRefusesWithoutAReading(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		endFirst bool
		source   func(steps *int) Source
	}{
		{
			name:   "no reading at all",
			source: func(*int) Source { return Source{} },
		},
		{
			name: "only a load average",
			source: func(*int) Source {
				return Source{NCPU: 4, Load1: func() (float64, bool) { return 1, true }}
			},
		},
		{
			name:     "a context already ended steps nothing",
			endFirst: true,
			source: func(steps *int) Source {
				return Source{CPUSecond: func() (float64, error) {
					*steps++
					return 30, nil
				}}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			steps := 0
			s := NewSampler(tc.source(&steps))
			if tc.endFirst {
				cancel()
			}
			s.Run(ctx)
			assert.Equal(t, 0, steps, "a refused run takes no reading")
			_, count, ok := s.Peak(0)
			assert.Equal(t, uint64(0), count, "a refused run adds no sample")
			assert.False(t, ok, "with no sample Peak reports nothing")
		})
	}
}
