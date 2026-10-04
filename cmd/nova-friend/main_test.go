package main

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/mas-bandwidth/nova-tools/internal/friend"
	"github.com/mas-bandwidth/nova-tools/internal/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var start = time.Date(2026, 10, 4, 3, 0, 0, 0, time.UTC)

// rig is the tool over one fake store with ada and bob known, a fake
// launchctl, a fixed home and clock: no socket, no real time, no launchd.
type rig struct {
	store     *bus.Fake
	env       map[string]string
	launchctl []string
	onPath    map[string]string // what lookPath finds, by name
	now       time.Time
	home      string
}

func newRig(t *testing.T, names ...string) *rig {
	t.Helper()
	return &rig{store: bus.NewFake(start, names...), env: map[string]string{RedisEnv: "store.test:6379", "PATH": "/usr/bin:/bin"}, now: start, home: t.TempDir()}
}

func (r *rig) world() world {
	return world{
		getenv: func(k string) string { return r.env[k] },
		open: func(context.Context, string) (bus.Store, func(), error) {
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
		lookPath: func(name string) (string, error) {
			if p, ok := r.onPath[name]; ok {
				return p, nil
			}
			return "", errors.New("executable file not found in ")
		},
		random: func() string { return "r4nd0m" },
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
	entries, err := r.store.Range(context.Background(), bus.StreamOf("bob"), "-", "+", 10)
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
	_, err = (&bus.Bus{Store: r.store}).Send(context.Background(), bus.Message{From: "ada", To: []string{"ada"}, Subject: "pong", Body: friend.PongLine("zzz999", 0, 0, 0)})
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
	cli.Do(t, "status", "--as", "bob", "--dir", dir).Exit(1).Err("STATUS NONE: no daemon has run as bob (no status file in "+state+")", "nova-friend install --as bob --harness <h> --dir "+dir)
	require.NoError(t, friend.WriteStatus(state, friend.Status{Friend: "bob", Harness: "opencode", At: start, Seat: "ada", LastPing: start.Add(-time.Minute), Connection: friend.Connected, Challenge: friend.Challenged, Nonce: "n1", Beats: 7, Width: 4, Delivered: 2, BeatError: "the sprint server at 127.0.0.1:6390 did not answer"}))
	require.NoError(t, friend.WritePong(state, friend.Pong{Nonce: "n0", At: start.Add(-2 * time.Minute), Queue: 3, Working: 1, Width: 8}))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "inbox"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "inbox", "QUEUE.json"), []byte(`{"tasks":[{"id":"a","state":"queued"},{"id":"b","state":"working"}]}`), 0o644))
	cli.Do(t, "status", "--as", "bob", "--dir", dir).Exit(0).
		Out("STATUS OK daemon=up harness=opencode status_age=1s connection=connected seat=ada last_ping=2026-10-04T02:59:00Z ping_age=1m1s challenge=challenged nonce=n1 last_pong=2026-10-04T02:58:00Z pong_age=2m1s pongs=0 queue=1 working=1 width=8 beats=7 last_beat=- delivered=2 session=-",
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
			"NOTE the agent runs: nova-friend run --as bob --harness opencode --dir /w/bob --width 4, with --redis and --server as given here")
	assert.NoFileExists(t, plist)
	assert.Empty(t, r.launchctl)

	cli.Do(t, "install", "--as", "bob", "--harness", "opencode", "--dir", "/w/bob", "--session", "ses_1").Exit(0).
		Out("INSTALL OK label=com.nova.friend-bob plist="+plist, `INSTALL RAN command="launchctl bootout gui/501/com.nova.friend-bob"`, `INSTALL RAN command="launchctl bootstrap gui/501 `+plist+`"`, "NOTE check it: nova-friend status --as bob --dir /w/bob")
	assert.Equal(t, []string{"bootout gui/501/com.nova.friend-bob", "bootstrap gui/501 " + plist}, r.launchctl)
	raw, err := os.ReadFile(plist)
	require.NoError(t, err)
	assert.Contains(t, string(raw), "<string>--session</string>\n    <string>ses_1</string>")
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
	w.beat = func(context.Context, string, string) error {
		beats++
		if beats == 3 {
			cancel()
		}
		return nil
	}
	dir := t.TempDir()
	var out, errb strings.Builder
	code := run([]string{"run", "--as", "bob", "--harness", "claude", "--dir", dir, "--width", "4"}, strings.NewReader(""), &out, &errb, w)
	assert.Equal(t, 0, code, errb.String())
	assert.NoDirExists(t, filepath.Join(dir, ".nova-friend"), "nothing of the daemon's on the friend's volume")
	s, found, err := friend.ReadStatus(friend.DefaultStateDir(r.home, "bob"))
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, "bob", s.Friend)
	assert.Equal(t, 3, beats)
	assert.GreaterOrEqual(t, s.Beats, 1, "the count in the file lags up to StatusEvery")

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
	w.open = func(context.Context, string) (bus.Store, func(), error) {
		opens++
		if opens < 4 {
			return nil, nil, io.ErrUnexpectedEOF
		}
		return r.store, func() {}, nil
	}
	var slept []time.Duration
	w.sleep = func(_ context.Context, d time.Duration) { slept = append(slept, d) }
	w.beat = func(context.Context, string, string) error {
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
	w.open = func(context.Context, string) (bus.Store, func(), error) { return nil, nil, io.ErrUnexpectedEOF }
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
	b := &bus.Bus{Store: r.store}
	for i := 0; i < 10000; i++ { // one second of the store's clock each
		_, err := b.Send(context.Background(), bus.Message{From: "ada", To: []string{"bob"}, Subject: "old", Body: "x"})
		require.NoError(t, err)
	}
	_, err := b.Send(context.Background(), bus.Message{From: "bob", To: []string{"ada"}, Subject: "pong", Body: friend.PongLine("abc123", 1, 0, 4)})
	require.NoError(t, err)
	cli := r.cli()
	cli.Do(t, "wait-pong", "--from", "bob", "--nonce", "abc123", "--timeout", "3s").Exit(0).Out("WAIT-PONG OK nonce=abc123 from=bob", "queue=1 working=0 width=4")
	r.store.Advance(time.Minute)
	cli.Do(t, "wait-pong", "--from", "bob", "--nonce", "abc123", "--timeout", "3s").Exit(1).Err("WAIT-PONG NONE")
}

// install --secrets NAME[,NAME] --seat <seat> writes the agent with the
// daemon wrapped in nova-secrets exec: the programs by absolute path from
// PATH at install, the names opened and required, the daemon after the --.
func TestInstallSecretsWrapsTheDaemonInNovaSecretsExec(t *testing.T) {
	t.Parallel()
	r := newRig(t, "ada", "bob")
	cli := r.cli()
	cli.Do(t, "install", "--as", "bob", "--harness", "opencode", "--dir", "/w/bob", "--secrets", "DEEPSEEK_API_KEY", "--dry-run").Exit(2).Err("--secrets wants --seat")
	cli.Do(t, "install", "--as", "bob", "--harness", "opencode", "--dir", "/w/bob", "--secrets", "DEEPSEEK_API_KEY,no-such", "--seat", "studio", "--dry-run").Exit(2).Err(`"no-such" is none`)
	cli.Do(t, "install", "--as", "bob", "--harness", "opencode", "--dir", "/w/bob", "--secrets", "DEEPSEEK_API_KEY", "--seat", "studio", "--dry-run").Exit(2).Err("nova-secrets is not on PATH")
	r.onPath = map[string]string{"nova-secrets": "/opt/nova/bin/nova-secrets", "sops": "/opt/homebrew/bin/sops"}
	wrap := "/opt/nova/bin/nova-secrets exec --store " + filepath.Join(r.home, "nova-bench", "secrets") + " --as studio --key " + filepath.Join(r.home, ".config", "nova-secrets", "studio.key") +
		" --sops /opt/homebrew/bin/sops --only DEEPSEEK_API_KEY,GH_TOKEN --require DEEPSEEK_API_KEY --require GH_TOKEN -- /opt/nova/bin/nova-friend run --as bob --harness opencode --dir /w/bob --redis store.test:6379 --server 127.0.0.1:6390 --width 0"
	cli.Do(t, "install", "--as", "bob", "--harness", "opencode", "--dir", "/w/bob", "--secrets", "DEEPSEEK_API_KEY,GH_TOKEN", "--seat", "studio", "--dry-run").Exit(0).
		Out("NOTE the agent runs: nova-secrets exec --as studio --only DEEPSEEK_API_KEY,GH_TOKEN --require DEEPSEEK_API_KEY --require GH_TOKEN -- nova-friend run --as bob --harness opencode --dir /w/bob --width 0, with --redis and --server as given here")
	cli.Do(t, "install", "--as", "bob", "--harness", "opencode", "--dir", "/w/bob", "--secrets", "DEEPSEEK_API_KEY,GH_TOKEN", "--seat", "studio").Exit(0).Out("INSTALL OK label=com.nova.friend-bob")
	raw, err := os.ReadFile(filepath.Join(r.home, "Library", "LaunchAgents", "com.nova.friend-bob.plist"))
	require.NoError(t, err)
	var args []string
	for _, line := range strings.Split(string(raw), "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "<string>"); ok && strings.HasSuffix(v, "</string>") && !strings.Contains(line, "<key>") {
			args = append(args, strings.TrimSuffix(v, "</string>"))
		}
	}
	assert.Equal(t, strings.Fields(wrap), args[:len(strings.Fields(wrap))], "the plist's ProgramArguments are the wrap, then the daemon")
}

// A broken session is in the status line, with its id and the provider's
// reason, and a note on what to do (the finding of 2026-10-04: Freddy's
// session refused every turn for two hours and status said nothing).
func TestStatusSaysABrokenSessionAndWhy(t *testing.T) {
	t.Parallel()
	r := newRig(t, "ada", "bob")
	cli := r.cli()
	dir := t.TempDir()
	state := friend.DefaultStateDir(r.home, "bob")
	require.NoError(t, friend.WriteStatus(state, friend.Status{Friend: "bob", Harness: "opencode", At: start, Connection: friend.Connected, Challenge: friend.Quiet,
		Session: friend.SessionBroken, SessionID: "ses_x", SessionReason: "invalid_request_error: bad input", BrokenAt: start.Add(-time.Minute)}))
	cli.Do(t, "status", "--as", "bob", "--dir", dir).Exit(0).
		Out(`delivered=0 session=broken session_id=ses_x broken_at=2026-10-04T02:59:00Z reason="invalid_request_error: bad input"`,
			"NOTE the session is broken: the provider refused the same way turn after turn")
}

// The daemon's new flags reach the agent's command line when they are set
// and not the default, so a reinstall with the same flags writes the same
// plist.
func TestInstallCarriesTheCoordinatorAndANonDefaultSilentStop(t *testing.T) {
	t.Parallel()
	r := newRig(t, "ada", "bob")
	cli := r.cli()
	plist := filepath.Join(r.home, "Library", "LaunchAgents", "com.nova.friend-bob.plist")
	cli.Do(t, "install", "--as", "bob", "--harness", "opencode", "--dir", "/w/bob", "--coordinator", "ada", "--silent-stop", "30m").Exit(0)
	raw, err := os.ReadFile(plist)
	require.NoError(t, err)
	assert.Contains(t, string(raw), "<string>--coordinator</string>\n    <string>ada</string>\n    <string>--silent-stop</string>\n    <string>30m0s</string>")
	cli.Do(t, "install", "--as", "bob", "--harness", "opencode", "--dir", "/w/bob").Exit(0)
	raw, err = os.ReadFile(plist)
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "--silent-stop", "the defaults are not written")
	assert.NotContains(t, string(raw), "--broken-after")
	assert.NotContains(t, string(raw), "--coordinator")
}
