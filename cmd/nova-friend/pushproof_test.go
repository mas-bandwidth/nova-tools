package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/mas-bandwidth/nova-tools/internal/friend"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// install and run refuse at the start, each with its remedy, a harness whose
// adapter has no deliver command; install also refuses a session the adapter cannot
// drive (dsh, a session under an agent preset). run never exits for want of a proof:
// the daemon starts with its push unproven, says why once, beats down with the check's
// nonce, and delivers nothing until the session answers (docs/SPEC-FRIEND.md, The push
// proof).
func TestRunRefusesAHarnessThatCannotDeliver(t *testing.T) {
	t.Parallel()
	t.Run("no deliver command", func(t *testing.T) {
		t.Parallel()
		r := newRig(t, "ada", "bob")
		cli := r.cli()
		for _, h := range []string{"cursor"} {
			remedy := "run: the adapter card: give internal/friend a deliver command for " + h + " (NewDeliverer), or run the friend under a harness that has one: opencode, codex, claude, antigravity, dsh, gemini, grok, tmux"
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
		w.beat = func(_ context.Context, _, _ string, _ time.Time, words friend.BeatWords) (string, error) {
			if words != (friend.BeatWords{}) {
				beats++ // an up beat carries proof words; the row fetch before it does not
			}
			return "FRIEND-BEAT OK bob at=2026-10-04T03:00:00Z row_mode=batch row_width=1", nil
		}
		remedy := "run: start a session in /w/bob with no agent preset and name it with --session <id>"
		var downs []string
		w.beatDown = func(_ context.Context, _, _ string, _, _ time.Time, reason string, _ friend.BeatWords) error {
			downs = append(downs, reason)
			return nil
		}
		var out, errb strings.Builder
		code := run([]string{"run", "--as", "bob", "--harness", "dsh", "--dir", "/w/bob", "--session", "session-z"}, strings.NewReader(""), &out, &errb, w)
		require.Equal(t, 0, code, "the daemon ran until it was stopped: %s", errb.String())
		assert.Equal(t, 1, strings.Count(out.String(), "presence: REFUSED: session check r4nd0m cannot go into the session"), "said once: %s", out.String())
		assert.Contains(t, out.String(), "minimal")
		assert.Contains(t, out.String(), remedy)
		assert.Zero(t, beats, "never beaten up: the session was never proved")
		require.NotEmpty(t, downs)
		assert.Contains(t, downs[0], "push unproven: session check r4nd0m")
		w = r.world()
		w.exec = func(_ context.Context, _, prog string, args []string, _ string) (string, int, error) {
			// install records the session on the nova-config friend row before the
			// delivery check; that call is not the dsh preset refusal.
			if prog == "nova-config" && len(args) >= 2 && args[0] == "friend" && args[1] == "set" {
				return "CONFIG SET kind=friend name=bob rev=2 changed=session\n", 0, nil
			}
			return `dsh: session "session-z" runs under agent preset "minimal", which the one-shot runner does not compose` + "\n", 1, nil
		}
		cli := cliOf(w)
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
		w.beat = func(_ context.Context, _, _ string, _ time.Time, words friend.BeatWords) (string, error) {
			if words != (friend.BeatWords{}) {
				beats++ // an up beat carries proof words; the row fetch before it does not
			}
			return "FRIEND-BEAT OK bob at=2026-10-04T03:00:00Z row_mode=batch row_width=1", nil
		}
		var downs []string
		w.beatDown = func(_ context.Context, _, _ string, _, _ time.Time, reason string, _ friend.BeatWords) error {
			downs = append(downs, reason)
			return nil
		}
		dir := t.TempDir()
		var out, errb strings.Builder
		code := run([]string{"run", "--as", "bob", "--harness", "opencode", "--dir", dir, "--coordinator", "ada"}, strings.NewReader(""), &out, &errb, w)
		assert.Equal(t, 0, code, "the daemon ran until it was stopped, never exiting for want of a proof: %s", errb.String())
		assert.Empty(t, errb.String())
		assert.Contains(t, out.String(), "push proof: pending: the first session check goes into the opencode session now")
		assert.Equal(t, 1, strings.Count(out.String(), "push proof: unproven: session check r4nd0m"), "the refusal names the nonce once: %s", out.String())
		assert.Zero(t, beats, "never beaten up")
		require.NotEmpty(t, downs, "her beat says down")
		assert.Contains(t, downs[len(downs)-1], "push unproven: session check r4nd0m")
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
		var said []friend.BeatWords
		w.beatDown = func(_ context.Context, _, _ string, _, _ time.Time, _ string, words friend.BeatWords) error {
			said = append(said, words)
			return nil
		}
		ups := 0
		w.beat = func(_ context.Context, _, _ string, _ time.Time, words friend.BeatWords) (string, error) {
			said = append(said, words)
			if ups++; ups == 3 {
				cancel()
			}
			answer := "FRIEND-BEAT OK bob at=2026-10-04T03:00:00Z row_mode=batch row_width=1"
			if words.Pong != "" {
				answer += " proved=" + words.Pong
			}
			return answer, nil
		}
		var out, errb strings.Builder
		code := run([]string{"run", "--as", "bob", "--harness", "opencode", "--dir", t.TempDir(), "--coordinator", "ada"}, strings.NewReader(""), &out, &errb, w)
		require.Equal(t, 0, code, errb.String())
		assert.Contains(t, out.String(), "push proof: proved: the session answered")
		var checks, pongs []string
		for _, w := range said {
			if w.Check != "" {
				checks = append(checks, w.Check)
				assert.NotEmpty(t, w.Run, "a check is said with the daemon's run")
			}
			if w.Pong != "" {
				pongs = append(pongs, w.Pong)
				assert.NotEmpty(t, w.Run, "an answer is said with the daemon's run")
			}
		}
		assert.Equal(t, []string{"r4nd0m"}, checks, "the check her daemon asked is said once")
		assert.Equal(t, []string{"r4nd0m"}, pongs, "her session's answer names it, once")
	})
}

// A per-card harness (claude: each card a process of its own) has no session for its
// daemon to check, so its beat never proves her: no check and no answer on any beat,
// whatever the daemon does (the owner, 2026-10-05: "there is no value in things that
// are answered just by the daemon"); a card of hers finished stays her evidence.
func TestAPerCardHarnessNeverProvesByTheDaemonAlone(t *testing.T) {
	t.Parallel()
	r := newRig(t, "ada", "bob")
	w := r.world()
	var cancel context.CancelFunc
	w.signals = func(ctx context.Context) (context.Context, context.CancelFunc) {
		ctx, cancel = context.WithCancel(ctx)
		return ctx, cancel
	}
	var said []friend.BeatWords
	w.beat = func(_ context.Context, _, _ string, _ time.Time, words friend.BeatWords) (string, error) {
		if said = append(said, words); len(said) == 5 {
			cancel()
		}
		return "FRIEND-BEAT OK bob at=2026-10-04T03:00:00Z row_mode=batch row_width=1", nil
	}
	var out, errb strings.Builder
	code := run([]string{"run", "--as", "bob", "--harness", "claude", "--dir", t.TempDir()}, strings.NewReader(""), &out, &errb, w)
	require.Equal(t, 0, code, errb.String())
	require.Len(t, said, 5, "the daemon beats")
	for _, words := range said {
		assert.Equal(t, friend.BeatWords{}, words, "no proof word on a per-card harness's beat")
	}
}
