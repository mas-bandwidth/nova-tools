package sprint

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestARouteRestEndIsJittered(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	start := now
	route := "test-route"

	end := restEnd(route, start, now)
	base := now.Add(RouteRestFor)

	t.Run("end within 20% of base", func(t *testing.T) {
		t.Parallel()
		delta := end.Sub(base)
		maxJitter := time.Duration(float64(RouteRestFor) * 0.2)
		assert.LessOrEqual(t, absDuration(delta), maxJitter)
	})

	t.Run("same route and start give same end", func(t *testing.T) {
		t.Parallel()
		end2 := restEnd(route, start, now)
		assert.Equal(t, end, end2)
	})

	t.Run("different routes give different ends", func(t *testing.T) {
		t.Parallel()
		endB := restEnd("other-route", start, now)
		assert.NotEqual(t, end, endB)
	})
}

func absDuration(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}
