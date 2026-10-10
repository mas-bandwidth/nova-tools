package main

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/bus"
	"github.com/mas-bandwidth/nova-tools/pkg/friend"
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
	r := newRig(t, "ada", "bob", "cy", "dee")
	r.store.Friends = []string{"ada", "bob", "cy", "dee"}
	return &serveRig{rig: r, clock: start, answered: map[string]int{}, nonces: map[string][]string{}}
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
		name    string
		args    []string
		friends []string
		fail    error
		says    []string
	}{
		{"nothing given", []string{"serve"}, []string{"ada", "bob"}, nil, []string{"--as is required"}},
		{"rows unreadable", []string{"serve", "--as", "ada", "--dry-run"}, []string{"ada", "bob"}, fmt.Errorf("connection refused"), []string{"connection refused"}},
		{"no friend but me", []string{"serve", "--as", "ada", "--dry-run"}, []string{"ada"}, nil, []string{"no friend row but ada", "nova-config friend add"}},
		{"no friend but me, the loop", []string{"serve", "--as", "ada"}, []string{"ada"}, nil, []string{"no friend row but ada"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			r := newRig(t, "ada", "bob")
			r.store.Friends = c.friends
			r.store.Fail = c.fail
			w := r.world()
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
	r := newRig(t, "cy", "ada", "bob", "m1")
	r.store.Friends = []string{"cy", "ada", "bob"} // m1 is a machine row, never pinged
	w := r.world()
	var out, errb strings.Builder
	code := run([]string{"serve", "--as", "ada", "--dry-run"}, strings.NewReader(""), &out, &errb, w)
	require.Equal(t, 0, code, errb.String())
	assert.Equal(t, "SERVE OK friends=bob,cy every=1s down_after=10s dry_run=true\n", out.String())
	for _, n := range []string{"ada", "bob", "cy", "m1"} {
		assert.Zero(t, r.store.Len(bus.StreamOf(n)), "a dry run sends nothing")
	}
}
