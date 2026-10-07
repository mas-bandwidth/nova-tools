package sprint

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestETARateStatesTheWindowAndLandingSample(t *testing.T) {
	t.Parallel()
	now := t0.Add(3 * time.Hour)
	for _, tc := range []struct {
		name   string
		stamps []time.Time
		total  int64
		window int64
		sample int64
		basis  string
	}{
		{"recent", []time.Time{now.Add(-50 * time.Minute), now.Add(-40 * time.Minute), now.Add(-30 * time.Minute), now.Add(-20 * time.Minute), now.Add(-10 * time.Minute)}, 30, 3600, 5, "recent-running-time"},
		{"sparse", []time.Time{now.Add(-10 * time.Minute)}, 30, 10800, 30, "epoch-running-time"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := LandingRateEvidence(tc.stamps, tc.total, nil, t0, now)
			assert.Equal(t, tc.window, got.WindowSeconds)
			assert.Equal(t, tc.sample, got.Landings)
			assert.Equal(t, tc.basis, got.Basis)
			assert.Equal(t, LandingRate(tc.stamps, tc.total, nil, t0, now), got.Rate)
		})
	}
}
