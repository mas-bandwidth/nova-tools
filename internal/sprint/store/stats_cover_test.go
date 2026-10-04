package store

import (
	"context"
	"errors"
	"net"
	"sync/atomic"
	"testing"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The round-trip counters of a Redis backend (stats.go): each command, and
// each pipeline or transaction, sent and answered is one. The hooks are driven
// through the client itself answering every command from a hook (the
// keyRecorder of acl_steps_test.go), so no store is dialled: a hook that
// answers without calling next never dials, and the dial hook is the pass
// through it counts nothing of.

// errNextSaidNo is the error the wrapped next returns, to show a refused
// exchange is still one round trip.
var errNextSaidNo = errors.New("next said no")

func TestStatsCoverDialHookPassesTheDialThroughAndCountsNoTrip(t *testing.T) {
	t.Parallel()
	var n atomic.Int64
	h := tripHook{n: &n}
	sentinel := errors.New("dial refused")
	next := func(_ context.Context, _, _ string) (net.Conn, error) { return nil, sentinel }
	// The dial hook is the pass through: the dial it wraps runs untouched, and
	// a dial is not a round trip, so nothing is counted.
	wrapped := h.DialHook(next)
	_, err := wrapped(context.Background(), "tcp", "store.invalid:6379")
	assert.Equal(t, sentinel, err)
	assert.Equal(t, int64(0), n.Load(), "a dial is not a round trip")
}

func TestStatsCoverProcessHookCountsOneTripAndCarriesTheError(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		want error
	}{
		{name: "answered", want: nil},
		{name: "refused", want: errNextSaidNo},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var n atomic.Int64
			h := tripHook{n: &n}
			var nextCmd redis.Cmder
			next := func(_ context.Context, cmd redis.Cmder) error {
				nextCmd = cmd
				return tc.want
			}
			cmd := redis.NewStringCmd(context.Background(), "GET", "key")
			err := h.ProcessHook(next)(context.Background(), cmd)
			assert.Equal(t, tc.want, err, "the hook's answer is the next's, not its own")
			assert.Equal(t, int64(1), n.Load(), "one command sent and answered is one trip, even when it is refused")
			assert.Equal(t, cmd, nextCmd, "the next hook reads the command the client sent")
		})
	}
}

func TestStatsCoverProcessPipelineHookCountsOneTripPerPipeline(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		want error
	}{
		{name: "answered", want: nil},
		{name: "refused", want: errNextSaidNo},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var n atomic.Int64
			h := tripHook{n: &n}
			var nextCmds []redis.Cmder
			next := func(_ context.Context, cmds []redis.Cmder) error {
				nextCmds = cmds
				return tc.want
			}
			cmds := []redis.Cmder{
				redis.NewStringCmd(context.Background(), "GET", "a"),
				redis.NewStringCmd(context.Background(), "GET", "b"),
			}
			err := h.ProcessPipelineHook(next)(context.Background(), cmds)
			assert.Equal(t, tc.want, err)
			assert.Equal(t, int64(1), n.Load(), "a pipeline of many commands is one round trip")
			assert.Equal(t, cmds, nextCmds, "the next hook answers every command of the pipeline")
		})
	}
}

func TestStatsCoverCountTripsCountsEachCommandAsOneTrip(t *testing.T) {
	t.Parallel()
	c := redis.NewClient(&redis.Options{Addr: "store.invalid:6379"})
	t.Cleanup(func() { _ = c.Close() })
	r := &Redis{C: c}
	r.CountTrips()
	require.Equal(t, int64(0), r.Trips(), "nothing sent yet: no trip")
	c.AddHook(&keyRecorder{}) // added after: the trip hook is first, the recorder answers without dialling
	ctx := context.Background()
	require.NoError(t, r.C.Get(ctx, "a").Err())
	require.Equal(t, int64(1), r.Trips(), "one command, one trip")
	require.NoError(t, r.C.Get(ctx, "b").Err())
	assert.Equal(t, int64(2), r.Trips(), "each command is one")
}

func TestStatsCoverCountTripsRefusesASecondCounter(t *testing.T) {
	t.Parallel()
	c := redis.NewClient(&redis.Options{Addr: "store.invalid:6379"})
	t.Cleanup(func() { _ = c.Close() })
	r := &Redis{C: c}
	r.CountTrips()
	r.CountTrips() // the refusal: the counter and the hook already exist
	c.AddHook(&keyRecorder{})
	p := c.Pipeline()
	p.Get(context.Background(), "a")
	p.Get(context.Background(), "b")
	_, err := p.Exec(context.Background())
	require.NoError(t, err)
	assert.Equal(t, int64(1), r.Trips(), "one pipeline is one trip, whatever it holds, whatever it was asked")
}

func TestStatsCoverTripsIsZeroWithoutCountTrips(t *testing.T) {
	t.Parallel()
	c := redis.NewClient(&redis.Options{Addr: "store.invalid:6379"})
	t.Cleanup(func() { _ = c.Close() })
	r := &Redis{C: c}
	c.AddHook(&keyRecorder{})
	require.NoError(t, r.C.Get(context.Background(), "a").Err())
	assert.Equal(t, int64(0), r.Trips(), "a backend that was never asked to count says zero")
}
