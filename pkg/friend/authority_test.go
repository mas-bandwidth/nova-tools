package friend

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestTheSeatIsCachedForTenSecondsOnTheDaemonsClock(t *testing.T) {
	t.Parallel()
	calls, holder := 0, "ada"
	l := &loop{d: &Daemon{Seat: func(context.Context) (string, error) { calls++; return holder, nil }}, ctx: context.Background()}
	assert.Equal(t, "ada", l.seat(t0))
	holder = "bob"
	assert.Equal(t, "ada", l.seat(t0.Add(SeatCacheFor-time.Second)), "inside the cache")
	assert.Equal(t, 1, calls)
	assert.Equal(t, "bob", l.seat(t0.Add(SeatCacheFor)), "at ten seconds it is read again")
	assert.Equal(t, 2, calls)
}
