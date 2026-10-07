package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/friend"
	"github.com/stretchr/testify/assert"
)

// The CLI guard selects the notification-only route before native factories, including
// startup and dry run (SPEC-FRIEND.md, notifications).
func TestNotificationRunGuardsNativeFactoriesBeforeStartup(t *testing.T) {
	t.Parallel()
	for _, dry := range []bool{false, true} {
		t.Run(map[bool]string{false: "run", true: "dry"}[dry], func(t *testing.T) {
			t.Parallel()
			r := newRig(t, "ada", "bob")
			w := r.world()
			w.stage = func(string) *friend.Stager { t.Error("native stager constructed"); return nil }
			w.beat = func(context.Context, string, string, time.Time, friend.BeatWords) (string, error) {
				t.Error("native beat called")
				return "", nil
			}
			var stop context.CancelFunc
			w.signals = func(parent context.Context) (context.Context, context.CancelFunc) {
				ctx, cancel := context.WithCancel(parent)
				stop = cancel
				return ctx, cancel
			}
			w.sleep = func(context.Context, time.Duration) {
				if stop != nil {
					stop()
				}
			}
			args := []string{"run", "--as", "bob", "--harness", "codex", "--dir", t.TempDir(), "--session", "thread-test", "--notifications-only"}
			if dry {
				args = append(args, "--dry-run")
			}
			// Use the existing tool harness so flag validation and early dispatch both run.
			var out, errs strings.Builder
			code := run(args, strings.NewReader(""), &out, &errs, w)
			assert.Zero(t, code, errs.String())
			assert.Empty(t, r.beats)
		})
	}
}
