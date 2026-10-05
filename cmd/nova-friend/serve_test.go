package main

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/mas-bandwidth/nova-tools/internal/friend"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// serveRig is serve over the fake store and the friend rows ada (the
// coordinator), bob, cy and dee, with a clock that moves only when the loop
// sleeps. Each sleep, the daemons whose friend answers send a daemon-pong for
// every PING on their stream not yet answered: bob always, cy only before
// cyQuiet, dee never; replay names a friend that sends one more pong, for its
// first nonce, at replayAt.
type serveRig struct {
	*rig
	clock    time.Time
	answered map[string]int
	nonces   map[string][]string
}

func newServeRig(t *testing.T) *serveRig {
	t.Helper()
	return &serveRig{rig: newRig(t, "ada", "bob", "cy", "dee"), clock: start, answered: map[string]int{}, nonces: map[string][]string{}}
}

func (s *serveRig) pong(t *testing.T, from, nonce string) {
	t.Helper()
	_, err := (&bus.Bus{Store: s.store}).Send(context.Background(), bus.Message{From: from, To: []string{"ada"}, Subject: friend.DaemonPongSubject, Body: "daemon-pong " + nonce + "\n"})
	require.NoError(t, err)
}

func TestPingMarksAFriendDownAfterTenSecondsWithoutAPong(t *testing.T) {
	t.Parallel()
	s := newServeRig(t)
	cyQuiet, replayAt, end := start.Add(5*time.Second), start.Add(20*time.Second), start.Add(30*time.Second)
	w := s.world()
	n := 0
	w.now = func() time.Time { return s.clock }
	w.random = func() string { n++; return fmt.Sprintf("n%05d", n) }
	w.friends = func(context.Context, string) ([]string, error) { return []string{"ada", "bob", "cy", "dee"}, nil }
	var cancel context.CancelFunc
	w.signals = func(ctx context.Context) (context.Context, context.CancelFunc) {
		ctx, cancel = context.WithCancel(ctx)
		return ctx, cancel
	}
	w.sleep = func(_ context.Context, d time.Duration) {
		for _, f := range []string{"bob", "cy", "dee"} {
			es, err := s.store.Range(context.Background(), bus.StreamOf(f), "-", "+", 0)
			require.NoError(t, err)
			for _, e := range es[s.answered[f]:] {
				nonce, ok := strings.CutPrefix(e.Message().Subject, friend.PingPrefix)
				require.True(t, ok, "only PINGs reach a friend's stream")
				s.nonces[f] = append(s.nonces[f], nonce)
				if f == "bob" || (f == "cy" && s.clock.Before(cyQuiet)) {
					s.pong(t, f, nonce)
				}
			}
			s.answered[f] = len(es)
		}
		if s.clock.Equal(replayAt) {
			s.pong(t, "cy", s.nonces["cy"][0]) // a stale nonce brings no one up
		}
		s.clock = s.clock.Add(d)
		if !s.clock.Before(end) {
			cancel()
		}
	}
	var out, errb strings.Builder
	code := run([]string{"serve", "--as", "ada"}, strings.NewReader(""), &out, &errb, w)
	require.Equal(t, 0, code, errb.String())
	at := func(sec int) string { return start.Add(time.Duration(sec) * time.Second).UTC().Format(time.RFC3339) }
	assert.Equal(t, []string{
		"SERVE OK friends=bob,cy,dee every=1s down_after=10s",
		"SERVE UP friend=bob at=" + at(1),
		"SERVE UP friend=cy at=" + at(1),
		"SERVE DOWN friend=dee at=" + at(10) + " last_pong=never reason=\"no pong for 10s\"",
		"SERVE DOWN friend=cy at=" + at(15) + " last_pong=" + at(5) + " reason=\"no pong for 10s\"",
		"SERVE STOP interrupted",
	}, strings.Split(strings.TrimSpace(out.String()), "\n"), "one line per state change, never one per ping")
	for _, f := range []string{"bob", "cy", "dee"} {
		assert.Len(t, s.nonces[f], 30, "%s is pinged once a second, whatever it answers", f)
	}
	assert.Equal(t, 0, s.store.Len(bus.StreamOf("ada"))-len(s.nonces["bob"])-5-1, "the coordinator is pinged by no one; its stream holds only the pongs")
}

func TestServeRefusesWithoutItsNameOrRows(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		args []string
		rows func(context.Context, string) ([]string, error)
		says []string
	}{
		{"nothing given", []string{"serve"}, nil, []string{"--as is required"}},
		{"rows unreadable", []string{"serve", "--as", "ada", "--dry-run"}, func(context.Context, string) ([]string, error) { return nil, fmt.Errorf("--pg is required") }, []string{"the friend rows cannot be read", "--pg is required"}},
		{"no friend but me", []string{"serve", "--as", "ada", "--dry-run"}, func(context.Context, string) ([]string, error) { return []string{"ada"}, nil }, []string{"no friend row but ada", "nova-config friend add"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			r := newRig(t, "ada", "bob")
			w := r.world()
			w.friends = c.rows
			var out, errb strings.Builder
			code := run(c.args, strings.NewReader(""), &out, &errb, w)
			assert.Equal(t, 2, code)
			for _, s := range c.says {
				assert.Contains(t, errb.String(), s)
			}
		})
	}
}

func TestServeDryRunNamesTheFriendsItWouldPing(t *testing.T) {
	t.Parallel()
	r := newRig(t, "ada", "bob")
	w := r.world()
	w.friends = func(context.Context, string) ([]string, error) { return []string{"cy", "ada", "bob"}, nil }
	var out, errb strings.Builder
	code := run([]string{"serve", "--as", "ada", "--dry-run"}, strings.NewReader(""), &out, &errb, w)
	require.Equal(t, 0, code, errb.String())
	assert.Equal(t, "SERVE OK friends=bob,cy every=1s down_after=10s dry_run=true\n", out.String())
	assert.Zero(t, r.store.Trips, "a dry run opens no store")
}
