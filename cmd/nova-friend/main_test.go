package main

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bus2"
	"github.com/mas-bandwidth/nova-tools/internal/friend"
	"github.com/mas-bandwidth/nova-tools/internal/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var start = time.Date(2026, 10, 4, 3, 0, 0, 0, time.UTC)

// rig is the tool over one fake store with ada and bob known, a fake
// launchctl, a fixed home and clock: no socket, no real time, no launchd.
type rig struct {
	store     *bus2.Fake
	env       map[string]string
	launchctl []string
	now       time.Time
	home      string
}

func newRig(t *testing.T, names ...string) *rig {
	t.Helper()
	return &rig{store: bus2.NewFake(start, names...), env: map[string]string{RedisEnv: "store.test:6379", "PATH": "/usr/bin:/bin"}, now: start, home: t.TempDir()}
}

func (r *rig) world() world {
	return world{
		getenv: func(k string) string { return r.env[k] },
		open: func(context.Context, string) (bus2.Store, func(), error) {
			if r.store.Fail != nil {
				return nil, nil, r.store.Fail
			}
			return r.store, func() {}, nil
		},
		launchctl: func(_ context.Context, args ...string) (string, error) {
			r.launchctl = append(r.launchctl, strings.Join(args, " "))
			return "", nil
		},
		now:     func() time.Time { r.now = r.now.Add(time.Second); return r.now },
		sleep:   func(context.Context, time.Duration) { r.now = r.now.Add(time.Second) },
		signals: func(ctx context.Context) (context.Context, context.CancelFunc) { return context.WithCancel(ctx) },
		uid:     501,
		home:    r.home,
		binary:  func() (string, error) { return "/opt/nova/bin/nova-friend", nil },
		random:  func() string { return "r4nd0m" },
	}
}

func (r *rig) cli() testkit.Main {
	w := r.world()
	return testkit.Main(func(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
		return run(args, stdin, stdout, stderr, w)
	})
}

func TestTheToolMeetsTheSkeletonStandard(t *testing.T) {
	t.Parallel()
	assert.Empty(t, friendTool(newRig(t).world()).Problems())
}

func TestBareCommandNamesTheDoor(t *testing.T) {
	t.Parallel()
	newRig(t).cli().Do(t).Exit(2).Err("FRIEND REFUSED", "nova-friend help")
}

func TestSleepNeedsCoordinatorAndWakeCanRecoverIt(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	cli := r.cli()
	stateDir := friend.DefaultStateDir(r.home, "bob")
	cli.Do(t, "sleep", "--as", "bob", "--state-dir", stateDir).Exit(2).Err("sleep needs a coordinator")
	state, err := friend.ReadSessionState(stateDir)
	require.NoError(t, err)
	assert.False(t, state.Asleep, "a refused sleep changes nothing")
	cli.Do(t, "sleep", "--as", "bob", "--coordinator", "ada", "--state-dir", stateDir, "--dry-run").Exit(0).Out("SLEEP OK asleep=true coordinator=ada dry_run=true", "plan only; the session state was not changed")
	state, err = friend.ReadSessionState(stateDir)
	require.NoError(t, err)
	assert.Equal(t, friend.SessionState{}, state, "dry-run does not persist the coordinator or asleep marker")
	assert.NoDirExists(t, stateDir, "dry-run does not create the state directory or lock")

	cli.Do(t, "sleep", "--as", "bob", "--coordinator", "ada", "--state-dir", stateDir).Exit(0).Out("SLEEP OK asleep=true coordinator=ada")
	state, err = friend.ReadSessionState(stateDir)
	require.NoError(t, err)
	assert.Equal(t, friend.SessionState{Coordinator: "ada", Asleep: true}, state)
	cli.Do(t, "wake", "--as", "bob", "--state-dir", stateDir, "--dry-run").Exit(0).Out("WAKE OK asleep=false was_asleep=true dry_run=true", "plan only; the session state was not changed")
	state, err = friend.ReadSessionState(stateDir)
	require.NoError(t, err)
	assert.True(t, state.Asleep, "dry-run does not clear asleep")
	cli.Do(t, "wake", "--as", "bob", "--state-dir", stateDir).Exit(0).Out("WAKE OK asleep=false")
	state, err = friend.ReadSessionState(stateDir)
	require.NoError(t, err)
	assert.Equal(t, friend.SessionState{Coordinator: "ada", Asleep: false}, state)
}

func TestInvalidStateOwnerNameIsRefusedBeforeCreatingItsDefaultPath(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.cli().Do(t, "sleep", "--as", "../bob").Exit(2).Err("--as", "not lowercase")
	assert.NoDirExists(t, filepath.Join(r.home, ".nova-friend"), "validate before deriving or creating a state path")
}

func TestRunRefusesAStateDirectoryOwnedByAnotherFriendBeforeOpeningTheBus(t *testing.T) {
	t.Parallel()
	r := newRig(t, "ada", "bob")
	stateDir := friend.DefaultStateDir(r.home, "bob")
	require.NoError(t, friend.WriteStatus(stateDir, friend.Status{Friend: "alice", At: start}))
	_, err := friend.UpdateSessionState(stateDir, func(s *friend.SessionState) error {
		s.Coordinator = "ada"
		return nil
	})
	require.NoError(t, err)
	w := r.world()
	opens := 0
	w.open = func(context.Context, string) (bus2.Store, func(), error) {
		opens++
		return r.store, func() {}, nil
	}
	code := run([]string{"run", "--as", "bob", "--harness", "claude", "--dir", t.TempDir(), "--state-dir", stateDir, "--coordinator", "other"}, strings.NewReader(""), io.Discard, io.Discard, w)
	assert.Equal(t, 2, code)
	assert.Zero(t, opens)
	state, err := friend.ReadSessionState(stateDir)
	require.NoError(t, err)
	assert.Equal(t, "ada", state.Coordinator, "refusal did not overwrite another owner's state")
}

func TestSleepAndWakeRefuseAStateDirectoryOwnedByAnotherFriend(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	stateDir := friend.DefaultStateDir(r.home, "alice")
	require.NoError(t, friend.WriteStatus(stateDir, friend.Status{Friend: "alice", At: start}))
	cli := r.cli()
	cli.Do(t, "sleep", "--as", "bob", "--coordinator", "ada", "--state-dir", stateDir).Exit(2).Err("runs as alice, not bob")
	cli.Do(t, "wake", "--as", "bob", "--state-dir", stateDir).Exit(2).Err("runs as alice, not bob")
	state, err := friend.ReadSessionState(stateDir)
	require.NoError(t, err)
	assert.Equal(t, friend.SessionState{}, state)
}

func TestRunRefusesAnAsleepSessionWithoutCoordinatorWithoutWakingIt(t *testing.T) {
	t.Parallel()
	r := newRig(t, "ada", "bob")
	stateDir := friend.DefaultStateDir(r.home, "bob")
	_, err := friend.UpdateSessionState(stateDir, func(s *friend.SessionState) error {
		s.Asleep = true
		return nil
	})
	require.NoError(t, err)
	w := r.world()
	opens := 0
	w.open = func(context.Context, string) (bus2.Store, func(), error) {
		opens++
		return r.store, func() {}, nil
	}
	code := run([]string{"run", "--as", "bob", "--harness", "claude", "--dir", t.TempDir(), "--state-dir", stateDir}, strings.NewReader(""), io.Discard, io.Discard, w)
	assert.Equal(t, 2, code)
	assert.Zero(t, opens, "refuse before opening the bus")
	state, err := friend.ReadSessionState(stateDir)
	require.NoError(t, err)
	assert.True(t, state.Asleep, "startup does not silently wake the saved session")
	assert.Empty(t, state.Coordinator)
}

func TestRunReportsPersistedSleepOnItsBeat(t *testing.T) {
	t.Parallel()
	r := newRig(t, "ada", "bob")
	stateDir := friend.DefaultStateDir(r.home, "bob")
	_, err := friend.UpdateSessionState(stateDir, func(s *friend.SessionState) error {
		s.Coordinator, s.Asleep = "ada", true
		return nil
	})
	require.NoError(t, err)
	w := r.world()
	var cancel context.CancelFunc
	w.signals = func(ctx context.Context) (context.Context, context.CancelFunc) {
		ctx, cancel = context.WithCancel(ctx)
		return ctx, cancel
	}
	var beatAsleep []bool
	w.beat = func(_ context.Context, _, _ string, asleep bool) error {
		beatAsleep = append(beatAsleep, asleep)
		cancel()
		return nil
	}
	code := run([]string{"run", "--as", "bob", "--harness", "claude", "--dir", t.TempDir(), "--state-dir", stateDir}, strings.NewReader(""), io.Discard, io.Discard, w)
	assert.Equal(t, 0, code)
	assert.Equal(t, []bool{true}, beatAsleep)
}

func TestRefusalsNameEveryProblemAndWhatEachWants(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		args []string
		says []string
	}{
		{"run nothing given", []string{"run"}, []string{"--as is required", "--harness is required", "--dir is required"}},
		{"run bad harness", []string{"run", "--as", "bob", "--harness", "vim", "--dir", "d"}, []string{`--harness "vim" is no harness`, "opencode, codex, claude, antigravity, dsh"}},
		{"install nothing given", []string{"install"}, []string{"--as is required", "--harness is required", "--dir is required"}},
		{"ping nothing given", []string{"ping"}, []string{"--as is required", "--to is required"}},
		{"ping bad since", []string{"ping", "--as", "ada", "--to", "bob", "--since", "yesterday"}, []string{"--since wants an RFC3339 instant"}},
		{"ping unknown friend", []string{"ping", "--as", "ada", "--to", "zed"}, []string{"zed is no known name", "nova-config friend add zed"}},
		{"pong nothing given", []string{"pong"}, []string{"--as is required", "--nonce is required"}},
		{"pong no seat yet", []string{"pong", "--as", "bob", "--nonce", "n1", "--state-dir", t.TempDir()}, []string{"--to is required", "no ping has named a seat yet"}},
		{"wait-pong nothing given", []string{"wait-pong"}, []string{"--from is required", "--nonce is required"}},
		{"status nothing given", []string{"status"}, []string{"--as is required", "--dir is required"}},
		{"sleep nothing given", []string{"sleep"}, []string{"--as is required"}},
		{"wake nothing given", []string{"wake"}, []string{"--as is required"}},
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
	r := newRig(t, "ada", "bob")
	r.env = map[string]string{}
	r.cli().Do(t, "ping", "--as", "ada", "--to", "bob").Exit(2).Err("--redis is required", RedisEnv)
	r.cli().Do(t, "install", "--as", "bob", "--harness", "opencode", "--dir", "d", "--dry-run").Exit(2).Err("--redis is required", "written into the agent")
}

func TestPingPongAndWaitPongAreTheCanary(t *testing.T) {
	t.Parallel()
	r := newRig(t, "ada", "bob")
	cli := r.cli()
	state := friend.DefaultStateDir(r.home, "bob")
	cli.Do(t, "wait-pong", "--from", "bob", "--nonce", "abc123", "--timeout", "3s").Exit(1).Err("WAIT-PONG NONE daemon=false: no pong abc123 from bob within 3s")

	sent := cli.Do(t, "ping", "--as", "ada", "--to", "bob", "--nonce", "abc123").Exit(0).Out("PING OK nonce=abc123 id=", " to=bob at=2026-10-04T03:00:", "NOTE wait for it: nova-friend wait-pong --from bob --nonce abc123")
	_ = sent
	entries, err := r.store.Range(context.Background(), bus2.StreamOf("bob"), "-", "+", 10)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	m := entries[0].Message()
	assert.Equal(t, "PING abc123", m.Subject)
	assert.Contains(t, m.Body, "seat=ada since=2026-10-04T03:00:")
	assert.Contains(t, m.Body, "nova-friend pong --as <you> --nonce abc123")
	cli.Do(t, "ping", "--as", "ada", "--to", "bob").Exit(0).Out("PING OK nonce=r4nd0m")

	// the session answers, naming the coordinator since no daemon has recorded a seat; the pong file goes under the home directory
	cli.Do(t, "pong", "--as", "bob", "--nonce", "abc123", "--to", "ada", "--queue", "2", "--working", "1", "--width", "4").Exit(0).Out("PONG OK nonce=abc123 to=ada id=")
	p, found, err := friend.ReadPong(state)
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, "abc123", p.Nonce)
	assert.Equal(t, [3]int{2, 1, 4}, [3]int{p.Queue, p.Working, p.Width})
	cli.Do(t, "wait-pong", "--from", "bob", "--nonce", "abc123", "--timeout", "3s").Exit(0).Out("WAIT-PONG OK nonce=abc123 from=bob at=", "queue=2 working=1 width=4 daemon=false")

	// a pong in the body from another name never counts: the from is the proof
	_, err = (&bus2.Bus{Store: r.store}).Send(context.Background(), bus2.Message{From: "ada", To: []string{"ada"}, Subject: "pong", Body: friend.PongLine("zzz999", 0, 0, 0)})
	require.NoError(t, err)
	cli.Do(t, "wait-pong", "--from", "bob", "--nonce", "zzz999", "--timeout", "2s").Exit(1).Err("WAIT-PONG NONE")
	cli.Do(t, "wait-pong", "--from", "bob", "--nonce", "abc123", "--json").Exit(0).Out(`"status":"ok"`, `"nonce":"abc123"`)
}

func TestPongCarriesTheDaemonsNameAndTheSeatItRecorded(t *testing.T) {
	t.Parallel()
	r := newRig(t, "ada", "bob")
	cli := r.cli()
	state := t.TempDir()
	require.NoError(t, friend.WriteStatus(state, friend.Status{Friend: "bob", Harness: "opencode", At: start, Seat: "ada", Connection: friend.Connected, Challenge: friend.Challenged, Nonce: "n1"}))
	cli.Do(t, "pong", "--as", "ada", "--nonce", "n1", "--state-dir", state).Exit(2).Err("PONG REFUSED: the daemon whose state is in " + state + " runs as bob, not ada")
	cli.Do(t, "pong", "--as", "bob", "--nonce", "n1", "--state-dir", state).Exit(0).Out("PONG OK nonce=n1 to=ada")
	cli.Do(t, "status", "--as", "ada", "--dir", "/w/ada", "--state-dir", state).Exit(2).Err("runs as bob, not ada")
	// the default state directory is under the home directory, by name
	require.NoError(t, friend.WriteStatus(friend.DefaultStateDir(r.home, "bob"), friend.Status{Friend: "bob", Harness: "opencode", At: start, Seat: "ada"}))
	cli.Do(t, "pong", "--as", "bob", "--nonce", "n2").Exit(0).Out("PONG OK nonce=n2 to=ada")
}

func TestStatusReadsTheThreeFiles(t *testing.T) {
	t.Parallel()
	r := newRig(t, "ada", "bob")
	cli := r.cli()
	dir := t.TempDir()
	state := friend.DefaultStateDir(r.home, "bob")
	_, err := friend.UpdateSessionState(state, func(s *friend.SessionState) error {
		s.Coordinator, s.Asleep = "ada", true
		return nil
	})
	require.NoError(t, err)
	cli.Do(t, "status", "--as", "bob", "--dir", dir).Exit(1).
		Out("STATUS NONE: no daemon has run as bob (no status file in " + state + "); run: nova-friend install --as bob --harness <h> --dir " + dir + " reported_asleep=unknown sleep_requested=true coordinator=ada")
	require.NoError(t, friend.WriteStatus(state, friend.Status{Friend: "bob", Harness: "opencode", At: start, Seat: "ada", LastPing: start.Add(-time.Minute), Connection: friend.Connected, Challenge: friend.Challenged, Nonce: "n1", Beats: 7, Width: 4, Delivered: 2, BeatError: "the sprint server at 127.0.0.1:6390 did not answer"}))
	require.NoError(t, friend.WritePong(state, friend.Pong{Nonce: "n0", At: start.Add(-2 * time.Minute), Queue: 3, Working: 1, Width: 8}))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "inbox"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "inbox", "QUEUE.json"), []byte(`{"tasks":[{"id":"a","state":"queued"},{"id":"b","state":"working"}]}`), 0o644))
	cli.Do(t, "status", "--as", "bob", "--dir", dir).Exit(0).
		Out("STATUS OK daemon=up harness=opencode status_age=1s connection=connected seat=ada last_ping=2026-10-04T02:59:00Z ping_age=1m1s challenge=challenged nonce=n1 last_pong=2026-10-04T02:58:00Z pong_age=2m1s pongs=0 queue=1 working=1 width=8 beats=7 last_beat=- delivered=2 reported_asleep=false sleep_requested=true coordinator=ada",
			"NOTE the last beat failed: the sprint server at 127.0.0.1:6390 did not answer")
	r.now = start.Add(friend.DaemonStale)
	cli.Do(t, "status", "--as", "bob", "--dir", dir).Exit(0).Out("STATUS OK daemon=down")
}

func TestInstallWritesThePlistBootsOutAndBootstrapsAndUninstallUndoesIt(t *testing.T) {
	t.Parallel()
	r := newRig(t, "ada", "bob")
	cli := r.cli()
	plist := filepath.Join(r.home, "Library", "LaunchAgents", "com.nova.friend-bob.plist")
	cli.Do(t, "install", "--as", "bob", "--harness", "opencode", "--dir", "/w/bob", "--width", "4", "--dry-run").Exit(0).
		Out("INSTALL OK label=com.nova.friend-bob plist="+plist+" launchd_log="+filepath.Join(r.home, "Library", "Logs", "nova-friend-bob.log")+" dry_run=true",
			`INSTALL PLAN command="launchctl bootout gui/501/com.nova.friend-bob"`, `INSTALL PLAN command="launchctl bootstrap gui/501 `+plist+`"`,
			"NOTE the agent runs: nova-friend run --as bob --harness opencode --dir /w/bob --width 4, with --redis, --server and --coordinator as given here")
	assert.NoFileExists(t, plist)
	assert.Empty(t, r.launchctl)

	cli.Do(t, "install", "--as", "bob", "--harness", "opencode", "--dir", "/w/bob", "--session", "ses_1", "--coordinator", "ada").Exit(0).
		Out("INSTALL OK label=com.nova.friend-bob plist="+plist, `INSTALL RAN command="launchctl bootout gui/501/com.nova.friend-bob"`, `INSTALL RAN command="launchctl bootstrap gui/501 `+plist+`"`, "NOTE check it: nova-friend status --as bob --dir /w/bob")
	assert.Equal(t, []string{"bootout gui/501/com.nova.friend-bob", "bootstrap gui/501 " + plist}, r.launchctl)
	raw, err := os.ReadFile(plist)
	require.NoError(t, err)
	assert.Contains(t, string(raw), "<string>--session</string>\n    <string>ses_1</string>")
	assert.Contains(t, string(raw), "<string>--coordinator</string>\n    <string>ada</string>")
	assert.Contains(t, string(raw), "<key>PATH</key><string>/usr/bin:/bin</string>")

	cli.Do(t, "uninstall", "--as", "bob", "--dry-run").Exit(0).Out("UNINSTALL OK label=com.nova.friend-bob", `UNINSTALL PLAN command="launchctl bootout gui/501/com.nova.friend-bob"`, `UNINSTALL PLAN command="rm `+plist+`"`)
	assert.FileExists(t, plist)
	cli.Do(t, "uninstall", "--as", "bob").Exit(0).Out("UNINSTALL OK label=com.nova.friend-bob", `UNINSTALL RAN command="launchctl bootout gui/501/com.nova.friend-bob"`)
	assert.NoFileExists(t, plist)
	cli.Do(t, "uninstall", "--as", "bob").Exit(0).Out("UNINSTALL OK")
}

// run over the fake store, a stub harness and a cancelled context: the
// daemon's own tests are internal/friend's; here, that the verb wires it.
func TestRunStopsOnASignalAndRefusesAStoreThatDoesNotAnswer(t *testing.T) {
	t.Parallel()
	r := newRig(t, "ada", "bob")
	w := r.world()
	var cancel context.CancelFunc
	w.signals = func(ctx context.Context) (context.Context, context.CancelFunc) {
		ctx, cancel = context.WithCancel(ctx)
		return ctx, cancel
	}
	beats := 0
	w.beat = func(context.Context, string, string, bool) error {
		beats++
		if beats == 3 {
			cancel()
		}
		return nil
	}
	dir := t.TempDir()
	var out, errb strings.Builder
	code := run([]string{"run", "--as", "bob", "--harness", "claude", "--dir", dir, "--width", "4", "--coordinator", "ada"}, strings.NewReader(""), &out, &errb, w)
	assert.Equal(t, 0, code, errb.String())
	assert.NoDirExists(t, filepath.Join(dir, ".nova-friend"), "nothing of the daemon's on the friend's volume")
	s, found, err := friend.ReadStatus(friend.DefaultStateDir(r.home, "bob"))
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, "bob", s.Friend)
	assert.Equal(t, 3, beats)
	assert.GreaterOrEqual(t, s.Beats, 1, "the count in the file lags up to StatusEvery")
	session, err := friend.ReadSessionState(friend.DefaultStateDir(r.home, "bob"))
	require.NoError(t, err)
	assert.Equal(t, "ada", session.Coordinator, "the run's explicit coordinator is saved for sleep/wake")

}

// A store that is down when the daemon starts is no reason to exit: under launchd's
// KeepAlive an exit 2 was a crash loop every five seconds (the finding of
// 2026-10-04). The store is opened until it answers, waiting longer each time up
// to OpenRetryMax, each wait said on stdout (launchd's log); a signal while it
// is down ends the daemon cleanly.
func TestRunWaitsForAStoreThatIsDownAtTheStart(t *testing.T) {
	t.Parallel()
	r := newRig(t, "ada", "bob")
	w := r.world()
	var cancel context.CancelFunc
	w.signals = func(ctx context.Context) (context.Context, context.CancelFunc) {
		ctx, cancel = context.WithCancel(ctx)
		return ctx, cancel
	}
	opens, beats := 0, 0
	w.open = func(context.Context, string) (bus2.Store, func(), error) {
		opens++
		if opens < 4 {
			return nil, nil, io.ErrUnexpectedEOF
		}
		return r.store, func() {}, nil
	}
	var slept []time.Duration
	w.sleep = func(_ context.Context, d time.Duration) { slept = append(slept, d) }
	w.beat = func(context.Context, string, string, bool) error {
		beats++
		if beats == 2 {
			cancel()
		}
		return nil
	}
	var out, errb strings.Builder
	code := run([]string{"run", "--as", "bob", "--harness", "claude", "--dir", t.TempDir()}, strings.NewReader(""), &out, &errb, w)
	assert.Equal(t, 0, code, errb.String())
	require.GreaterOrEqual(t, len(slept), 3)
	assert.Equal(t, []time.Duration{time.Second, 2 * time.Second, 4 * time.Second}, slept[:3], "longer each time; the rest are the loop's own pauses")
	assert.Equal(t, 4, opens)
	assert.Equal(t, 2, beats, "the loop ran once the store answered")
	assert.Contains(t, out.String(), "RUN 2026-10-04T03:00:01Z store: unexpected EOF; opening again in 1s")
	assert.Contains(t, out.String(), "opening again in 4s")

	// down for good: the signal ends it, exit 0, no crash loop
	w.open = func(context.Context, string) (bus2.Store, func(), error) { return nil, nil, io.ErrUnexpectedEOF }
	w.sleep = func(_ context.Context, d time.Duration) {
		slept = append(slept, d)
		if d == OpenRetryMax {
			cancel()
		}
	}
	out.Reset()
	code = run([]string{"run", "--as", "bob", "--harness", "claude", "--dir", t.TempDir()}, strings.NewReader(""), &out, &errb, w)
	assert.Equal(t, 0, code)
	assert.Equal(t, OpenRetryMax, slept[len(slept)-1], "the wait is capped")
	assert.Contains(t, out.String(), "opening again in "+OpenRetryMax.String())
}

// wait-pong reads the log from --timeout before the wait began, never from
// its start: a log longer than one read's limit still answers (the finding
// of 2026-10-04: the whole log from "-" with a 10,000 cap), and a pong older
// than the wait is not looked for.
func TestWaitPongReadsTheLogFromTheWaitsOwnWindow(t *testing.T) {
	t.Parallel()
	r := newRig(t, "ada", "bob")
	b := &bus2.Bus{Store: r.store}
	for i := 0; i < 10000; i++ { // one second of the store's clock each
		_, err := b.Send(context.Background(), bus2.Message{From: "ada", To: []string{"bob"}, Subject: "old", Body: "x"})
		require.NoError(t, err)
	}
	_, err := b.Send(context.Background(), bus2.Message{From: "bob", To: []string{"ada"}, Subject: "pong", Body: friend.PongLine("abc123", 1, 0, 4)})
	require.NoError(t, err)
	cli := r.cli()
	cli.Do(t, "wait-pong", "--from", "bob", "--nonce", "abc123", "--timeout", "3s").Exit(0).Out("WAIT-PONG OK nonce=abc123 from=bob", "queue=1 working=0 width=4")
	r.store.Advance(time.Minute)
	cli.Do(t, "wait-pong", "--from", "bob", "--nonce", "abc123", "--timeout", "3s").Exit(1).Err("WAIT-PONG NONE")
}
