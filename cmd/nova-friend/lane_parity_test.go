package main

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/friend"
)

// A provider failure writes the pause marker; only a person's resume clears it, and says what it held.
func TestResumeClearsTheLanesPauseAPersonBringsUp(t *testing.T) {
	t.Parallel()
	r := newRig(t, "ada", "bob")
	cli := r.cli()
	state := friend.DefaultStateDir(r.home, "bob")
	cli.Do(t, "resume", "--as", "bob").Exit(0).Out("cleared=none")
	require.NoError(t, friend.WritePause(state, "402 Payment Required: insufficient balance", start))
	cli.Do(t, "resume", "--as", "bob", "--dry-run").Exit(0).Out("cleared=would", `402\x20Payment\x20Required:\x20insufficient\x20balance`)
	assert.NotEmpty(t, friend.ReadPause(state), "a dry run clears nothing")
	cli.Do(t, "resume", "--as", "bob").Exit(0).Out("cleared=yes", `402\x20Payment\x20Required:\x20insufficient\x20balance`)
	assert.Empty(t, friend.ReadPause(state))
}

// The shims run this binary as go or gofmt: the refuse-go verb says no, exit 2, with the next step.
func TestRefuseGoRefusesWithTheWayToABench(t *testing.T) {
	t.Parallel()
	r := newRig(t, "ada", "bob")
	cli := r.cli()
	for _, name := range friend.GoShimNames {
		cli.Do(t, "refuse-go", "--name", name).Exit(2).Err(name+" is refused: no go command runs on this machine", "; run: rsync the job's clone to a bench")
	}
}

// While a provider failure has paused her lanes (the marker stands), her beat says her down
// with the provider's exact message, through the served friend beat --until --reason, never
// up; once a person clears the marker she beats up again (docs/SPEC-FRIEND.md, one-shot
// lanes at parity).
func TestRunBeatsDownWhileTheLanesArePausedUntilAPersonResumes(t *testing.T) {
	t.Parallel()
	r := newRig(t, "ada", "bob")
	dir := t.TempDir()
	state := friend.StateDirIn(dir)
	require.NoError(t, friend.WritePause(state, "402 Payment Required: insufficient balance", start))
	w := r.world()
	var mu sync.Mutex
	clock := start
	w.now = func() time.Time { mu.Lock(); defer mu.Unlock(); clock = clock.Add(time.Second); return clock }
	var cancel context.CancelFunc
	w.signals = func(ctx context.Context) (context.Context, context.CancelFunc) {
		ctx, cancel = context.WithCancel(ctx)
		return ctx, cancel
	}
	var cleared time.Time
	sleeps := 0
	w.sleep = func(context.Context, time.Duration) {
		mu.Lock()
		defer mu.Unlock()
		clock = clock.Add(5 * time.Second)
		switch sleeps++; sleeps {
		case 40: // a person resumes
			_, err := friend.ClearPause(state)
			assert.NoError(t, err)
			cleared = clock
		case 80:
			cancel()
		}
	}
	var ups []time.Time
	w.beat = func(context.Context, string, string, time.Time, friend.BeatWords) (string, error) {
		mu.Lock()
		ups = append(ups, clock)
		mu.Unlock()
		return "FRIEND-BEAT OK bob at=2026-10-04T03:00:00Z row_mode=batch row_width=1", nil
	}
	type downBeat struct {
		at, until time.Time
		reason    string
	}
	var downs []downBeat
	w.beatDown = func(_ context.Context, _, _ string, _, until time.Time, reason string, _ friend.BeatWords) error {
		mu.Lock()
		downs = append(downs, downBeat{clock, until, reason})
		mu.Unlock()
		return nil
	}
	w.exec = func(_ context.Context, _, _ string, args []string, _ string) (string, int, error) {
		if text := args[len(args)-1]; strings.HasPrefix(text, friend.SessionCheckPrefix) {
			nonce, _, _ := strings.Cut(strings.TrimPrefix(text, friend.SessionCheckPrefix), "\n")
			r.answer(nonce)
			return "answered\n", 0, nil
		}
		return "", 0, nil
	}
	out, errb := &lockedBuilder{mu: &mu}, &strings.Builder{}
	code := run([]string{"run", "--as", "bob", "--harness", "opencode", "--session", "ses_main", "--dir", dir, "--coordinator", "ada", "--model", "inception/mercury-2.5", "--mode", "one-shot"}, strings.NewReader(""), out, errb, w)
	require.Equal(t, 0, code, errb.String())
	mu.Lock()
	defer mu.Unlock()
	require.False(t, cleared.IsZero())
	require.NotEmpty(t, downs, "she beats down while paused\n%s", out.String())
	for _, b := range downs {
		if strings.HasPrefix(b.reason, "push unproven: ") {
			// resumed, her session is asked again before it is beaten up: its own word, down
			assert.False(t, b.at.Before(cleared), "the session check's down beat follows the resume: %s before %s", b.at, cleared)
			continue
		}
		assert.False(t, b.at.After(cleared), "a down beat only while the marker stands: %s after %s", b.at, cleared)
		assert.Equal(t, "provider failure (inception/mercury-2.5): 402 Payment Required: insufficient balance", b.reason, "the provider's exact message")
		assert.InDelta(t, friend.PauseBeatAhead.Seconds(), b.until.Sub(b.at).Seconds(), 10, "until an hour ahead, sent again each beat")
	}
	for _, at := range ups {
		assert.False(t, at.Before(cleared), "no up beat while paused: %s before %s", at, cleared)
	}
	assert.NotEmpty(t, ups, "she beats up once a person resumed")
}
