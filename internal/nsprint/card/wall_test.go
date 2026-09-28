package card_test

import (
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card"
)

// TestWallMaxIsEstTimesOneAndAHalf pins the cap's arithmetic and its defaults.
func TestWallMaxIsEstTimesOneAndAHalf(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		est, def float64
		want     time.Duration
	}{
		{20, 0, 30 * time.Minute},
		{20, 40, 30 * time.Minute},
		{0, 40, 60 * time.Minute},
		{0, 0, 45 * time.Minute},
		{0.0001, 0, time.Second},
	} {
		if got := card.WallMax(tc.est, tc.def); got != tc.want {
			t.Fatalf("WallMax(%v, %v) = %s, want %s", tc.est, tc.def, got, tc.want)
		}
	}
}
