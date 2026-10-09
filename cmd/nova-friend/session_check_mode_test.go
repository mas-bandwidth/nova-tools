package main

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/friend"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProductionSessionCheckModeNeverLeaksOneShotOrUnknownTurn(t *testing.T) {
	t.Parallel()
	rows := []struct {
		name, override, row string
		raw                 bool
	}{
		{"explicit one-shot beats default batch", "one-shot", "batch", false},
		{"configured one-shot no override", "", "one-shot", false},
		{"explicit batch preserves adapter", "batch", "one-shot", true},
		{"resolved batch preserves adapter", "", "batch", true},
		{"unknown stays held", "", "", false},
		{"malformed mode stays held", "", "invalid", false},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				r := newRig(t, "ada", "bob")
				w := r.world()
				w.initialRowMode = "" // production must learn the row from its actual down reply
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
				rawChecks, marked, downs := 0, 0, 0
				w.exec = func(ctx context.Context, dir, program string, args []string, _ string) (string, int, error) {
					mu.Lock()
					defer mu.Unlock()
					if len(args) > 0 && args[0] == "session" {
						return `[{"id":"ses_old","directory":"` + dir + `","updated":1}]`, 0, nil
					}
					text := strings.Join(args, " ")
					if strings.Contains(text, friend.SessionCheckPrefix) && !friend.InLane(ctx) {
						rawChecks++
					}
					if friend.InLane(ctx) {
						marked++
					}
					return "", 1, errors.New("fixture refuses provider; no session proof")
				}
				w.wall = func(wl friend.Wall, run friend.Exec) friend.Exec {
					return func(ctx context.Context, dir, program string, args []string, stdin string) (string, int, error) {
						// Preserve context and test the actual wall wrapper separately in the lane rig.
						return run(ctx, dir, program, args, stdin)
					}
				}
				w.sleep = func(context.Context, time.Duration) { synctest.Wait() }
				w.beatDownAnswer = func(_ context.Context, _, _ string, _, _ time.Time, _ string, words friend.BeatWords) (string, error) {
					downs++
					if row.override == "" && downs == 1 {
						assert.Empty(t, words.Check, "unknown mode cannot dispatch before the first down reply")
					}
					if downs == 12 {
						cancel()
					}
					if row.row == "" {
						return "FRIEND-BEAT OK bob machine=STOPPED", nil
					}
					return "FRIEND-BEAT OK bob row_mode=" + row.row + " row_width=1 machine=STOPPED", nil
				}
				args := []string{"run", "--as", "bob", "--harness", "opencode", "--dir", t.TempDir(), "--width", "1"}
				if row.override != "" {
					args = append(args, "--mode", row.override)
				}
				out := &lockedBuilder{mu: &mu}
				var errb strings.Builder
				require.Equal(t, 0, run(args, strings.NewReader(""), out, &errb, w), errb.String())
				mu.Lock()
				defer mu.Unlock()
				if row.raw {
					assert.Positive(t, rawChecks, "resolved batch still uses its existing check adapter")
				} else {
					assert.Zero(t, rawChecks, "a one-shot check cannot spawn an unowned provider turn")
				}
				if row.override == "one-shot" || row.row == "one-shot" && row.override != "batch" {
					assert.Positive(t, marked, "the managed lane opening remains reachable")
				}
			})
		})
	}
}
