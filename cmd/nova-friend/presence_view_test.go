package main

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/hook"
	"github.com/mas-bandwidth/nova-tools/pkg/bus"
	"github.com/mas-bandwidth/nova-tools/pkg/bus/bustest"
	"github.com/mas-bandwidth/nova-tools/pkg/friend"
	"github.com/mas-bandwidth/nova-tools/pkg/testkit"
	"github.com/stretchr/testify/require"
)

// TestPresenceViewReconcilesTheProofDomains pins the reconciled view
// (docs/SPEC-FRIEND.md, the proof domains): a daemon down, fifteen recent bus
// messages and a proven hook read as reachable by-push (on the hook alone),
// native wake not proven (no session pong), and one domain line per proof
// domain, each with a source and an age, none merged into one boolean.
func TestPresenceViewReconcilesTheProofDomains(t *testing.T) {
	t.Parallel()

	now := start
	store := bustest.NewFake(start, "ada", "bob")
	home := t.TempDir()
	state := friend.DefaultStateDir(home, "bob")

	// the daemon is down: a status file older than DaemonStale
	require.NoError(t, friend.WriteStatus(state, friend.Status{
		Friend:  "bob",
		Harness: "opencode",
		At:      now.Add(-time.Minute),
	}))

	// fifteen recent real messages from the session
	b := &bus.Bus{Store: store}
	for i := 0; i < 15; i++ {
		_, err := b.Send(context.Background(), bus.Message{From: "bob", To: []string{"ada"}, Subject: "reply", Body: "done"})
		require.NoError(t, err)
	}

	w := world{
		getenv: func(string) string { return "" },
		home:   home,
		now:    func() time.Time { return now },
		open:   func(context.Context, string) (bus.Store, func(), error) { return store, func() {}, nil },
		presenceHook: func(context.Context, bus.Store, string) (string, time.Time, error) {
			return hook.PushProven, now.Add(-time.Minute), nil
		},
	}

	cli := testkit.Main(func(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
		return run(args, stdin, stdout, stderr, w)
	})

	ran := cli.Do(t, "presence", "--as", "bob", "--redis", "store.test:6379").Exit(0)
	ran.Out(
		"PRESENCE OK friend=bob",
		"reachable=by-push",
		"reachable_by=hook",
		"native_wake=not-proven",
		"native_wake_by=pong",
		"PRESENCE DOMAIN domain=daemon source=",
		"PRESENCE DOMAIN domain=bus source=",
		"PRESENCE DOMAIN domain=hook source=",
		"PRESENCE DOMAIN domain=pong source=",
		"verdict=stale",
		"verdict=active",
		"verdict=proven",
		"verdict=none",
		"count=15",
	)
	// four domain lines, each carrying an age
	require.Equal(t, 4, strings.Count(ran.Stdout, " age="), ran)
}
