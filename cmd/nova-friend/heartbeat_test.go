package main

import (
	"context"
	"io"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/mas-bandwidth/nova-tools/internal/friend"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stalledPresenceStore blocks either the delivery read or the session proof
// read until cancellation; other operations use the socket-free fake store.
type stalledPresenceStore struct {
	bus.Store
	where string
}

func (s stalledPresenceStore) Claim(ctx context.Context, stream, group, consumer string, idle time.Duration, count int) ([]bus.Entry, error) {
	if s.where == "bus" {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return s.Store.Claim(ctx, stream, group, consumer, idle, count)
}

func (s stalledPresenceStore) Range(ctx context.Context, stream, from, to string, count int) ([]bus.Entry, error) {
	if s.where == "proof" && stream == bus.LogKey {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return s.Store.Range(ctx, stream, from, to, count)
}

// A report whose lanes have all ended names an explicit empty running list.
// An omitted list (nil) stays off the argv, so a liveness beat does not clear.
func TestAZeroRunningReportNamesAnExplicitEmptyList(t *testing.T) {
	t.Parallel()
	started := time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC)
	zero := 0
	args := beatReportArgs("amy", time.Time{}, friend.BeatWords{}, friend.BeatReport{
		Width: 4, Started: started, Running: []string{}, Working: &zero,
	})
	var got string
	for i, a := range args {
		if a == "--running" && i+1 < len(args) {
			got = args[i+1]
		}
	}
	assert.Equal(t, friend.RunningNone, got)
	assert.Contains(t, args, "--working")
	omitted := beatReportArgs("amy", time.Time{}, friend.BeatWords{}, friend.BeatReport{Width: 4, Started: started})
	assert.NotContains(t, omitted, "--running")
}

// Native heartbeat wiring keeps the actual server sender alive while the
// worker's bus read or SessionCheck.Step blocks. No session proof is invented
// (docs/SPEC-FRIEND.md, The beat; tla/Presence.tla, Heartbeat).
func TestNativeHeartbeatContinuesWhileBusOrSessionProofIsBlocked(t *testing.T) {
	t.Parallel()
	for _, where := range []string{"open", "bus", "proof"} {
		t.Run(where, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				r := newRig(t)
				w := r.world()
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				w.now = time.Now
				w.heartbeat = friend.Heartbeat
				w.signals = func(context.Context) (context.Context, context.CancelFunc) { return ctx, cancel }
				w.sleep = func(ctx context.Context, d time.Duration) {
					select {
					case <-ctx.Done():
					case <-time.After(d):
					}
				}
				w.open = func(context.Context, string) (bus.Store, func(), error) {
					if where == "open" {
						<-ctx.Done()
						return nil, nil, ctx.Err()
					}
					return stalledPresenceStore{r.store, where}, func() {}, nil
				}
				var beats []time.Time
				w.beat = func(_ context.Context, _, _ string, _ time.Time, proof friend.BeatWords) (string, error) {
					assert.Empty(t, proof.Pong, "a blocked session gave no answer")
					beats = append(beats, time.Now())
					return "", nil
				}
				done := make(chan int, 1)
				go func() {
					done <- run([]string{"run", "--as", "bob", "--harness", "opencode", "--session", "ses_main", "--dir", t.TempDir(), "--coordinator", "ada"}, strings.NewReader(""), io.Discard, io.Discard, w)
				}()
				synctest.Wait()
				require.Len(t, beats, 1, "one beat from startup, without waiting for the worker")
				first := beats[0]
				for i := 1; i <= 10; i++ {
					time.Sleep(friend.BeatEvery)
					synctest.Wait()
					require.Len(t, beats, i+1)
					assert.Equal(t, first.Add(time.Duration(i)*friend.BeatEvery), beats[i])
				}
				cancel()
				synctest.Wait()
				require.Equal(t, 0, <-done)
			})
		})
	}
}
