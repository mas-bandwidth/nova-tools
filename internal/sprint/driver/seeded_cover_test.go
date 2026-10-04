package driver

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// TestSeededCoverDrawnIsTheChancesTheSourceHolds pins Drawn: it is the six
// chances the source draws against, with Down and Up as the per-tick chances
// Use computed, and Up taken from the source's Back field. Drawn has no
// refusal path, so its one main path is what a case covers.
func TestSeededCoverDrawnIsTheChancesTheSourceHolds(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name  string
		use   *Chances
		every time.Duration
		want  Chances
	}{
		{
			name: "a source given no chances draws against none",
			want: Chances{},
		},
		{
			name:  "a tick of a second leaves every chance as it was",
			use:   &Chances{Broken: 0.05, Fail: 0.10, Stuck: 0.10, Cross: 0.01, Down: 0.02, Up: 0.30},
			every: time.Second,
			want:  Chances{Broken: 0.05, Fail: 0.10, Stuck: 0.10, Cross: 0.01, Down: 0.02, Up: 0.30},
		},
		{
			name:  "a tick of no time leaves every chance as it was",
			use:   &Chances{Broken: 0.05, Fail: 0.10, Stuck: 0.10, Cross: 0.01, Down: 0.02, Up: 0.30},
			every: 0,
			want:  Chances{Broken: 0.05, Fail: 0.10, Stuck: 0.10, Cross: 0.01, Down: 0.02, Up: 0.30},
		},
		{
			name:  "a tick of a minute folds Down and Up by PerTick",
			use:   &Chances{Broken: 0.20, Fail: 0.30, Stuck: 0.40, Cross: 0.04, Down: 0.01, Up: 0.10},
			every: time.Minute,
			want:  Chances{Broken: 0.20, Fail: 0.30, Stuck: 0.40, Cross: 0.04, Down: PerTick(0.01, time.Minute), Up: PerTick(0.10, time.Minute)},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			s := NewSeeded(1)
			if c.use != nil {
				s.Use(*c.use, c.every)
			}
			got := s.Drawn()
			assert.Equal(t, c.want, got, "Drawn: %+v\nwant %+v", got, c.want)
			assert.Equal(t, c.want.Up, got.Up, "Up is drawn from the source's Back, not its Down")
		})
	}
}
