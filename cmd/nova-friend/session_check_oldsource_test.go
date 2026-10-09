package main

import (
	"context"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/friend"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// This probe compiles unchanged against the leaking parent: an explicit
// one-shot friend must never start the beat's check outside a managed lane.
func TestExplicitOneShotNeverStartsRawPresenceTurn(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, "ada", "bob")
		w := r.world()
		var cancel context.CancelFunc
		w.signals = func(ctx context.Context) (context.Context, context.CancelFunc) {
			ctx, cancel = context.WithCancel(ctx)
			return ctx, cancel
		}
		var mu sync.Mutex
		clock := start
		w.now = func() time.Time {
			mu.Lock()
			defer mu.Unlock()
			clock = clock.Add(time.Second)
			return clock
		}
		raw := 0
		w.exec = func(ctx context.Context, _, _ string, args []string, _ string) (string, int, error) {
			mu.Lock()
			defer mu.Unlock()
			if strings.Contains(strings.Join(args, " "), friend.SessionCheckPrefix) && !friend.InLane(ctx) {
				raw++
			}
			if len(args) > 0 && args[0] == "session" {
				return "[]", 0, nil
			}
			return "fixture refuses provider", 1, nil
		}
		w.sleep = func(context.Context, time.Duration) { synctest.Wait() }
		beats := 0
		w.beatDown = func(context.Context, string, string, time.Time, time.Time, string, friend.BeatWords) error {
			beats++
			if beats == 8 {
				cancel()
			}
			return nil
		}
		out := &lockedBuilder{mu: &mu}
		var errb strings.Builder
		require.Equal(t, 0, run([]string{"run", "--as", "bob", "--harness", "opencode", "--session", "ses_old", "--dir", t.TempDir(), "--mode", "one-shot", "--width", "1"}, strings.NewReader(""), out, &errb, w), errb.String())
		mu.Lock()
		defer mu.Unlock()
		assert.Zero(t, raw, "the standalone check must not execute a raw provider turn")
	})
}
