package store

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
)

// The unit cover of the fence handover: lock.go's Redis.Relock, a reader's
// finding that the unit tier never reached it. The Redis side is reached
// through the package's own seam, Redis.C, a client whose hook answers every
// command itself and never dials (the keyRecorder of acl_steps_test.go); no
// sleep, no real time, no network, no live store.

// relockHook answers a Redis.Relock's exchange without a store: GETRANGE
// returns the held id's prefix, so a row sets held to another id for the
// fence-moved refusal; processErr is the store refusing an exchange and pipeErr
// is the transaction failing, as a WATCHed key that moved does.
type relockHook struct {
	held       string
	processErr error
	pipeErr    error
}

func (h *relockHook) DialHook(next redis.DialHook) redis.DialHook { return next }

func (h *relockHook) ProcessHook(redis.ProcessHook) redis.ProcessHook {
	return func(_ context.Context, cmd redis.Cmder) error {
		if c, ok := cmd.(*redis.StringCmd); ok && len(cmd.Args()) > 1 && cmd.Args()[0] == "getrange" {
			id, _ := json.Marshal(h.held)
			c.SetVal(`{"id":` + string(id) + `,`)
		}
		return h.processErr
	}
}

func (h *relockHook) ProcessPipelineHook(redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(_ context.Context, _ []redis.Cmder) error { return h.pipeErr }
}

// TestLockCoverRedisRelock pins the fence handover's main path (the held
// operation's id still on the fence is handed to op) and its refusals: the
// fence moved, the transaction failed, and the store refused the exchange.
func TestLockCoverRedisRelock(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	held := "op-1"
	op := OpRecord{ID: "op-2", Verb: "tick deal"}
	for _, tt := range []struct {
		name    string
		hook    *relockHook
		want    bool
		wantErr error
	}{
		{name: "handed over", hook: &relockHook{held: held}, want: true},
		{name: "fence moved", hook: &relockHook{held: "another"}},
		{name: "transaction failed", hook: &relockHook{held: held, pipeErr: redis.TxFailedErr}},
		{name: "store refused", hook: &relockHook{held: held, processErr: errLost}, wantErr: errLost},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c := redis.NewClient(&redis.Options{Addr: "store.invalid:6379"})
			t.Cleanup(func() { _ = c.Close() })
			c.AddHook(tt.hook)
			r := &Redis{C: c}
			got, err := r.Relock(ctx, held, op)
			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr, tt.name)
				return
			}
			assert.NoError(t, err, tt.name)
			assert.Equal(t, tt.want, got, tt.name)
		})
	}
}
