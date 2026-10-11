package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/bus"
	"github.com/mas-bandwidth/nova-tools/pkg/friend"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// wakeRig is ping --wake --every --to-friends over the fake store and a fake
// friends table: ada the coordinator; bob, cy and dee up; eve held; fay up with
// never-wake; gus down. A clock moves only when the loop sleeps; each sleep, the
// sessions that answer pong every wake PING on their stream not yet answered:
// bob always, cy until cyDeafAt, dee never (her daemon answers, her session does not).
type wakeRig struct {
	*rig
	clock    time.Time
	seen     map[string]int
	wakes    map[string]int
	cyDeafAt time.Time
}

func newWakeRig(t *testing.T) *wakeRig {
	t.Helper()
	r := newRig(t, "ada", "bob", "cy", "dee", "eve", "fay", "gus")
	r.store.Friends = []string{"ada", "bob", "cy", "dee", "eve", "fay", "gus"}
	return &wakeRig{rig: r, clock: start, seen: map[string]int{}, wakes: map[string]int{}}
}

func (s *wakeRig) send(t *testing.T, from, subject, body string) {
	t.Helper()
	_, err := (&bus.Bus{Store: s.store}).Send(context.Background(), bus.Message{From: from, To: []string{"ada"}, Subject: subject, Body: body + "\n"})
	require.NoError(t, err)
}

// Models: docs/SPEC-FRIEND.md, "The wake ping loop": every pass pings the friends
// the table holds up and not never-wake, waits for each session's pong, and tells
// the coordinator once per change of who is deaf.
func TestWakePingsEveryUpFriendAndReportsTheDeafOnes(t *testing.T) {
	t.Parallel()
	s := newWakeRig(t)
	every, within := 10*time.Minute, 30*time.Second
	s.cyDeafAt = start.Add(15 * time.Minute) // answers the first pass, deaf from the second
	end := start.Add(35 * time.Minute)       // passes at 0, 10, 20 and 30 minutes
	w := s.world()
	n := 0
	w.now = func() time.Time { return s.clock }
	w.random = func() string { n++; return fmt.Sprintf("n%05d", n) }
	w.friends = func(context.Context, string) ([]friend.WakeRow, string, error) {
		return []friend.WakeRow{{Name: "ada", Status: "up"}, {Name: "bob", Status: "up"}, {Name: "cy", Status: "up"}, {Name: "dee", Status: "up"},
			{Name: "eve", Status: "held"}, {Name: "fay", Status: "up"}, {Name: "gus", Status: "down"}}, "ada", nil
	}
	var cancel context.CancelFunc
	w.signals = func(ctx context.Context) (context.Context, context.CancelFunc) {
		ctx, cancel = context.WithCancel(ctx)
		return ctx, cancel
	}
	w.sleep = func(_ context.Context, d time.Duration) {
		for _, f := range []string{"bob", "cy", "dee", "eve", "fay", "gus"} {
			es, err := s.store.Range(context.Background(), bus.StreamOf(f), "-", "+", 0)
			require.NoError(t, err)
			for _, e := range es[s.seen[f]:] {
				nonce, ok := strings.CutPrefix(e.Message().Subject, friend.PingPrefix)
				require.True(t, ok, "only PINGs reach a friend's stream")
				require.True(t, friend.IsWake(e.Message().Body), "every ping of the loop is a wake ping")
				s.wakes[f]++
				switch {
				case f == "bob" || (f == "cy" && s.clock.Before(s.cyDeafAt)):
					s.send(t, f, friend.PongSubject, friend.PongLine(nonce, 0, 0, 1))
				case f == "dee":
					s.send(t, f, friend.DaemonPongSubject, "daemon-pong "+nonce) // the daemon answers; the session never does
				}
			}
			s.seen[f] = len(es)
		}
		s.clock = s.clock.Add(d)
		if !s.clock.Before(end) {
			cancel()
		}
	}
	var out, errb strings.Builder
	code := run([]string{"ping", "--as", "ada", "--wake", "--to-friends", "--every", every.String(), "--within", within.String(), "--never-wake", "fay", "--server", "sprint.test:1"}, strings.NewReader(""), &out, &errb, w)
	require.Equal(t, 0, code, errb.String())

	for _, f := range []string{"bob", "cy", "dee"} {
		assert.Equal(t, 4, s.wakes[f], "%s is wake-pinged each pass", f)
	}
	for _, f := range []string{"eve", "fay", "gus"} {
		assert.Zero(t, s.wakes[f], "%s is held, never-wake or down: never pinged", f)
	}

	var notes []bus.Message
	es, err := s.store.Range(context.Background(), bus.StreamOf("ada"), "-", "+", 0)
	require.NoError(t, err)
	for _, e := range es {
		if m := e.Message(); m.From == "ada" {
			notes = append(notes, m)
		}
	}
	require.Len(t, notes, 2, "one note per change of who is deaf, none while it stays the same: dee, then cy and dee")
	assert.Equal(t, "ada", notes[0].To[0])
	assert.Contains(t, notes[0].Subject, "deaf: dee")
	assert.NotContains(t, notes[0].Subject+notes[0].Body, "eve")
	assert.NotContains(t, notes[0].Subject+notes[0].Body, "fay")
	assert.NotContains(t, notes[0].Subject+notes[0].Body, "gus")
	assert.Contains(t, notes[1].Subject, "deaf: cy,dee")
	assert.Equal(t, bus.KindBlocker, notes[1].Kind)
	assert.Equal(t, 2, strings.Count(out.String(), "WAKE DEAF "), "said once per change, never once a pass")
	assert.Contains(t, out.String(), "WAKE DEAF friends=dee ")
	assert.Contains(t, out.String(), "WAKE DEAF friends=cy,dee ")
}

func TestWakeLoopRefusalsNameEveryProblem(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		args []string
		says []string
	}{
		{"neither target", []string{"ping", "--as", "ada"}, []string{"--to is required", "--to-friends"}},
		{"both targets", []string{"ping", "--as", "ada", "--to", "bob", "--to-friends", "--wake"}, []string{"--to and --to-friends name the same thing twice"}},
		{"no wake", []string{"ping", "--as", "ada", "--to-friends"}, []string{"wants --wake", "nova-friend serve"}},
		{"loop flags alone", []string{"ping", "--as", "ada", "--to", "bob", "--every", "1m", "--within", "1s", "--never-wake", "x"}, []string{"--every is the wake loop's", "--within is the wake loop's", "--never-wake is the wake loop's"}},
		{"bad durations", []string{"ping", "--as", "ada", "--wake", "--to-friends", "--every", "0s", "--within", "-1s"}, []string{"--every wants a positive duration", "--within wants a positive duration"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got := newRig(t, "ada", "bob").cli().Do(t, c.args...).Exit(2).Err("REFUSED")
			for _, s := range c.says {
				got.Err(s)
			}
		})
	}
}

func TestWakeLoopOnePassWithoutEveryAndTheDryRun(t *testing.T) {
	t.Parallel()
	s := newWakeRig(t)
	w := s.world()
	w.friends = func(context.Context, string) ([]friend.WakeRow, string, error) {
		return []friend.WakeRow{{Name: "bob", Status: "up"}, {Name: "eve", Status: "held"}}, "", nil
	}
	var out, errb strings.Builder
	require.Equal(t, 0, run([]string{"ping", "--as", "ada", "--wake", "--to-friends", "--dry-run"}, strings.NewReader(""), &out, &errb, w), errb.String())
	assert.Contains(t, out.String(), "friends=bob")
	assert.Zero(t, s.store.Len(bus.StreamOf("bob")), "a dry run sends nothing")
	out.Reset()
	w.friends = func(context.Context, string) ([]friend.WakeRow, string, error) {
		return nil, "", fmt.Errorf("no server")
	}
	require.Equal(t, 2, run([]string{"ping", "--as", "ada", "--wake", "--to-friends"}, strings.NewReader(""), &out, &errb, w))
	assert.Contains(t, errb.String(), "the friends table cannot be read: no server")
}

func TestPingInstallWritesAndLoadsTheAgentAndUninstallRemovesIt(t *testing.T) {
	t.Parallel()
	r := newRig(t, "ada")
	cli := r.cli()
	cli.Do(t, "ping-install", "--as", "ada", "--every", "10m", "--never-wake", "alex", "--dry-run").Exit(0).Out("label=com.nova.friend-wake-ping-ada", "ping --as ada --wake --to-friends --every 10m0s")
	assert.Empty(t, r.launchctl, "a dry run loads nothing")
	cli.Do(t, "ping-install", "--as", "ada", "--every", "10m", "--never-wake", "alex").Exit(0).Out("label=com.nova.friend-wake-ping-ada", "launchctl bootstrap gui/501")
	plist := filepath.Join(r.home, "Library", "LaunchAgents", "com.nova.friend-wake-ping-ada.plist")
	raw, err := os.ReadFile(plist)
	require.NoError(t, err)
	assert.Contains(t, string(raw), "<string>--to-friends</string>")
	assert.Contains(t, string(raw), "<string>alex</string>")
	cli.Do(t, "ping-uninstall", "--as", "ada").Exit(0).Out("launchctl bootout gui/501/com.nova.friend-wake-ping-ada")
	assert.NoFileExists(t, plist)
	newRig(t, "ada").cli().Do(t, "ping-install", "--as", "ada", "--every", "0s").Exit(2).Err("--every wants a positive duration")
}
