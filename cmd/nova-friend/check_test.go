package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/bus"
	"github.com/mas-bandwidth/nova-tools/pkg/friend"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCheckSaysBrokenWhenTheTableShowsUpButNoTurnWasAnswered(t *testing.T) {
	t.Parallel()
	r := newRig(t, "ada", "bob")
	cli := r.cli()
	state := friend.DefaultStateDir(r.home, "bob")
	require.NoError(t, friend.WriteStatus(state, friend.Status{
		Friend:        "bob",
		Harness:       "opencode",
		At:            start,
		Session:       friend.SessionBroken,
		SessionReason: "provider quota exceeded",
		BrokenAt:      start,
	}))
	shownFile := filepath.Join(t.TempDir(), "shown.json")
	require.NoError(t, os.WriteFile(shownFile, []byte(`{"bob": {"state": "up", "working": 1}}`), 0o644))

	cli.Do(t, "check", "--as", "ada", "--shown", shownFile, "bob").Exit(1).
		Out("CHECK DAEMON friend=bob",
			"CHECK HARNESS friend=bob harness=opencode route=push",
			"CHECK BUS friend=bob real_since=0 last_real=-",
			"CHECK WORK friend=bob inbox=0 outbox=0 newest_outbox=- newest_at=-",
			`CHECK VERDICT friend=bob verdict=broken shown=up/1 why="untrue: shown up/1, session broken: provider quota exceeded"`,
			"CHECK OK friends=1 ok=0 broken=1 deaf=0 silent=0 down=0 untrue=0")
}

func TestCheckSaysDeafWhenDeliveriesSucceedAndNothingComesBack(t *testing.T) {
	t.Parallel()
	r := newRig(t, "ada", "bob")
	cli := r.cli()
	state := friend.DefaultStateDir(r.home, "bob")
	require.NoError(t, friend.WriteStatus(state, friend.Status{
		Friend:     "bob",
		Harness:    "opencode",
		At:         start,
		Connection: friend.Connected,
	}))
	// deliver log has exit=0 (deliveries succeed)
	require.NoError(t, friend.Record(state, "2026-10-04T02:50:00Z subject=work messages=1 took=3s exit=0 acked=true"))

	cli.Do(t, "check", "--as", "ada", "bob").Exit(1).
		Out("CHECK VERDICT friend=bob verdict=deaf shown=- why=\"deliveries succeed but no session pong or real message came back in the window\"",
			"CHECK OK friends=1 ok=0 broken=0 deaf=1 silent=0 down=0 untrue=0")
}

func TestCheckSaysOkForALiveFriend(t *testing.T) {
	t.Parallel()
	r := newRig(t, "ada", "bob")
	cli := r.cli()
	state := friend.DefaultStateDir(r.home, "bob")
	r.launchctlOut = "12345 0 com.nova.friend-bob\n"
	require.NoError(t, friend.WriteStatus(state, friend.Status{
		Friend:     "bob",
		Harness:    "opencode",
		At:         start,
		Connection: friend.Connected,
	}))
	require.NoError(t, friend.WritePresence(state, friend.PresenceStatus{
		Friend:    "bob",
		Presence:  friend.PresenceUp,
		At:        start,
		LastHeard: start,
	}))
	require.NoError(t, friend.WritePong(state, friend.Pong{
		Nonce: "n0",
		At:    start,
	}))
	require.NoError(t, friend.Record(state, "2026-10-04T02:50:00Z subject=work messages=1 took=3s exit=0 acked=true"))

	b := &bus.Bus{Store: r.store}
	_, err := b.Send(context.Background(), bus.Message{
		From:    "bob",
		To:      []string{"ada"},
		Subject: "reply",
		Body:    "done",
	})
	require.NoError(t, err)

	cli.Do(t, "check", "--as", "ada", "bob").Exit(0).
		Out("CHECK VERDICT friend=bob verdict=ok shown=- why=live",
			"CHECK OK friends=1 ok=1 broken=0 deaf=0 silent=0 down=0 untrue=0")
}

func TestCheckExitsOneOnAnyVerdictButOk(t *testing.T) {
	t.Parallel()

	// Broken exits 1
	t.Run("broken exits 1", func(t *testing.T) {
		t.Parallel()
		r := newRig(t, "ada", "bob")
		state := friend.DefaultStateDir(r.home, "bob")
		require.NoError(t, friend.WriteStatus(state, friend.Status{
			Friend:   "bob",
			Session:  friend.SessionBroken,
			BrokenAt: start,
		}))
		r.cli().Do(t, "check", "--as", "ada", "bob").Exit(1)
	})

	// Deaf exits 1
	t.Run("deaf exits 1", func(t *testing.T) {
		t.Parallel()
		r := newRig(t, "ada", "bob")
		state := friend.DefaultStateDir(r.home, "bob")
		require.NoError(t, friend.WriteStatus(state, friend.Status{Friend: "bob"}))
		require.NoError(t, friend.Record(state, "2026-10-04T02:50:00Z subject=work exit=0"))
		r.cli().Do(t, "check", "--as", "ada", "bob").Exit(1)
	})

	// Down exits 1
	t.Run("down exits 1", func(t *testing.T) {
		t.Parallel()
		r := newRig(t, "ada", "bob")
		r.cli().Do(t, "check", "--as", "ada", "bob").Exit(1).
			Out("verdict=down")
	})

	// Untrue exits 1: shown up, the facts say asleep
	t.Run("untrue exits 1", func(t *testing.T) {
		t.Parallel()
		r := newRig(t, "ada", "bob")
		state := friend.DefaultStateDir(r.home, "bob")
		r.launchctlOut = "12345 0 com.nova.friend-bob\n"
		require.NoError(t, friend.WriteStatus(state, friend.Status{Friend: "bob", At: start}))
		require.NoError(t, friend.WritePresence(state, friend.PresenceStatus{Friend: "bob", Presence: "asleep", At: start}))
		require.NoError(t, friend.WritePong(state, friend.Pong{Nonce: "n0", At: start}))
		require.NoError(t, friend.Record(state, "2026-10-04T02:50:00Z subject=work exit=0"))
		shownFile := filepath.Join(t.TempDir(), "shown.json")
		require.NoError(t, os.WriteFile(shownFile, []byte(`{"bob": {"state": "up", "working": 1}}`), 0o644))
		r.cli().Do(t, "check", "--as", "ada", "--shown", shownFile, "bob").Exit(1).
			Out(`verdict=untrue shown=up/1 why="shown up/1, facts say asleep"`)
	})

	// Shown up and down by presence: the verdict stays down, the why says untrue
	t.Run("shown up and down keeps down and says untrue", func(t *testing.T) {
		t.Parallel()
		r := newRig(t, "ada", "bob")
		shownFile := filepath.Join(t.TempDir(), "shown.json")
		require.NoError(t, os.WriteFile(shownFile, []byte(`{"bob": {"state": "up", "working": 1}}`), 0o644))
		r.cli().Do(t, "check", "--as", "ada", "--shown", shownFile, "bob").Exit(1).
			Out(`verdict=down shown=up/1 why="untrue: shown up/1, down by presence"`)
	})

	// OK exits 0
	t.Run("ok exits 0", func(t *testing.T) {
		t.Parallel()
		r := newRig(t, "ada", "bob")
		state := friend.DefaultStateDir(r.home, "bob")
		r.launchctlOut = "12345 0 com.nova.friend-bob\n"
		require.NoError(t, friend.WriteStatus(state, friend.Status{Friend: "bob", At: start}))
		require.NoError(t, friend.WritePresence(state, friend.PresenceStatus{Friend: "bob", Presence: friend.PresenceUp, At: start, LastHeard: start}))
		require.NoError(t, friend.WritePong(state, friend.Pong{Nonce: "n0", At: start}))
		require.NoError(t, friend.Record(state, "2026-10-04T02:50:00Z subject=work exit=0"))
		b := &bus.Bus{Store: r.store}
		_, err := b.Send(context.Background(), bus.Message{From: "bob", To: []string{"ada"}, Subject: "reply", Body: "ok"})
		require.NoError(t, err)
		r.cli().Do(t, "check", "--as", "ada", "bob").Exit(0).
			Out("verdict=ok")
	})
}

func TestCheckJSONCarriesEveryFact(t *testing.T) {
	t.Parallel()
	r := newRig(t, "ada", "bob")
	state := friend.DefaultStateDir(r.home, "bob")
	r.launchctlOut = "12345 0 com.nova.friend-bob\n"
	require.NoError(t, friend.WriteStatus(state, friend.Status{
		Friend:     "bob",
		Harness:    "opencode",
		At:         start,
		Connection: friend.Connected,
		Challenge:  friend.Quiet,
	}))
	require.NoError(t, friend.WritePresence(state, friend.PresenceStatus{
		Friend:    "bob",
		Presence:  friend.PresenceUp,
		At:        start,
		LastHeard: start,
	}))
	require.NoError(t, friend.WritePong(state, friend.Pong{
		Nonce: "n0",
		At:    start,
	}))
	require.NoError(t, friend.Record(state, "2026-10-04T02:50:00Z subject=work exit=0"))

	b := &bus.Bus{Store: r.store}
	_, err := b.Send(context.Background(), bus.Message{
		From:    "bob",
		To:      []string{"ada"},
		Subject: "work-ack",
		Body:    "done",
	})
	require.NoError(t, err)

	ran := r.cli().Do(t, "check", "--as", "ada", "--json", "bob").Exit(0)
	var report friend.CheckReport
	require.NoError(t, json.Unmarshal([]byte(ran.Stdout), &report))

	require.Len(t, report.Friends, 1)
	fc := report.Friends[0]
	assert.Equal(t, "bob", fc.Friend)

	// Daemon facts
	assert.Equal(t, "bob", fc.Daemon.Friend)
	assert.Equal(t, "loaded", fc.Daemon.Agent)
	assert.Equal(t, "12345", fc.Daemon.PID)
	assert.Equal(t, "ok", fc.Daemon.Status)
	assert.Equal(t, friend.Connected, fc.Daemon.Connection)
	assert.Equal(t, friend.Quiet, fc.Daemon.Challenge)
	assert.NotEmpty(t, fc.Daemon.PongAge)
	assert.Equal(t, friend.PresenceUp, fc.Daemon.Presence)
	assert.NotEmpty(t, fc.Daemon.SeenAge)

	// Harness facts
	assert.Equal(t, "bob", fc.Harness.Friend)
	assert.Equal(t, "opencode", fc.Harness.Harness)
	assert.Equal(t, "push", fc.Harness.Route)
	assert.Equal(t, "2026-10-04T02:50:00Z", fc.Harness.Last)
	assert.Equal(t, "0", fc.Harness.LastExit)
	assert.Equal(t, 0, fc.Harness.FailedOfLast20)
	assert.Equal(t, 0, fc.Harness.Deferred)
	assert.Equal(t, "-", fc.Harness.Broken)
	assert.Equal(t, "-", fc.Harness.Reason)

	// Bus facts
	assert.Equal(t, "bob", fc.Bus.Friend)
	assert.Equal(t, 1, fc.Bus.RealSince)
	assert.NotEmpty(t, fc.Bus.LastReal)

	// Work facts
	assert.Equal(t, "bob", fc.Work.Friend)
	assert.Equal(t, 0, fc.Work.Inbox)
	assert.Equal(t, 0, fc.Work.Outbox)
	assert.Equal(t, "-", fc.Work.NewestOutbox)
	assert.Equal(t, "-", fc.Work.NewestAt)

	// Verdict facts
	assert.Equal(t, "bob", fc.Verdict.Friend)
	assert.Equal(t, "ok", fc.Verdict.Verdict)
	assert.Equal(t, "-", fc.Verdict.Shown)
	assert.Equal(t, "live", fc.Verdict.Why)

	// Summary
	assert.Equal(t, 1, report.Summary.Friends)
	assert.Equal(t, 1, report.Summary.OK)
	assert.Equal(t, 0, report.Summary.Broken)
	assert.Equal(t, 0, report.Summary.Deaf)
	assert.Equal(t, 0, report.Summary.Silent)
	assert.Equal(t, 0, report.Summary.Down)
	assert.Equal(t, 0, report.Summary.Untrue)
}

func TestCheckAppliesTheWindowToTheLog(t *testing.T) {
	t.Parallel()
	log := []string{
		"2026-10-03T01:00:00Z subject=work messages=1 exit=1",
		"2026-10-03T01:10:00Z subject=work messages=1 exit=1",
		"2026-10-04T02:50:00Z subject=work messages=1 exit=0",
	}
	for _, c := range []struct {
		name, since, want string
	}{
		{"a wide window sees two failures and a success", "48h", "failed_of_last20=2"},
		{"a narrow window sees only the recent success", "1h", "failed_of_last20=0"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			r := newRig(t, "ada", "bob")
			state := friend.DefaultStateDir(r.home, "bob")
			require.NoError(t, friend.WriteStatus(state, friend.Status{Friend: "bob", Harness: "opencode", At: start}))
			for _, l := range log {
				require.NoError(t, friend.Record(state, l))
			}
			r.cli().Do(t, "check", "--as", "ada", "--since", c.since, "bob").Out(c.want)
		})
	}

	t.Run("failures older than the window do not make a friend broken", func(t *testing.T) {
		t.Parallel()
		r := newRig(t, "ada", "bob")
		state := friend.DefaultStateDir(r.home, "bob")
		require.NoError(t, friend.WriteStatus(state, friend.Status{Friend: "bob", Harness: "opencode", At: start}))
		require.NoError(t, friend.Record(state, "2026-10-03T01:00:00Z subject=work messages=1 exit=1"))
		ran := r.cli().Do(t, "check", "--as", "ada", "--since", "1h", "bob").Exit(1)
		assert.NotContains(t, ran.Stdout, "verdict=broken")
	})

	t.Run("every failure inside the window is broken", func(t *testing.T) {
		t.Parallel()
		r := newRig(t, "ada", "bob")
		state := friend.DefaultStateDir(r.home, "bob")
		require.NoError(t, friend.WriteStatus(state, friend.Status{Friend: "bob", Harness: "opencode", At: start}))
		require.NoError(t, friend.Record(state, "2026-10-04T02:40:00Z subject=work messages=1 exit=1"))
		require.NoError(t, friend.Record(state, "2026-10-04T02:50:00Z subject=work messages=1 exit=1"))
		r.cli().Do(t, "check", "--as", "ada", "--since", "1h", "bob").Exit(1).
			Out(`verdict=broken shown=- why="every delivery in the window failed (2 of 2)"`)
	})

	t.Run("one failure beside a success is not broken", func(t *testing.T) {
		t.Parallel()
		r := newRig(t, "ada", "bob")
		state := friend.DefaultStateDir(r.home, "bob")
		require.NoError(t, friend.WriteStatus(state, friend.Status{Friend: "bob", Harness: "opencode", At: start}))
		require.NoError(t, friend.Record(state, "2026-10-04T02:40:00Z subject=work messages=1 exit=1"))
		require.NoError(t, friend.Record(state, "2026-10-04T02:50:00Z subject=work messages=1 exit=0"))
		ran := r.cli().Do(t, "check", "--as", "ada", "--since", "1h", "bob").Exit(1)
		assert.NotContains(t, ran.Stdout, "verdict=broken")
	})
}

// The example in the help runs as written: every word after the tool's name is
// an argument of the run.
func TestCheckHelpExampleRunsAsWritten(t *testing.T) {
	t.Parallel()
	r := newRig(t, "ada", "bob")
	cli := r.cli()
	help := cli.Do(t, "check", "-h").Exit(0).Stdout
	var example string
	for _, line := range strings.Split(help, "\n") {
		if rest, ok := strings.CutPrefix(line, "example: nova-friend "); ok {
			example = rest
		}
	}
	require.Equal(t, "check --as ada bob", example)

	cli.Do(t, strings.Fields(example)...).Exit(1).
		Out("CHECK DAEMON friend=bob", "CHECK VERDICT friend=bob verdict=down",
			"CHECK OK friends=1 ok=0 broken=0 deaf=0 silent=0 down=1 untrue=0")
}
