package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/bus"
	"github.com/mas-bandwidth/nova-tools/pkg/friend"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// install and run refuse at the start, each with its remedy, a harness whose
// adapter has no deliver command; install also refuses a session the adapter cannot
// drive (dsh, a session under an agent preset). run starts without SessionCheck or
// PushProver and delivers without holding on a SessionCheck pong.
func TestRunRefusesAHarnessThatCannotDeliver(t *testing.T) {
	t.Parallel()
	t.Run("no deliver command", func(t *testing.T) {
		t.Parallel()
		r := newRig(t, "ada", "bob")
		cli := r.cli()
		for _, h := range []string{"cursor"} {
			remedy := "run: the adapter card: give pkg/friend a deliver command for " + h + " (NewDeliverer), or run the friend under a harness that has one: opencode, claude, antigravity, dsh, gemini, grok, tmux"
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
		remedy := "run: start a session in /w/bob with no agent preset and name it with --session <id>"
		w := r.world()
		w.exec = func(context.Context, string, string, []string, string) (string, int, error) {
			return `dsh: session "session-z" runs under agent preset "minimal", which the one-shot runner does not compose` + "\n", 1, nil
		}
		cli := cliOf(w)
		cli.Do(t, "install", "--as", "bob", "--harness", "dsh", "--dir", "/w/bob", "--session", "session-z").Exit(2).
			Err("INSTALL REFUSED", "minimal", "the agent was booted out and its plist removed", remedy)
		assert.NoFileExists(t, filepath.Join(r.home, "Library", "LaunchAgents", "com.nova.friend-bob.plist"))
		assert.Contains(t, strings.Join(r.launchctl, "\n"), "bootout gui/501/com.nova.friend-bob")
	})
	t.Run("delivery is not held on a session check pong", func(t *testing.T) {
		t.Parallel()
		r := newRig(t, "ada", "bob")
		r.deaf = true // the session never runs nova-friend pong
		_, err := (&bus.Bus{Store: r.store}).Send(context.Background(), bus.Message{From: "ada", To: []string{"bob"}, Subject: "hello", Body: "hi\n"})
		require.NoError(t, err)
		w := r.world()
		var cancel context.CancelFunc
		w.signals = func(ctx context.Context) (context.Context, context.CancelFunc) {
			ctx, cancel = context.WithCancel(ctx)
			return ctx, cancel
		}
		var delivered []string
		w.exec = func(_ context.Context, dir, _ string, args []string, _ string) (string, int, error) {
			if args[0] == "--version" || len(args) > 1 && args[1] == "--help" {
				return "", 0, nil
			}
			if args[0] == "session" {
				return `[{"id":"ses_1","directory":"` + dir + `","updated":1}]`, 0, nil
			}
			delivered = append(delivered, args[len(args)-1])
			cancel()
			return "ok\n", 0, nil
		}
		w.beat = func(context.Context, string, string, time.Time, friend.BeatWords) (string, error) {
			return "FRIEND-BEAT OK bob at=2026-10-04T03:00:00Z row_mode=batch row_width=1", nil
		}
		var out, errb strings.Builder
		code := run([]string{"run", "--as", "bob", "--harness", "opencode", "--dir", t.TempDir(), "--coordinator", "ada"}, strings.NewReader(""), &out, &errb, w)
		require.Equal(t, 0, code, errb.String())
		require.Len(t, delivered, 1)
		assert.Contains(t, delivered[0], "hello")
		assert.NotContains(t, delivered[0], friend.SessionCheckPrefix)
	})
}

func TestAClaudeDaemonsCheckGoesInByTheFolderAndItsBeatCarriesThePong(t *testing.T) {
	t.Parallel()
	r := newRig(t, "ada", "bob")
	w := r.world()
	var cancel context.CancelFunc
	w.signals = func(ctx context.Context) (context.Context, context.CancelFunc) {
		ctx, cancel = context.WithCancel(ctx)
		return ctx, cancel
	}
	dir := t.TempDir()
	var said []friend.BeatWords
	w.beat = func(_ context.Context, _, _ string, _ time.Time, words friend.BeatWords) (string, error) {
		said = append(said, words)
		if len(said) == 3 {
			cancel()
		}
		return "FRIEND-BEAT OK bob at=2026-10-04T03:00:00Z row_mode=batch row_width=1", nil
	}
	var out, errb strings.Builder
	code := run([]string{"run", "--as", "bob", "--harness", "claude", "--dir", dir}, strings.NewReader(""), &out, &errb, w)
	require.Equal(t, 0, code, errb.String())
	assert.Len(t, said, 3)
	assert.NoFileExists(t, friend.SessionCheckFile(dir, "r4nd0m"))
}
