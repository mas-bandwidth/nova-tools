package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/bus"
	"github.com/mas-bandwidth/nova-tools/pkg/bus/bustest"
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

// laneLimitRun runs a claude friend's one-shot lane over one queued card whose run
// prints laneOut and exits 1 (a limit or a credit refusal), the daemon's limit and
// refusal readers watching it: the daemon delivers nothing into a session, so a
// lane's own output is where a limit is read. It answers the record and the rig.
func laneLimitRun(t *testing.T, laneOut, beatReply string, down func(context.Context, string, []string) error, extra ...string) (string, *rig) {
	t.Helper()
	r := newRig(t, "ada", "bob")
	dir := t.TempDir()
	w := r.world()
	w.down = down
	var cancel context.CancelFunc
	w.signals = func(ctx context.Context) (context.Context, context.CancelFunc) {
		ctx, cancel = context.WithCancel(ctx)
		return ctx, cancel
	}
	var sleeps atomic.Int32
	w.sleep = func(context.Context, time.Duration) {
		synctest.Wait()
		if sleeps.Add(1) == 40 { // a beat held back while she is down never counts: the run is bounded by its steps
			cancel()
		}
	}
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "inbox", "c1~15"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "inbox", "QUEUE.json"), []byte(`{"tasks":[{"id":"c1","state":"queued"}]}`), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "inbox", "c1~15", "BRIEF.md"), []byte("RESULT: c1\n"), 0o644))
	var mu sync.Mutex
	hits := 0
	w.wall = func(_ friend.Wall, _ friend.Exec) friend.Exec {
		return func(ctx context.Context, _, _ string, _ []string, _ string) (string, int, error) {
			if friend.InLane(ctx) {
				mu.Lock()
				hits++
				mu.Unlock()
				return laneOut, 1, nil
			}
			return `{"type":"result","subtype":"success","is_error":false,"total_cost_usd":0,"session_id":"s1","result":"ok"}` + "\n", 0, nil
		}
	}
	w.beat = func(context.Context, string, string, time.Time, friend.BeatWords) (string, error) {
		return beatReply, nil
	}
	out, errb := &lockedBuilder{mu: &mu}, &strings.Builder{}
	code := run(append([]string{"run", "--as", "bob", "--harness", "claude", "--dir", dir, "--config-dir", t.TempDir()}, extra...), strings.NewReader(""), out, errb, w)
	require.Equal(t, 0, code, errb.String())
	mu.Lock()
	defer mu.Unlock()
	require.Positive(t, hits, "the lane ran its card: %s", out.String())
	return out.String(), r
}

const laneBeat = "FRIEND-BEAT OK bob at=2026-10-04T03:00:00Z row_mode=one-shot row_width=1"

// adaGot is what the coordinator ada was told on the bus, subject and body.
func adaGot(t *testing.T, r *rig) string {
	t.Helper()
	got, err := r.store.Range(context.Background(), bus.StreamOf("ada"), "-", "+", 0)
	require.NoError(t, err)
	var told []string
	for _, e := range got {
		told = append(told, e.Fields["subject"]+"\n"+e.Fields["body"])
	}
	return strings.Join(told, "\n")
}

// A harness at its limit with no seat and no --coordinator says so on the
// record instead of telling no one (tellSeat's empty answer).
func TestRunSaysThereIsNoSeatOrCoordinatorToTellOfALimit(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		said, _ := laneLimitRun(t, "working\nInsufficient AI Credits. Your credits will refresh in 10 minutes.\n", laneBeat, nil)
		assert.Contains(t, said, "no coordinator to tell: friend bob down: their harness is at its limit until", said)
	})
}

// A lane whose harness says it is at its limit makes the friend down until the
// reset, and the coordinator is told once, in the friend's pronouns: the startup
// flag, or the synced row's when its beat names one.
func TestRunTellsTheCoordinatorOfALimitInTheFriendsPronouns(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ row, flag, want string }{
		{"", "", "friend bob down: their harness is at its limit until "},
		{"", "she/her", "friend bob down: her harness is at its limit until "},
		{"", "he/him", "friend bob down: his harness is at its limit until "},
		{"she/her", "he/him", "friend bob down: her harness is at its limit until "},
		{"he/him", "they/them", "friend bob down: his harness is at its limit until "},
		{"they/them", "she/her", "friend bob down: their harness is at its limit until "},
	} {
		t.Run(tc.row+"/"+tc.flag, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				beat := laneBeat
				if tc.row != "" {
					beat += " row_pronouns=" + tc.row
				}
				flags := []string{"--coordinator", "ada"}
				if tc.flag != "" {
					flags = append(flags, "--pronouns", tc.flag)
				}
				said, r := laneLimitRun(t, "working\nInsufficient AI Credits. Your credits will refresh in 10 minutes.\n", beat, nil, flags...)
				assert.Contains(t, said, "limit: down until ")
				all := adaGot(t, r)
				assert.Contains(t, all, tc.want)
				assert.Contains(t, all, "nova-sprint friend down bob --reason 'harness limit: ")
			})
		})
	}
}

// A credit refusal with no down verb (w.down nil) is said on the record, never
// sent anywhere.
func TestRunNotesACreditRefusalWithNoDownVerb(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		said, _ := laneLimitRun(t, "Error 402\n", laneBeat, nil)
		assert.Contains(t, said, "credit refusal: no down verb to send: ", said)
	})
}

// A credit refusal with a down verb that errors says so on the record and
// tells the seat once on the bus.
func TestRunSendsTheDownVerbAndNotesItsRefusal(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		said, r := laneLimitRun(t, "Error 402\n", laneBeat, func(context.Context, string, []string) error { return errors.New("refused") }, "--coordinator", "ada")
		assert.Contains(t, said, "credit refusal: friend down: refused", said)
		assert.Equal(t, 1, strings.Count(adaGot(t, r), "The down verb was refused"), "one bus message to the seat names the down verb's refusal")
	})
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

// Receipt retries must learn a newly present coordinator even when the daemon
// loop is held at queue capacity and never calls Status again.
func TestNoticeRetryFindsANewCoordinatorWithoutAStatusStep(t *testing.T) {
	t.Parallel()
	f := bustest.NewFake(start, "ada", "bob")
	var ready atomic.Bool
	w := world{seatHolder: func(ctx context.Context, _ string) (string, error) {
		_, bounded := ctx.Deadline()
		assert.True(t, bounded, "recipient lookup shares the bounded bus attempt")
		if ready.Load() {
			return "ada", nil
		}
		return "", nil
	}}
	waiting, resume := make(chan struct{}), make(chan struct{})
	n := &friend.Notices{Bus: &bus.Bus{Store: f}, To: w.noticeTarget("fake-server", ""), Record: func(string) {}}
	n.Wait = func(ctx context.Context) bool {
		close(waiting)
		select {
		case <-ctx.Done():
			return false
		case <-resume:
			return true
		}
	}
	for range friend.NoticeLimit {
		require.True(t, n.Send(t.Context(), bus.Message{From: "bob", Subject: "held", Body: "lanes held"}))
	}
	done := make(chan bool, 1)
	go func() {
		done <- n.Send(t.Context(), bus.Message{From: "bob", Subject: "later fault", Body: "another episode"})
	}()
	<-waiting
	ready.Store(true)
	close(resume)
	require.True(t, <-done)
	assert.Zero(t, n.Unsent())
	assert.Equal(t, friend.NoticeLimit+1, f.Len(bus.StreamOf("ada")))
}
