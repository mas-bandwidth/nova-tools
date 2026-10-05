package friend

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// grokHouse is a grok home with one open session in dir (pid 94410) and
// the wake file its monitor tails; the listing is what ps shows for it:
// the tail under a shell under the TUI.
func grokHouse(t *testing.T) (home, dir, wake, listing string) {
	t.Helper()
	home = t.TempDir()
	dir = filepath.Join(t.TempDir(), "bob")
	require.NoError(t, os.Mkdir(dir, 0o755))
	wake = filepath.Join(home, "long-running-background-tasks", "bob_bus.wake")
	require.NoError(t, os.MkdirAll(filepath.Dir(wake), 0o755))
	require.NoError(t, os.WriteFile(wake, []byte("INBOX NOTE id=old\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(home, "active_sessions.json"),
		[]byte(`[{"session_id":"01a1","pid":94410,"cwd":`+strconv.Quote(dir)+`,"opened_at":"2026-10-04T02:02:49Z"}]`), 0o644))
	listing = "38163 38155 tail -n 0 -F " + wake + "\n38155 94410 /bin/zsh -c snap=$(command cat <&3)\n94410  7509 grok\n    1     0 /sbin/launchd\n"
	return home, dir, wake, listing
}

func TestGrokDeliversOneMonitorEventLineIntoTheOpenSessionsWakeFile(t *testing.T) {
	t.Parallel()
	home, dir, wake, listing := grokHouse(t)
	var record strings.Builder
	fe := &fakeExec{out: listing}
	d, err := NewDeliverer("grok", dir, "", fe.run, &record)
	require.NoError(t, err)
	d.(*Grok).Home = home
	_, passive := d.(interface{ Passive() })
	assert.False(t, passive, "grok can be delivered into")

	exit, err := d.Deliver(context.Background(), "PING n1\nrun: nova-friend pong --as bob --nonce n1\n")
	require.NoError(t, err)
	assert.Equal(t, 0, exit)
	require.Len(t, fe.calls, 1)
	assert.Equal(t, []string{dir, "ps", "-axww", "-o", "pid=,ppid=,args="}, fe.calls[0], "the listing comes from ps, never a shell")
	got, err := os.ReadFile(wake)
	require.NoError(t, err)
	assert.Equal(t, "INBOX NOTE id=old\nnova-friend: PING n1 ⏎ run: nova-friend pong --as bob --nonce n1\n", string(got), "appended as one line; what was there stays")
	assert.Contains(t, record.String(), "one monitor event appended to "+wake)

	// a named wake file is the one written, and must be the one tailed
	fe = &fakeExec{out: listing}
	d, err = NewDeliverer("grok", dir, wake, fe.run, nil)
	require.NoError(t, err)
	d.(*Grok).Home = home
	exit, err = d.Deliver(context.Background(), "hello")
	require.NoError(t, err)
	assert.Equal(t, 0, exit)
	d, _ = NewDeliverer("grok", dir, filepath.Join(home, "other.wake"), fe.run, nil)
	d.(*Grok).Home = home
	_, err = d.Deliver(context.Background(), "hello")
	assert.ErrorContains(t, err, "runs no monitor over "+filepath.Join(home, "other.wake"))

	// no home at all: nothing is open
	d, _ = NewDeliverer("grok", dir, "", fe.run, nil)
	d.(*Grok).Home = filepath.Join(home, "none")
	_, err = d.Deliver(context.Background(), "hello")
	assert.ErrorContains(t, err, "no grok session is open")
}

func TestWakeOfRefusesWhatIsNotAnOpenSessionWithAMonitor(t *testing.T) {
	t.Parallel()
	active := `[{"session_id":"01a1","pid":94410,"cwd":"/w/bob"},{"session_id":"01a2","pid":500,"cwd":"/w/ada"}]`
	listing := "38163 38155 tail -n 0 -F /h/bob.wake\n38155 94410 /bin/zsh -c x\n94410 7509 grok\n600 500 tail -n 0 -F /h/ada.wake\n500 1 grok\n"

	file, err := WakeOf(active, listing, "/w/bob/", "")
	require.NoError(t, err)
	assert.Equal(t, "/h/bob.wake", file, "the tail under this session's TUI, the directory cleaned")
	file, err = WakeOf(active, listing, "/w/ada", "")
	require.NoError(t, err)
	assert.Equal(t, "/h/ada.wake", file, "another session's tail is not this one's")

	_, err = WakeOf(active, listing, "/w/zed", "")
	assert.EqualError(t, err, "no grok session is open in /w/zed; start grok there")
	_, err = WakeOf(active, "38163 38155 tail -n 0 -F /h/bob.wake\n38155 1 /bin/zsh\n", "/w/bob", "")
	assert.EqualError(t, err, "no grok session is open in /w/bob; start grok there", "a pid the listing lacks is a stale record")
	_, err = WakeOf(active, "94410 7509 grok\n", "/w/bob", "")
	assert.EqualError(t, err, "the grok session in /w/bob runs no monitor over <file>.wake; in that session: monitor `tail -n 0 -F <file>.wake`")
	_, err = WakeOf(active, listing, "/w/bob", "/h/x.wake")
	assert.EqualError(t, err, "the grok session in /w/bob runs no monitor over /h/x.wake; in that session: monitor `tail -n 0 -F /h/x.wake`")
	_, err = WakeOf(`{}`, listing, "/w/bob", "")
	assert.ErrorContains(t, err, "active_sessions.json: not a JSON list")
}

func TestGrokRefusesAmbiguousMonitorWakePathsWithoutWriting(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"relative", "space before suffix", "truncated absolute", "named truncated absolute", "named relative", "named space"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			home, dir, wake, listing := grokHouse(t)
			monitor, named := wake, ""
			switch name {
			case "relative":
				monitor = "relative.wake"
			case "space before suffix":
				monitor = filepath.Join(home, "two words.wake")
			case "truncated absolute", "named truncated absolute":
				monitor = wake + " trailing"
				if name == "named truncated absolute" {
					named = wake
				}
			case "named relative":
				named = "relative.wake"
			case "named space":
				named = filepath.Join(home, "two words.wake")
			}
			fe := &fakeExec{out: strings.ReplaceAll(listing, wake, monitor)}
			d, err := NewDeliverer("grok", dir, named, fe.run, nil)
			require.NoError(t, err)
			d.(*Grok).Home = home
			_, err = d.Deliver(context.Background(), "must not append")
			require.EqualError(t, err, "the monitor's wake path must be absolute")
			got, err := os.ReadFile(wake)
			require.NoError(t, err)
			assert.Equal(t, "INBOX NOTE id=old\n", string(got))
			require.Len(t, fe.calls, 1)
			assert.Equal(t, []string{dir, "ps", "-axww", "-o", "pid=,ppid=,args="}, fe.calls[0], "only the injected read-only listing runs; no delivery process starts")
		})
	}
}

func TestGrokDeliveryIsATurnInTheOpenWindow(t *testing.T) {
	t.Parallel()
	home, dir, wake, listing := grokHouse(t)
	fe := &fakeExec{out: listing}
	d, err := NewDeliverer("grok", dir, "", fe.run, nil)
	require.NoError(t, err)
	g := d.(*Grok)
	g.Home = home

	exit, err := g.Deliver(context.Background(), "turn")
	require.NoError(t, err)
	assert.Equal(t, 0, exit, "a line under a running tail is the turn")
	got, err := os.ReadFile(wake)
	require.NoError(t, err)
	assert.Equal(t, "INBOX NOTE id=old\nnova-friend: turn\n", string(got))

	route, line, err := g.Route(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "push", route, "a tail under the window's pid is route=push")
	assert.Equal(t, GrokMonitorLine(wake), line)

	// defer without a monitor: the window is open, nothing tails, nothing is written
	fe.out = "94410  7509 grok\n    1     0 /sbin/launchd\n"
	exit, err = g.Deliver(context.Background(), "must stay pending")
	assert.Equal(t, 0, exit)
	var deferred Deferred
	require.ErrorAs(t, err, &deferred)
	assert.Contains(t, deferred.Reason, "runs no monitor")
	assert.Contains(t, deferred.Reason, GrokMonitorLine(""))
	after, err := os.ReadFile(wake)
	require.NoError(t, err)
	assert.Equal(t, string(got), string(after), "a defer writes nothing; the message is not dropped")

	route, line, err = g.Route(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "defer", route)
	assert.Equal(t, GrokMonitorLine(""), line)
	assert.Equal(t, line, GrokInstallLine("grok", ""))
	assert.Equal(t, GrokMonitorLine(wake), GrokInstallLine("grok", wake))
	assert.Empty(t, GrokInstallLine("opencode", wake))

	g.Home = filepath.Join(home, "none")
	exit, err = g.Deliver(context.Background(), "also pending")
	assert.Equal(t, 0, exit)
	require.ErrorAs(t, err, &deferred)
	assert.Contains(t, deferred.Reason, "no grok session is open")
	assert.Contains(t, deferred.Reason, GrokMonitorLine(""))
	after, err = os.ReadFile(wake)
	require.NoError(t, err)
	assert.Equal(t, string(got), string(after))
}

func TestWakeLineIsOneLine(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "nova-friend: hello", WakeLine("hello\n"))
	assert.Equal(t, "nova-friend: PING n1 ⏎ run: pong", WakeLine("PING n1\r\nrun: pong\n\n"))
}

func TestGrokAdapterBacklogDeliveredAsPacedWrites(t *testing.T) {
	t.Parallel()
	home, dir, wake, listing := grokHouse(t)
	const pace = 50 * time.Millisecond

	now := time.Time{}.Add(time.Hour)
	var clockMu sync.Mutex
	clock := func() time.Time {
		clockMu.Lock()
		defer clockMu.Unlock()
		return now
	}
	advance := func(d time.Duration) {
		clockMu.Lock()
		defer clockMu.Unlock()
		now = now.Add(d)
	}

	fe := &fakeExec{out: listing}
	d, err := NewDeliverer("grok", dir, "", fe.run, nil)
	require.NoError(t, err)
	g := d.(*Grok)
	g.Home = home
	g.wakePace = pace
	g.clock = clock

	for i := 0; i < 3; i++ {
		_, err := g.Deliver(context.Background(), fmt.Sprintf("msg%d", i))
		require.NoError(t, err)

		if i == 0 {
			assert.Equal(t, now, g.lastWake, "first write happens immediately")
		} else {
			assert.Equal(t, time.Time{}.Add(time.Hour).Add(time.Duration(i)*pace), g.lastWake,
				"write %d should be paced at %v", i, time.Duration(i)*pace)
		}

		if i < 2 {
			advance(pace)
		}
	}
}
