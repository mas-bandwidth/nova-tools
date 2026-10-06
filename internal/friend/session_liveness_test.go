package friend

import (
	"context"
	"fmt"
	"os/user"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/mas-bandwidth/nova-tools/internal/bus/bustest"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAFriendWhoseSessionPongsIsUpWithNoHarnessProcess is the live beat on a
// twin store: a dsh session that pongs is up with no harness process, and the
// same friend with the app running and no pong is down. The harness word is
// recorded and never decides.
func TestAFriendWhoseSessionPongsIsUpWithNoHarnessProcess(t *testing.T) {
	t.Parallel()
	u, err := user.Current()
	require.NoError(t, err)
	ctx := context.Background()
	now := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)

	up, records := beatDSH(t, ctx, now, "")
	assert.Equal(t, sprint.Up, up)
	assert.Contains(t, strings.Join(records, "\n"), "harness="+HarnessNotSeen+":")

	// The app line is what macOS reads. The dsh headless line is the same
	// session off macOS, where there is no app process to see. Either way the
	// watch records harness=running, and with no pong the row stays down.
	seen := u.Username + " 9 " + DSHApp + "\n" + u.Username + " 10 /usr/local/bin/dsh headless --session-id s -\n"
	down, records := beatDSH(t, ctx, now, seen)
	assert.Equal(t, sprint.Down, down)
	joined := strings.Join(records, "\n")
	assert.Contains(t, joined, "harness="+HarnessRunning+":")
	assert.NotContains(t, joined, "harness="+HarnessNotSeen+":")
}

// beatDSH runs the live watch over a dsh adapter and a twin store. listing
// is the process table. An empty listing with a pong beats once and the row
// is up. A listing of the app and no pong beats nothing and the row is down.
func beatDSH(t *testing.T, ctx context.Context, now time.Time, listing string) (string, []string) {
	t.Helper()
	fake := bustest.NewFake(now, "coord", "zhi")
	mem := store.NewMem()
	n := 0
	st := &store.Store{B: mem, Names: sprint.Names{Prefix: "t-"}, Actor: "coord", Now: func() time.Time { return now },
		NewID: func() string { n++; return fmt.Sprint(n) }, Sleep: func(time.Duration) {}}
	require.NoError(t, st.Init(ctx))
	require.NoError(t, mem.SetCoordinator(ctx, "coord"))
	_, _, _, err := st.SyncFriends(ctx, []store.FriendSpec{{Name: "zhi", Width: 1}})
	require.NoError(t, err)

	ps := &processTable{listing: listing}
	var delivered string
	dsh := &DSH{Dir: t.TempDir(), Session: "session-1", Program: "dsh", Run: func(ctx context.Context, dir, name string, args []string, stdin string) (string, int, error) {
		if filepath.Base(name) == "dsh" {
			delivered = stdin
			return "", 0, nil
		}
		return ps.run(ctx, dir, name, args, stdin)
	}}
	var records []string
	var beats int
	sc := &SessionCheck{
		Friend: "zhi", Store: fake, Now: func() time.Time { return now },
		Nonce:  func() string { return "n1" },
		Text:   func(nonce string) string { return SessionCheckPrefix + nonce },
		Record: func(line string) { records = append(records, line) },
		Go:     func(f func()) { f() },
	}
	sc.Deliver = sc.Gate(dsh)
	// The daemon's beat carries the session's last activity; the session
	// check and the sprint beat take a context alone, as nova-friend wires them.
	sprintBeat := sc.Beat(func(ctx context.Context) error {
		_, err := st.FriendBeat(ctx, "zhi")
		if err == nil {
			beats++
		}
		return err
	})
	d := &Daemon{
		Friend: "zhi", Dir: dsh.Dir, Now: func() time.Time { return now },
		Record: func(line string) { records = append(records, line) },
		Beat: func(ctx context.Context, _ time.Time) error {
			return sprintBeat(ctx)
		},
	}
	w := WatchHarness(d, dsh)
	err = w.Beat(ctx, time.Time{})
	require.Error(t, err, "no pong yet: the session is down and the beat is held")
	assert.Zero(t, beats)
	rows, err := st.FriendRows(ctx, now)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, sprint.Down, rows[0].Status)

	if listing == "" {
		require.Contains(t, delivered, SessionCheckPrefix+"n1")
		_, err = (&bus.Bus{Store: fake}).Send(ctx, bus.Message{From: "zhi", To: []string{"coord"}, Subject: PongSubject, Body: PongLine("n1", 0, 0, 1) + "\n"})
		require.NoError(t, err)
		require.NoError(t, w.Beat(ctx, time.Time{}))
		assert.Equal(t, 1, beats)
	}

	got, err := st.FriendBeats(ctx)
	require.NoError(t, err)
	b, ok := got["zhi"]
	require.True(t, ok, "the roster keeps the friend whether or not she has beaten")
	if listing == "" {
		assert.False(t, b.At.IsZero(), "a pong lets the sprint beat through")
	} else {
		assert.True(t, b.At.IsZero(), "no pong: the sprint beat was held")
	}
	rows, err = st.FriendRows(ctx, now)
	require.NoError(t, err)
	return rows[0].Status, records
}
