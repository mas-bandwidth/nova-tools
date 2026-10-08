package friend

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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

// grokPaced is one adapter whose clock and pace wait are the test's.
func grokPaced(t *testing.T, now *time.Time) (Deliverer, string) {
	t.Helper()
	home, dir, wake, listing := grokHouse(t)
	d, err := NewDeliverer("grok", dir, "", (&fakeExec{out: listing}).run, nil)
	require.NoError(t, err)
	g := d.(*Grok)
	g.Home = home
	g.now = func() time.Time { return *now }
	return d, wake
}

func TestGrokPacesTwelveDeliveriesSoABacklogIsNotOneBurst(t *testing.T) {
	t.Parallel()
	now := time.Unix(1_700_000_000, 0).UTC()
	d, wake := grokPaced(t, &now)
	g := d.(*Grok)
	var waits []time.Duration
	g.wait = func(ctx context.Context, gap time.Duration) error {
		got, err := os.ReadFile(wake)
		require.NoError(t, err)
		lines := strings.Split(strings.TrimRight(string(got), "\n"), "\n")
		// the line this wait is holding is not in the file yet: the inbox line, plus one per delivery already written
		assert.Len(t, lines, 2+len(waits))
		assert.Equal(t, "nova-friend: note "+strconv.Itoa(len(waits)), lines[len(lines)-1])
		waits = append(waits, gap)
		now = now.Add(gap)
		return ctx.Err()
	}

	for i := 0; i < 12; i++ {
		exit, err := d.Deliver(context.Background(), "note "+strconv.Itoa(i))
		require.NoError(t, err)
		assert.Equal(t, 0, exit)
	}
	require.Len(t, waits, 11, "the first line is immediate; the other eleven wait")
	for i, gap := range waits {
		assert.Equal(t, wakePace, gap, "wait %d", i)
	}
	got, err := os.ReadFile(wake)
	require.NoError(t, err)
	var want strings.Builder
	want.WriteString("INBOX NOTE id=old\n")
	for i := 0; i < 12; i++ {
		want.WriteString("nova-friend: note " + strconv.Itoa(i) + "\n")
	}
	assert.Equal(t, want.String(), string(got), "twelve deliveries are twelve lines, in order, not one burst")

	// the remainder checks record a gap without the burst's line count
	g.wait = func(ctx context.Context, gap time.Duration) error {
		got, err := os.ReadFile(wake)
		require.NoError(t, err)
		assert.NotContains(t, string(got), "nova-friend: half")
		waits = append(waits, gap)
		now = now.Add(gap)
		return ctx.Err()
	}

	// a full gap later, the next line does not wait
	now = now.Add(wakePace)
	exit, err := d.Deliver(context.Background(), "later")
	require.NoError(t, err)
	assert.Equal(t, 0, exit)
	assert.Len(t, waits, 11)

	// halfway through the gap, the next line waits out the remainder
	now = now.Add(wakePace / 2)
	exit, err = d.Deliver(context.Background(), "half")
	require.NoError(t, err)
	assert.Equal(t, 0, exit)
	require.Len(t, waits, 12)
	assert.Equal(t, wakePace/2, waits[11])
	got, err = os.ReadFile(wake)
	require.NoError(t, err)
	assert.Equal(t, want.String()+"nova-friend: later\nnova-friend: half\n", string(got))
}

func TestGrokDoesNotAppendWhenThePaceWaitIsCancelled(t *testing.T) {
	t.Parallel()
	now := time.Unix(1_700_000_000, 0).UTC()
	d, wake := grokPaced(t, &now)
	g := d.(*Grok)
	g.wait = func(context.Context, time.Duration) error { return context.Canceled }

	exit, err := d.Deliver(context.Background(), "first")
	require.NoError(t, err)
	assert.Equal(t, 0, exit)
	exit, err = d.Deliver(context.Background(), "second")
	assert.Equal(t, 0, exit)
	assert.ErrorIs(t, err, context.Canceled)
	got, err := os.ReadFile(wake)
	require.NoError(t, err)
	assert.Equal(t, "INBOX NOTE id=old\nnova-friend: first\n", string(got))

	// the cancelled attempt gave the booking back, so the next line waits once and lands
	waited := false
	g.wait = func(ctx context.Context, gap time.Duration) error {
		assert.Equal(t, wakePace, gap)
		waited = true
		now = now.Add(gap)
		return ctx.Err()
	}
	exit, err = d.Deliver(context.Background(), "third")
	require.NoError(t, err)
	assert.Equal(t, 0, exit)
	assert.True(t, waited, "the booking was given back, so this line waits one pace")
	got, err = os.ReadFile(wake)
	require.NoError(t, err)
	assert.Equal(t, "INBOX NOTE id=old\nnova-friend: first\nnova-friend: third\n", string(got))
}
