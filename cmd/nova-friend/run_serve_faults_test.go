package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/bus"
	"github.com/mas-bandwidth/nova-tools/pkg/friend"
	"github.com/mas-bandwidth/nova-tools/pkg/sandbox"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The dispatcher (main.go:308) answers the wall verb before anything else, so a
// wall with no command is refused without running (friend.RunWall).
func TestRunWallRefusesWithoutACommand(t *testing.T) {
	t.Parallel()
	r := newRig(t, "ada", "bob")
	ran := r.cli().Do(t, "wall").Exit(sandbox.ExitRefused)
	require.Contains(t, ran.Stderr, "WALL REFUSED reason=no_command", ran)
	assert.Empty(t, ran.Stdout, "nothing is executed", ran)
}

// An unknown wall flag is refused the same way (exit sandbox.ExitRefused), never
// treated as a command (friend.RunWall).
func TestRunWallRefusesAnUnknownFlag(t *testing.T) {
	t.Parallel()
	r := newRig(t, "ada", "bob")
	ran := r.cli().Do(t, "wall", "--no-such-flag").Exit(sandbox.ExitRefused)
	assert.Empty(t, ran.Stdout, "nothing is executed", ran)
}

// wall -h prints the wall's usage and exits 0 (flag.ErrHelp in friend.RunWall).
func TestRunWallHelpExitsZero(t *testing.T) {
	t.Parallel()
	r := newRig(t, "ada", "bob")
	r.cli().Do(t, "wall", "-h").Exit(0)
}

// The dispatcher (main.go:308) turns `hook -h` (and --help) into the tool's
// help, never the raw claude hook (runClaudeHook, which refuses -h at exit 2).
func TestRunHookHelpIsTheToolsHelpNotTheClaudeHook(t *testing.T) {
	t.Parallel()
	for _, flag := range []string{"-h", "--help"} {
		t.Run(flag, func(t *testing.T) {
			t.Parallel()
			r := newRig(t, "ada", "bob")
			ran := r.cli().Do(t, "hook", flag).Exit(0)
			require.Contains(t, ran.Stdout, "hook", ran)
			assert.NotContains(t, ran.Stderr, "REFUSED", ran)
		})
	}
}

// serve (serve.go:39) says one NOTE while the store is down and one NOTE when
// it answers again, never one per tick (serve.go:90-96, 144-147).
func TestServeNotesAStoreThatFailsOneTickThenAnswers(t *testing.T) {
	t.Parallel()
	s := newServeRig(t)
	w := s.world()
	w.now = func() time.Time { return s.clock }
	var cancel context.CancelFunc
	w.signals = func(ctx context.Context) (context.Context, context.CancelFunc) {
		ctx, cancel = context.WithCancel(ctx)
		return ctx, cancel
	}
	ticks := 0
	w.sleep = func(_ context.Context, d time.Duration) {
		ticks++
		switch ticks {
		case 1:
			s.store.Fail = errors.New("store down")
		case 2:
			s.store.Fail = nil
		}
		s.clock = s.clock.Add(d)
		if ticks >= 5 {
			cancel()
		}
	}
	var out, errb strings.Builder
	code := run([]string{"serve", "--as", "ada"}, strings.NewReader(""), &out, &errb, w)
	require.Equal(t, 0, code, errb.String())
	assert.Equal(t, 1, strings.Count(out.String(), "trying again each tick"), out.String())
	assert.Contains(t, out.String(), "the store did not answer: store down", out.String())
	assert.Contains(t, out.String(), "the rows and the store answer again", out.String())
}

// serve says one NOTE when the friend rows cannot be read again after
// friend.RowsEvery, and its pings go on to the names it already had
// (serve.go:101-109).
func TestServeNotesTheFriendRowsCannotBeReadAndPingsGoOn(t *testing.T) {
	t.Parallel()
	s := newServeRig(t)
	w := s.world()
	w.now = func() time.Time { return s.clock }
	var cancel context.CancelFunc
	w.signals = func(ctx context.Context) (context.Context, context.CancelFunc) {
		ctx, cancel = context.WithCancel(ctx)
		return ctx, cancel
	}
	ticks := 0
	w.sleep = func(_ context.Context, d time.Duration) {
		ticks++
		switch ticks {
		case 60: // the re-read of the rows happens on the tick at friend.RowsEvery
			s.store.Fail = errors.New("store down")
		case 61:
			s.store.Fail = nil
		}
		s.clock = s.clock.Add(d)
		if ticks >= 63 {
			cancel()
		}
	}
	var out, errb strings.Builder
	code := run([]string{"serve", "--as", "ada"}, strings.NewReader(""), &out, &errb, w)
	require.Equal(t, 0, code, errb.String())
	assert.Equal(t, 1, strings.Count(out.String(), "the friend rows cannot be read"), out.String())
	require.GreaterOrEqual(t, pingsOn(s, "bob"), 61, "pings go on to the names it had: %s", out.String())
}

// pingsOn counts the PINGs a friend's stream holds.
func pingsOn(s *serveRig, name string) int {
	es, err := s.store.Range(context.Background(), bus.StreamOf(name), "-", "+", 0)
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range es {
		if strings.HasPrefix(e.Message().Subject, friend.PingPrefix) {
			n++
		}
	}
	return n
}

// openUntil's doc (main.go:1095-1099) says a store down at the start is no
// reason to exit: serve waits, doubling the wait, and a signal ends it at 0.
func TestServeWaitsForAStoreThatWillNotOpenThenStopsOnASignal(t *testing.T) {
	t.Parallel()
	r := newRig(t, "ada", "bob")
	r.store.Friends = []string{"ada", "bob"}
	r.store.Fail = errors.New("store down")
	w := r.world()
	var cancel context.CancelFunc
	w.signals = func(ctx context.Context) (context.Context, context.CancelFunc) {
		ctx, cancel = context.WithCancel(ctx)
		return ctx, cancel
	}
	var slept []time.Duration
	w.sleep = func(_ context.Context, d time.Duration) {
		slept = append(slept, d)
		if len(slept) >= 3 {
			cancel()
		}
	}
	var out, errb strings.Builder
	code := run([]string{"serve", "--as", "ada"}, strings.NewReader(""), &out, &errb, w)
	require.Equal(t, 0, code, errb.String())
	require.GreaterOrEqual(t, len(slept), 3)
	assert.Equal(t, []time.Duration{time.Second, 2 * time.Second}, slept[:2], "the wait doubles")
	assert.Contains(t, out.String(), "opening again in 1s")
	assert.Contains(t, out.String(), "opening again in 2s")
	assert.Contains(t, out.String(), "SERVE STOP interrupted")
}

// serve --dry-run with a store that will not open refuses at exit 2 with the
// open error (serve.go:48-58).
func TestServeDryRunRefusesAStoreThatWillNotOpen(t *testing.T) {
	t.Parallel()
	r := newRig(t, "ada", "bob")
	r.store.Fail = errors.New("store down")
	w := r.world()
	var out, errb strings.Builder
	code := run([]string{"serve", "--as", "ada", "--dry-run"}, strings.NewReader(""), &out, &errb, w)
	assert.Equal(t, 2, code)
	assert.Contains(t, errb.String(), "store down")
}

// A harness at its limit with no seat and no --coordinator says so on the
// record instead of telling no one (main.go:1448-1451, tellSeat's empty answer).
func TestRunSaysThereIsNoSeatOrCoordinatorToTellOfALimit(t *testing.T) {
	t.Parallel()
	r := newRig(t, "ada", "bob")
	_, err := (&bus.Bus{Store: r.store}).Send(context.Background(), bus.Message{From: "ada", To: []string{"bob"}, Subject: "card c9", Body: "go\n"})
	require.NoError(t, err)
	dir := t.TempDir()
	w := r.world()
	checksStarted := 0
	w.checkGo = func(f func()) {
		checksStarted++
		if checksStarted == 1 {
			f()
			return
		}
		go f()
	}
	w.friends = func(context.Context, string) ([]friend.WakeRow, string, error) {
		return nil, "ada", nil
	}
	var mu sync.Mutex
	clock := start
	w.now = func() time.Time { mu.Lock(); defer mu.Unlock(); clock = clock.Add(time.Second); return clock }
	var cancel context.CancelFunc
	w.signals = func(ctx context.Context) (context.Context, context.CancelFunc) {
		ctx, cancel = context.WithCancel(ctx)
		return ctx, cancel
	}
	sleeps := 0
	w.sleep = func(context.Context, time.Duration) {
		mu.Lock()
		defer mu.Unlock()
		clock = clock.Add(5 * time.Second)
		if sleeps++; sleeps == 2000 {
			cancel()
		}
	}
	w.beat = func(context.Context, string, string, time.Time, friend.BeatWords) (string, error) {
		return "FRIEND-BEAT OK bob at=2026-10-04T03:00:00Z row_mode=batch row_width=1", nil
	}
	limited := false
	hits := 0
	w.exec = func(_ context.Context, _, _ string, args []string, _ string) (string, int, error) {
		text := args[len(args)-1]
		if strings.HasPrefix(text, friend.SessionCheckPrefix) {
			nonce, _, _ := strings.Cut(strings.TrimPrefix(text, friend.SessionCheckPrefix), "\n")
			r.answer(nonce)
			return "answered\n", 0, nil
		}
		if strings.HasPrefix(text, "nova-friend: your harness's usage limit has reset") {
			fields := strings.Fields(text)
			return fields[len(fields)-1] + "\n", 0, nil
		}
		if strings.Contains(text, "card c9") {
			mu.Lock()
			hits++
			mu.Unlock()
			if !limited {
				limited = true
				return "working\nInsufficient AI Credits. Your credits will refresh in 10 minutes.\n", 1, nil
			}
			cancel()
			return "I ran card c9.\n", 0, nil
		}
		return "", 0, nil
	}
	out, errb := &lockedBuilder{mu: &mu}, &strings.Builder{}
	code := run([]string{"run", "--as", "bob", "--harness", "opencode", "--session", "ses_main", "--dir", dir}, strings.NewReader(""), out, errb, w)
	require.Equal(t, 0, code, errb.String())
	mu.Lock()
	defer mu.Unlock()
	require.Positive(t, hits, "the turn hit the limit: %s", out.String())
	assert.Contains(t, out.String(), "limit: no seat or coordinator to tell: friend bob down: her harness is at its limit until", out.String())
}

// driveCreditRefusal runs the opencode daemon over one queued card whose turn
// fails with a credit refusal the harness's own wording does not name (the 402
// table, cmd/nova-friend/limit.go), so RefusalWatch hands it to rw.Down
// (main.go:1461-1471). It answers the record and the rig.
func driveCreditRefusal(t *testing.T, down func(context.Context, string, []string) error, flags ...string) (string, *rig) {
	t.Helper()
	r := newRig(t, "ada", "bob")
	_, err := (&bus.Bus{Store: r.store}).Send(context.Background(), bus.Message{From: "ada", To: []string{"bob"}, Subject: "card c9", Body: "go\n"})
	require.NoError(t, err)
	dir := t.TempDir()
	w := r.world()
	w.down = down
	checksStarted := 0
	w.checkGo = func(f func()) {
		checksStarted++
		if checksStarted == 1 {
			f()
			return
		}
		go f()
	}
	w.friends = func(context.Context, string) ([]friend.WakeRow, string, error) {
		return nil, "ada", nil
	}
	var mu sync.Mutex
	clock := start
	w.now = func() time.Time { mu.Lock(); defer mu.Unlock(); clock = clock.Add(time.Second); return clock }
	var cancel context.CancelFunc
	w.signals = func(ctx context.Context) (context.Context, context.CancelFunc) {
		ctx, cancel = context.WithCancel(ctx)
		return ctx, cancel
	}
	sleeps := 0
	w.sleep = func(context.Context, time.Duration) {
		mu.Lock()
		defer mu.Unlock()
		clock = clock.Add(5 * time.Second)
		if sleeps++; sleeps == 2000 {
			cancel()
		}
	}
	w.beat = func(context.Context, string, string, time.Time, friend.BeatWords) (string, error) {
		return "FRIEND-BEAT OK bob at=2026-10-04T03:00:00Z row_mode=batch row_width=1", nil
	}
	hit := false
	w.exec = func(_ context.Context, _, _ string, args []string, _ string) (string, int, error) {
		text := args[len(args)-1]
		if strings.HasPrefix(text, friend.SessionCheckPrefix) {
			nonce, _, _ := strings.Cut(strings.TrimPrefix(text, friend.SessionCheckPrefix), "\n")
			r.answer(nonce)
			return "answered\n", 0, nil
		}
		if strings.Contains(text, "card c9") {
			mu.Lock()
			if !hit {
				hit = true
				mu.Unlock()
				cancel()
				return "Error 402\n", 1, nil
			}
			mu.Unlock()
			return "", 0, nil
		}
		return "", 0, nil
	}
	out, errb := &lockedBuilder{mu: &mu}, &strings.Builder{}
	args := append([]string{"run", "--as", "bob", "--harness", "opencode", "--session", "ses_main", "--dir", dir}, flags...)
	code := run(args, strings.NewReader(""), out, errb, w)
	require.Equal(t, 0, code, errb.String())
	mu.Lock()
	defer mu.Unlock()
	return out.String(), r
}

// A credit refusal with no down verb (w.down nil) is said on the record, never
// sent anywhere (main.go:1463-1465).
func TestRunNotesACreditRefusalWithNoDownVerb(t *testing.T) {
	t.Parallel()
	said, _ := driveCreditRefusal(t, nil)
	assert.Contains(t, said, "credit refusal: no down verb to send: no credits: opencode: Error 402", said)
}

// A credit refusal with a down verb that errors says so on the record and
// tells the seat once on the bus (main.go:1467-1470).
func TestRunSendsTheDownVerbAndNotesItsRefusal(t *testing.T) {
	t.Parallel()
	said, r := driveCreditRefusal(t, func(context.Context, string, []string) error { return errors.New("refused") }, "--coordinator", "ada")
	assert.Contains(t, said, "credit refusal: friend down: refused", said)
	es, err := r.store.Range(context.Background(), bus.StreamOf("ada"), "-", "+", 0)
	require.NoError(t, err)
	var told []string
	for _, e := range es {
		if e.Message().Subject == "friend bob: the down verb was refused" {
			told = append(told, e.Message().Subject)
		}
	}
	require.Len(t, told, 1, "one bus message to the seat names the down verb's refusal: %v", told)
	assert.Equal(t, "friend bob: the down verb was refused", told[0])
}

// --refuse-go with a shim that cannot be built (w.binary fails) makes every
// lane command answer the shim error and none run (main.go:1231-1246).
func TestRunRefuseGoWithABrokenShimBuildsNoLane(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, "ada", "bob")
		w := r.world()
		w.binary = func() (string, error) { return "", errors.New("no binary") }
		var cancel context.CancelFunc
		w.signals = func(ctx context.Context) (context.Context, context.CancelFunc) {
			ctx, cancel = context.WithCancel(ctx)
			return ctx, cancel
		}
		w.sleep = func(context.Context, time.Duration) { synctest.Wait() }
		dir := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(dir, "inbox", "c1~15"), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "inbox", "QUEUE.json"), []byte(`{"tasks":[{"id":"c1","state":"queued"}]}`), 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "inbox", "c1~15", "BRIEF.md"), []byte("RESULT: c1\n"), 0o644))
		var mu sync.Mutex
		var laneRuns int
		w.wall = func(friend.Wall, friend.Exec) friend.Exec {
			return func(context.Context, string, string, []string, string) (string, int, error) {
				mu.Lock()
				laneRuns++
				mu.Unlock()
				return "", 0, nil
			}
		}
		beats := 0
		w.beat = func(context.Context, string, string, time.Time, friend.BeatWords) (string, error) {
			beats++
			if beats == 12 {
				cancel()
			}
			return "FRIEND-BEAT OK bob at=2026-10-04T03:00:00Z row_mode=one-shot row_width=1", nil
		}
		out, errb := &lockedBuilder{mu: &mu}, &strings.Builder{}
		code := run([]string{"run", "--as", "bob", "--harness", "claude", "--dir", dir, "--config-dir", "/accounts/heavy-a", "--refuse-go"}, strings.NewReader(""), out, errb, w)
		require.Equal(t, 0, code, errb.String())
		mu.Lock()
		defer mu.Unlock()
		assert.Zero(t, laneRuns, "no lane ran: %s", out.String())
		assert.Contains(t, out.String(), "the go refusal shims cannot be made, so no lane runs", out.String())
	})
}
