package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/mas-bandwidth/nova-tools/internal/friend"
	"github.com/mas-bandwidth/nova-tools/internal/sprintwire"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWakePingsEveryUpFriendAndReportsTheDeafOnes(t *testing.T) {
	t.Parallel()
	r := newRig(t, "ada", "amy", "bob", "cy", "dee", "alex")
	r.store.Friends = []string{"ada", "amy", "bob", "cy", "dee", "alex"}
	clock := start
	every := 10 * time.Second
	bound := 2 * time.Second

	friendsTable := []friend.FriendsTableRow{
		{Name: "amy", Status: "up"},
		{Name: "bob", Status: "up"},
		{Name: "cy", Status: "up"},
		{Name: "dee", Status: "held", Reason: "held by coordinator"},
		{Name: "alex", Status: "up", NeverWake: true},
	}

	w := r.world()
	n := 0
	w.now = func() time.Time { return clock }
	w.random = func() string { n++; return fmt.Sprintf("nonce%04d", n) }
	var cancel context.CancelFunc
	w.signals = func(ctx context.Context) (context.Context, context.CancelFunc) {
		ctx, cancel = context.WithCancel(ctx)
		return ctx, cancel
	}
	w.friendsTable = func(ctx context.Context) ([]friend.FriendsTableRow, error) {
		return friendsTable, nil
	}

	answered := map[string]int{}
	passes := 0
	w.sleep = func(_ context.Context, d time.Duration) {
		// During each sleep, answer pongs for amy and bob, but not cy
		for _, f := range []string{"amy", "bob"} {
			es, err := r.store.Range(context.Background(), bus.StreamOf(f), "-", "+", 0)
			require.NoError(t, err)
			for _, e := range es[answered[f]:] {
				if nonce, ok := strings.CutPrefix(e.Message().Subject, friend.PingPrefix); ok {
					_, err := (&bus.Bus{Store: r.store}).Send(context.Background(), bus.Message{
						From:    f,
						To:      []string{"ada"},
						Subject: friend.PongSubject,
						Body:    friend.PongLine(nonce, 0, 0, 1) + "\n",
					})
					require.NoError(t, err)
				}
			}
			answered[f] = len(es)
		}
		clock = clock.Add(d)
		if d >= every-bound {
			passes++
			if passes >= 2 {
				cancel()
			}
		}
	}

	var out, errb strings.Builder
	code := run([]string{
		"ping",
		"--wake",
		"--every", every.String(),
		"--to-friends",
		"--bound", bound.String(),
		"--as", "ada",
	}, strings.NewReader(""), &out, &errb, w)

	require.Equal(t, 0, code, errb.String())

	// Dee (held) and Alex (never-wake) were never pinged
	assert.Equal(t, 0, r.store.Len(bus.StreamOf("dee")), "held friend dee was not pinged")
	assert.Equal(t, 0, r.store.Len(bus.StreamOf("alex")), "never-wake friend alex was not pinged")

	// Amy, Bob, Cy received wake pings
	assert.Greater(t, r.store.Len(bus.StreamOf("amy")), 0, "amy was pinged")
	assert.Greater(t, r.store.Len(bus.StreamOf("bob")), 0, "bob was pinged")
	assert.Greater(t, r.store.Len(bus.StreamOf("cy")), 0, "cy was pinged")

	// Check messages on ada's stream for deaf notes
	adaEntries, err := r.store.Range(context.Background(), bus.StreamOf("ada"), "-", "+", 0)
	require.NoError(t, err)
	var deafNotes []bus.Message
	for _, e := range adaEntries {
		m := e.Message()
		if strings.Contains(strings.ToLower(m.Subject), "deaf") || strings.Contains(strings.ToLower(m.Body), "deaf") {
			deafNotes = append(deafNotes, m)
		}
	}

	// Exactly one deaf note naming cy, none for dee or alex, and no second note while cy stays deaf
	require.Len(t, deafNotes, 1, "exactly one deaf note sent across two passes while cy stays deaf")
	note := deafNotes[0]
	assert.Contains(t, note.Subject+note.Body, "cy", "the deaf note names cy")
	assert.NotContains(t, note.Subject+note.Body, "dee", "the deaf note does not name held friend dee")
	assert.NotContains(t, note.Subject+note.Body, "alex", "the deaf note does not name never-wake friend alex")
}

// TestWakePingNeverFallsBackToTheBusMembers: with no friends table to read (the sprint
// server unreachable, no --table), the loop refuses and wakes no one, though the bus
// holds every friend as a member: the member list carries no held, down or never-wake.
func TestWakePingNeverFallsBackToTheBusMembers(t *testing.T) {
	t.Parallel()
	r := newRig(t, "ada", "amy", "dee", "alex")
	r.store.Friends = []string{"ada", "amy", "dee", "alex"}
	w := r.world()
	w.friendsTable = nil
	var cancel context.CancelFunc
	w.signals = func(ctx context.Context) (context.Context, context.CancelFunc) {
		ctx, cancel = context.WithCancel(ctx)
		return ctx, cancel
	}
	w.sleep = func(context.Context, time.Duration) { cancel() } // one pass at most, should a regression start the loop
	var asked []string
	w.sprint = func(_ context.Context, server string, argv []string) ([]sprintwire.Result, error) {
		asked = append(asked, server+" "+strings.Join(argv, " "))
		return nil, errors.New("connection refused")
	}

	var out, errb strings.Builder
	code := run([]string{"ping", "--wake", "--every", "10s", "--to-friends", "--as", "ada"}, strings.NewReader(""), &out, &errb, w)

	assert.NotEqual(t, 0, code, out.String())
	assert.Contains(t, out.String()+errb.String(), "the friends table cannot be read")
	assert.Equal(t, []string{DefaultServer + " where --json"}, asked, "the table is read from the sprint server and nowhere else")
	for _, f := range []string{"amy", "dee", "alex"} {
		assert.Equal(t, 0, r.store.Len(bus.StreamOf(f)), "%s was not pinged", f)
	}
}

// TestWakePingSkipsAPassWhoseTableCannotBeRead: a later pass that cannot read the table
// pings no one (the last pass's targets may since be held) and says it skipped.
func TestWakePingSkipsAPassWhoseTableCannotBeRead(t *testing.T) {
	t.Parallel()
	r := newRig(t, "ada", "amy")
	w := r.world()
	clock := start
	w.now = func() time.Time { return clock }
	w.sleep = func(_ context.Context, d time.Duration) { clock = clock.Add(d) }
	var cancel context.CancelFunc
	w.signals = func(ctx context.Context) (context.Context, context.CancelFunc) {
		ctx, cancel = context.WithCancel(ctx)
		return ctx, cancel
	}
	reads := 0
	w.friendsTable = func(context.Context) ([]friend.FriendsTableRow, error) {
		reads++
		if reads == 1 {
			return []friend.FriendsTableRow{{Name: "amy", Status: "up"}}, nil
		}
		cancel()
		return nil, errors.New("the sprint server did not answer")
	}

	var out, errb strings.Builder
	code := run([]string{"ping", "--wake", "--every", "10s", "--to-friends", "--bound", "2s", "--as", "ada"}, strings.NewReader(""), &out, &errb, w)

	require.Equal(t, 0, code, errb.String())
	assert.Equal(t, 1, r.store.Len(bus.StreamOf("amy")), "amy was pinged on pass 1 only")
	assert.Contains(t, out.String(), "PING SKIP pass=2 the friends table cannot be read")
}

func TestWakePingInstallAndUninstall(t *testing.T) {
	t.Parallel()
	r := newRig(t, "ada")
	w := r.world()
	w.goos = "linux"
	dir := t.TempDir()

	var loadCalls []string
	w.seatLoad = func(goos, op, path string) error {
		loadCalls = append(loadCalls, goos+" "+op+" "+path)
		return nil
	}

	// 1. ping install --dry-run
	var out, errb strings.Builder
	code := run([]string{
		"ping", "install",
		"--as", "ada",
		"--every", "30s",
		"--dir", dir,
		"--dry-run",
	}, strings.NewReader(""), &out, &errb, w)
	require.Equal(t, 0, code, errb.String())
	assert.Contains(t, out.String(), "dry_run=true")
	assert.Empty(t, loadCalls)
	unitPath := filepath.Join(dir, friend.WakePingUnitFile("linux"))
	_, err := os.Stat(unitPath)
	assert.True(t, os.IsNotExist(err), "dry run wrote no file")

	// 2. ping install
	out.Reset()
	errb.Reset()
	code = run([]string{
		"ping", "install",
		"--as", "ada",
		"--every", "30s",
		"--dir", dir,
	}, strings.NewReader(""), &out, &errb, w)
	require.Equal(t, 0, code, errb.String())
	assert.Contains(t, out.String(), "written=true")
	assert.Contains(t, out.String(), "loaded=true")
	require.Len(t, loadCalls, 1)
	assert.Equal(t, "linux load "+unitPath, loadCalls[0])

	content, err := os.ReadFile(unitPath)
	require.NoError(t, err)
	assert.Contains(t, string(content), "--to-friends")
	assert.Contains(t, string(content), "--wake")

	// 3. ping uninstall --dry-run
	out.Reset()
	errb.Reset()
	code = run([]string{
		"ping", "uninstall",
		"--dir", dir,
		"--dry-run",
	}, strings.NewReader(""), &out, &errb, w)
	require.Equal(t, 0, code, errb.String())
	assert.Contains(t, out.String(), "present=true")
	assert.Contains(t, out.String(), "dry_run=true")
	require.Len(t, loadCalls, 1) // no new calls

	// 4. ping uninstall
	out.Reset()
	errb.Reset()
	code = run([]string{
		"ping", "uninstall",
		"--dir", dir,
	}, strings.NewReader(""), &out, &errb, w)
	require.Equal(t, 0, code, errb.String())
	assert.Contains(t, out.String(), "removed=true")
	require.Len(t, loadCalls, 2)
	assert.Equal(t, "linux unload "+unitPath, loadCalls[1])
	_, err = os.Stat(unitPath)
	assert.True(t, os.IsNotExist(err), "unit file was removed")
}

func TestWakePingValidation(t *testing.T) {
	t.Parallel()
	r := newRig(t, "ada")
	w := r.world()

	// --every requires --wake
	var out, errb strings.Builder
	code := run([]string{
		"ping",
		"--as", "ada",
		"--to-friends",
		"--every", "30s",
	}, strings.NewReader(""), &out, &errb, w)
	require.Equal(t, 2, code)
	assert.Contains(t, errb.String(), "--every requires --wake")

	// missing both --to and --to-friends
	out.Reset()
	errb.Reset()
	code = run([]string{
		"ping",
		"--as", "ada",
	}, strings.NewReader(""), &out, &errb, w)
	require.Equal(t, 2, code)
	assert.Contains(t, errb.String(), "--to is required")

	// both --to and --to-friends
	out.Reset()
	errb.Reset()
	code = run([]string{
		"ping",
		"--as", "ada",
		"--to", "bob",
		"--to-friends",
	}, strings.NewReader(""), &out, &errb, w)
	require.Equal(t, 2, code)
	assert.Contains(t, errb.String(), "--to and --to-friends cannot be used together")

	// --to-friends alone is no routine ping loop: it requires --wake
	out.Reset()
	errb.Reset()
	code = run([]string{
		"ping",
		"--as", "ada",
		"--to-friends",
	}, strings.NewReader(""), &out, &errb, w)
	require.Equal(t, 2, code)
	assert.Contains(t, errb.String(), "--to-friends requires --wake")

	// --every with one --to is no loop either
	out.Reset()
	errb.Reset()
	code = run([]string{
		"ping",
		"--as", "ada",
		"--to", "bob",
		"--wake",
		"--every", "30s",
	}, strings.NewReader(""), &out, &errb, w)
	require.Equal(t, 2, code)
	assert.Contains(t, errb.String(), "--every requires --to-friends")
}
