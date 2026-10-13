package config

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// oldLibraryHook is the whole store, in process: no connection is ever made.
// It answers every FCALL of the named function with the reply an older
// nova_sprint library gives (the window between this tool's release and the
// deploy that loads the new library: Prepare installs a library only when the
// store has none, fn.LoadMissing), GET fleet:coordinator with m1, a friend's
// beat host with nil, and refuses any other command.
type oldLibraryHook struct {
	function string
	reply    []any
}

func (h *oldLibraryHook) DialHook(next redis.DialHook) redis.DialHook { return next }

func (h *oldLibraryHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		args := cmd.Args()
		switch c := cmd.(type) {
		case *redis.Cmd:
			if len(args) > 1 && strings.EqualFold(cmd.Name(), "fcall") && args[1] == h.function {
				c.SetVal(h.reply)
				return nil
			}
		case *redis.StringCmd:
			switch {
			case strings.EqualFold(cmd.Name(), "get") && len(args) > 1 && args[1] == FleetKey("coordinator"):
				c.SetVal("m1")
				return nil
			case strings.EqualFold(cmd.Name(), "hget"):
				c.SetErr(redis.Nil)
				return redis.Nil
			}
		}
		err := fmt.Errorf("fake store: unexpected command %v", args)
		cmd.SetErr(err)
		return err
	}
}

func (h *oldLibraryHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}

// TestAnOldLibrarysCeilingIsANamedRefusalNeverAStoreError: until the deploy
// loads the new capacity functions, the store's older ns_capacity_desired can
// still answer CEILING or NOCEILING for a friend, and ns_capacity_machine
// CEILING for a machine. Each is a *RefusedError of ErrStaleLibrary naming the
// reply and the deploy step, never a plain error: a plain error is a store
// error, which stops the whole apply before the sprint kind (the v1.2.16
// incident).
func TestAnOldLibrarysCeilingIsANamedRefusalNeverAStoreError(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	applier := func(t *testing.T, hook *oldLibraryHook) *RedisApplier {
		t.Helper()
		// The address is never dialled: the hook answers every command.
		c := redis.NewClient(&redis.Options{Addr: "fake-store.invalid:0"})
		t.Cleanup(func() { _ = c.Close() })
		c.AddHook(hook)
		return &RedisApplier{Client: c}
	}
	friend := Row{Name: "ada", Fields: map[string]string{"tiers": "flash", "roles": "", "width": "16"}}

	for _, reply := range [][]any{
		{"CEILING", "m1", "16", "8"},
		{"NOCEILING", "m1", "0", "0"},
	} {
		t.Run("friend "+reply[0].(string), func(t *testing.T) {
			t.Parallel()
			a := applier(t, &oldLibraryHook{function: "ns_capacity_desired", reply: reply})
			err := a.Write(ctx, KindFriend, friend, nil, "ada", "config:friend:1")
			require.Error(t, err)
			assert.True(t, Refused(err), "an old library's %s was not a refusal: %v", reply[0], err)
			assert.ErrorIs(t, err, ErrStaleLibrary)
			assert.Contains(t, err.Error(), "friend ada: the store's nova_sprint function library answered "+reply[0].(string))
			assert.Contains(t, err.Error(), "; run: nova-redis fn load --addr <store>")
		})
	}

	t.Run("machine CEILING", func(t *testing.T) {
		t.Parallel()
		a := applier(t, &oldLibraryHook{function: "ns_capacity_machine", reply: []any{"CEILING", "m1", "64", "1"}})
		machine := Row{Name: "m1", Fields: map[string]string{"user": "u", "seat": "m1", "slots": "1"}}
		err := a.Write(ctx, KindMachine, machine, nil, "ada", "config:machine:1")
		require.Error(t, err)
		assert.True(t, Refused(err), "an old library's machine CEILING was not a refusal: %v", err)
		assert.ErrorIs(t, err, ErrStaleLibrary)
		assert.Contains(t, err.Error(), "machine m1: the store's nova_sprint function library answered CEILING m1 64 1")
	})
}
