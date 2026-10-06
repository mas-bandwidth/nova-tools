package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// install and run refuse at the start, each with its remedy, a harness whose
// adapter has no deliver command and a session the adapter cannot drive (dsh,
// a session under an agent preset); run proves the push with the first SESSION
// CHECK round trip and exits 2 when no pong returns within five minutes
// (docs/SPEC-FRIEND.md, The push proof).
func TestRunRefusesAHarnessThatCannotDeliver(t *testing.T) {
	t.Parallel()
	t.Run("no deliver command", func(t *testing.T) {
		t.Parallel()
		r := newRig(t, "ada", "bob")
		cli := r.cli()
		for _, h := range []string{"cursor"} {
			remedy := "run: the adapter card: give internal/friend a deliver command for " + h + " (NewDeliverer), or run the friend under a harness that has one: opencode, codex, claude, antigravity, dsh, gemini, grok"
			cli.Do(t, "run", "--as", "bob", "--harness", h, "--dir", "/w/bob").Exit(2).
				Err("RUN REFUSED: no deliver command for "+h, "the daemon did not start", remedy)
			cli.Do(t, "run", "--as", "bob", "--harness", h, "--dir", "/w/bob", "--dry-run").Exit(2).Err("RUN REFUSED: no deliver command for "+h, remedy)
			cli.Do(t, "install", "--as", "bob", "--harness", h, "--dir", "/w/bob", "--config-dir", "/w/bob-claude").Exit(2).
				Err("INSTALL REFUSED: no deliver command for "+h, "nothing was written or loaded", remedy)
			cli.Do(t, "install", "--as", "bob", "--harness", h, "--dir", "/w/bob", "--config-dir", "/w/bob-claude", "--dry-run").Exit(2).Err(remedy)
		}
		assert.Empty(t, r.launchctl, "nothing was loaded")
		assert.NoFileExists(t, filepath.Join(r.home, "Library", "LaunchAgents", "com.nova.friend-bob.plist"))
		assert.Zero(t, r.store.Len(bus.LogKey), "nothing was delivered")
	})
	t.Run("a dsh session under an agent preset", func(t *testing.T) {
		t.Parallel()
		r := newRig(t, "ada", "bob")
		require.NoError(t, r.fs.MkdirAll(filepath.Join(r.home, ".dsh", "profiles", "desktop"), 0o755))
		w := r.world()
		w.exec = func(context.Context, string, string, []string, string) (string, int, error) {
			return `dsh: session "session-z" runs under agent preset "minimal", which the one-shot runner does not compose` + "\n", 1, nil
		}
		var cancel context.CancelFunc
		w.signals = func(ctx context.Context) (context.Context, context.CancelFunc) {
			ctx, cancel = context.WithCancel(ctx)
			return ctx, cancel
		}
		stopAfter(&w, &cancel, 10*time.Minute) // a daemon that started anyway ends here, exit 0
		beats := 0
		w.beat = func(context.Context, string, string, time.Time, time.Time) (string, error) { beats++; return "", nil }
		cli := cliOf(w)
		remedy := "run: start a session in /w/bob with no agent preset and name it with --session <id>"
		cli.Do(t, "run", "--as", "bob", "--harness", "dsh", "--dir", "/w/bob", "--session", "session-z").Exit(2).
			Err("RUN REFUSED: no push proof: CHECK FAIL harness=dsh stage=deliver", "minimal", "the daemon did not start", remedy)
		assert.Zero(t, beats, "the daemon never started")
		cli.Do(t, "install", "--as", "bob", "--harness", "dsh", "--dir", "/w/bob", "--session", "session-z").Exit(2).
			Err("INSTALL REFUSED", "minimal", "the agent was booted out and its plist removed", remedy)
		assert.NoFileExists(t, filepath.Join(r.home, "Library", "LaunchAgents", "com.nova.friend-bob.plist"))
		assert.Contains(t, strings.Join(r.launchctl, "\n"), "bootout gui/501/com.nova.friend-bob")
	})
	t.Run("no pong within five minutes", func(t *testing.T) {
		t.Parallel()
		r := newRig(t, "ada", "bob")
		r.deaf = true // the session takes the turn and never runs the pong line
		w := r.world()
		var cancel context.CancelFunc
		w.signals = func(ctx context.Context) (context.Context, context.CancelFunc) {
			ctx, cancel = context.WithCancel(ctx)
			return ctx, cancel
		}
		stopAfter(&w, &cancel, 10*time.Minute) // a daemon that started anyway ends here, exit 0
		beats := 0
		w.beat = func(context.Context, string, string, time.Time, time.Time) (string, error) { beats++; return "", nil }
		dir := t.TempDir()
		var out, errb strings.Builder
		code := run([]string{"run", "--as", "bob", "--harness", "opencode", "--dir", dir, "--coordinator", "ada"}, strings.NewReader(""), &out, &errb, w)
		assert.Equal(t, 2, code, out.String())
		assert.Contains(t, errb.String(), "RUN REFUSED: no push proof: CHECK FAIL harness=opencode stage=act")
		assert.Contains(t, errb.String(), "within 5m0s")
		assert.Contains(t, errb.String(), "run: open the friend's opencode session in "+dir+", then prove it answers: nova-friend check --as bob --harness opencode --dir "+dir)
		assert.Zero(t, beats, "the daemon never started")
	})
	t.Run("a pong starts the daemon, and its beat carries the session's proof", func(t *testing.T) {
		t.Parallel()
		r := newRig(t, "ada", "bob")
		w := r.world()
		var cancel context.CancelFunc
		w.signals = func(ctx context.Context) (context.Context, context.CancelFunc) {
			ctx, cancel = context.WithCancel(ctx)
			return ctx, cancel
		}
		var pongs []time.Time
		w.beat = func(_ context.Context, _, _ string, _, pong time.Time) (string, error) {
			if pongs = append(pongs, pong); len(pongs) == 3 {
				cancel()
			}
			return "FRIEND-BEAT OK bob at=2026-10-04T03:00:00Z row_mode=batch row_width=1", nil
		}
		var out, errb strings.Builder
		code := run([]string{"run", "--as", "bob", "--harness", "opencode", "--dir", t.TempDir(), "--coordinator", "ada"}, strings.NewReader(""), &out, &errb, w)
		require.Equal(t, 0, code, errb.String())
		assert.Contains(t, out.String(), "push proof: CHECK OK harness=opencode took=")
		require.Len(t, pongs, 3)
		assert.False(t, pongs[2].IsZero(), "the beat carries the session's last proof")
		assert.False(t, pongs[2].After(r.now), "a proof is never in the future")
	})
}
