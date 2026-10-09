package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/mas-bandwidth/nova-tools/internal/bus/bustest"
	"github.com/mas-bandwidth/nova-tools/internal/friend"
	"github.com/mas-bandwidth/nova-tools/internal/friend/friendtest"
	"github.com/mas-bandwidth/nova-tools/internal/onboarding"
	"github.com/mas-bandwidth/nova-tools/internal/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var start = time.Date(2026, 10, 4, 3, 0, 0, 0, time.UTC)

// TestMain starts the runtime's signal-mask goroutine outside any synctest
// bubble. Every run goes through signal.NotifyContext (internal/tool's
// RunContext); the first such call in the process makes that goroutine and its
// channels, and made inside a bubble they belong to it, so another bubble's
// Notify blocks durably on them and the bubble panics as deadlocked.
func TestMain(m *testing.M) {
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)
	signal.Stop(c)
	os.Exit(m.Run())
}

// rig is the tool over one fake store with ada and bob known, a fake
// launchctl, a fixed home and clock: no socket, no real time, no launchd.
type rig struct {
	store        *bustest.Fake
	env          map[string]string
	launchctl    []string
	launchctlOut string
	beats        []string // "server friend" for each beat the fake answered, in order
	beatErr      error    // what the fake beat answers instead, when set
	copy         friend.CopyFile
	onPath       map[string]string // what lookPath finds, by name
	now          time.Time
	home         string
	answered     map[string]bool // the session checks bob's fake session has answered
	alive        friend.Aliver
	deaf         bool              // bob's opencode session takes a turn and never runs the pong line
	fs           *friendtest.MemFS // the harness settings' filesystem: bob's directory /w/bob
}

type fakeAlive struct {
	running bool
	why     string
}

func (f fakeAlive) Alive(context.Context) friend.Liveness {
	if f.running {
		return friend.Liveness{Known: true, Running: true, Why: f.why}
	}
	return friend.Liveness{Known: true, Running: false, Why: f.why}
}

func newRig(t *testing.T, names ...string) *rig {
	t.Helper()
	fs := friendtest.NewMemFS()
	require.NoError(t, fs.MkdirAll("/w/bob", 0o755))
	return &rig{store: bustest.NewFake(start, names...), env: map[string]string{RedisEnv: "store.test:6379", "PATH": "/usr/bin:/bin"}, now: start, home: t.TempDir(), alive: fakeAlive{running: true, why: "the fake harness runs"}, fs: fs}
}

func (r *rig) world() world {
	return world{
		stepBeat: true,
		checkGo:  func(f func()) { f() }, // fake-clock checks complete before the clock advances again
		getenv:   func(k string) string { return r.env[k] },
		open: func(context.Context, string) (bus.Store, func(), error) {
			if r.store.Fail != nil {
				return nil, nil, r.store.Fail
			}
			return r.store, func() {}, nil
		},
		launchctl: func(_ context.Context, args ...string) (string, error) {
			r.launchctl = append(r.launchctl, strings.Join(args, " "))
			if r.launchctlOut != "" {
				return r.launchctlOut, nil
			}
			return "", nil
		},
		now:     func() time.Time { r.answerChecks(); r.now = r.now.Add(time.Second); return r.now },
		sleep:   func(context.Context, time.Duration) { r.now = r.now.Add(time.Second) },
		signals: func(ctx context.Context) (context.Context, context.CancelFunc) { return context.WithCancel(ctx) },
		uid:     501,
		home:    r.home,
		binary:  func() (string, error) { return "/opt/nova/bin/nova-friend", nil },
		copy:    r.copy,
		lookPath: func(name string) (string, error) {
			if p, ok := r.onPath[name]; ok {
				return p, nil
			}
			return "", errors.New("executable file not found in ")
		},
		random:   func() string { return "r4nd0m" },
		alive:    r.alive,
		exec:     r.opencode,
		settings: r.fs,
	}
}

// opencode is bob's opencode session behind the Exec seam: its newest
// session is the directory's, and a turn carrying a session check is
// answered with the check's nonce unless the session is deaf.
func (r *rig) opencode(_ context.Context, dir, _ string, args []string, _ string) (string, int, error) {
	if args[0] == "session" {
		return `[{"id":"ses_1","directory":"` + dir + `","updated":1}]`, 0, nil
	}
	text := args[len(args)-1]
	if nonce, ok := strings.CutPrefix(strings.SplitN(text, "\n", 2)[0], friend.SessionCheckPrefix); ok && !r.deaf {
		r.answer(nonce)
	}
	return "", 0, nil
}

// answerChecks is bob's session, alive: each session check the daemon put
// on his own stream (a passive harness reads it there) is answered once, from
// bob, with its nonce, as the pong verb sends it.
func (r *rig) answerChecks() {
	es, err := r.store.Range(context.Background(), bus.StreamOf("bob"), "-", "+", 0)
	if err != nil {
		return // ignored: a store that is down answers nothing; the test that wants it down says so
	}
	for _, e := range es {
		if nonce, ok := strings.CutPrefix(e.Message().Subject, friend.SessionCheckPrefix); ok && !r.answered[nonce] {
			r.answer(nonce)
		}
	}
}

// answer is bob's session sending its pong for nonce.
func (r *rig) answer(nonce string) {
	if r.answered == nil {
		r.answered = map[string]bool{}
	}
	r.answered[nonce] = true
	b := &bus.Bus{Store: r.store}
	_, _ = b.Send(context.Background(), bus.Message{From: "bob", To: []string{"ada"}, Subject: friend.PongSubject, Body: friend.PongLine(nonce, 0, 0, 0) + "\n"}) // ignored: a pong that is not sent leaves the friend down, which the test reads
}

// stopAfter cancels the run once the rig's clock is d past the start: an
// adapter's daemon parks on the stream, which the fake answers at once, so
// no pause counts the loop's steps; the clock does.
func stopAfter(w *world, cancel *context.CancelFunc, d time.Duration) {
	now := w.now
	w.now = func() time.Time {
		t := now()
		if t.Sub(start) > d && *cancel != nil {
			(*cancel)()
		}
		return t
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
		{"check delivery nothing given", []string{"check", "--harness", "opencode"}, []string{"--as is required", "--dir is required"}},
		{"check bad harness and window", []string{"check", "--as", "bob", "--harness", "vim", "--dir", "d", "--within", "0s"}, []string{`--harness "vim" is no harness`, "--within wants a positive duration"}},
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
	assert.False(t, friend.IsWake(m.Body), "a plain ping asks no wake turn")
	cli.Do(t, "ping", "--as", "ada", "--to", "bob", "--nonce", "wake01", "--wake").Exit(0).Out("PING OK nonce=wake01")
	entries, err = r.store.Range(context.Background(), bus.StreamOf("bob"), "-", "+", 10)
	require.NoError(t, err)
	require.Len(t, entries, 3)
	assert.True(t, friend.IsWake(entries[2].Message().Body), "--wake carries the wake line")
	// the daemon's answer alone never satisfies wait-pong: only the session's pong does
	_, err = (&bus.Bus{Store: r.store}).Send(context.Background(), bus.Message{From: "bob", To: []string{"ada"}, Subject: friend.DaemonPongSubject, Body: "daemon-pong wake01\n"})
	require.NoError(t, err)
	cli.Do(t, "wait-pong", "--from", "bob", "--nonce", "wake01", "--timeout", "2s").Exit(1).Err("WAIT-PONG NONE daemon=true", "the daemon answered and the session did not: deaf")

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
	require.NoError(t, friend.WriteStatus(state, friend.Status{Friend: "bob", Harness: "opencode", At: start, Seat: "ada", LastPing: start.Add(-time.Minute), Connection: friend.Connected, Challenge: friend.Challenged, Nonce: "n1", LastDaemonPong: start.Add(-30 * time.Second), Beats: 7, Width: 4, Delivered: 2, BeatError: "the sprint server at 127.0.0.1:6390 did not answer"}))
	require.NoError(t, friend.WritePong(state, friend.Pong{Nonce: "n0", At: start.Add(-2 * time.Minute), Queue: 3, Working: 1, Width: 8}))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "inbox"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "inbox", "QUEUE.json"), []byte(`{"tasks":[{"id":"a","state":"queued"},{"id":"b","state":"working"}]}`), 0o644))
	cli.Do(t, "status", "--as", "bob", "--dir", dir).Exit(0).
		Out("STATUS OK daemon=up harness=opencode status_age=1s connection=connected seat=ada last_ping=2026-10-04T02:59:00Z ping_age=1m1s challenge=challenged nonce=n1 last_pong=2026-10-04T02:58:00Z session_pong_age=2m1s daemon_pong_age=31s pongs=0 queue=1 working=1 width=8 beats=7 last_beat=- delivered=2 envelope=0 envelope_bytes=0 session=- mode=-",
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
		Out("INSTALL OK label=com.nova.friend-bob plist="+plist, `INSTALL RAN command="launchctl bootout gui/501/com.nova.friend-bob"`, `INSTALL RAN command="launchctl bootstrap gui/501 `+plist+`"`,
			"INSTALL NOTE check: CHECK OK harness=opencode took=", "NOTE check it: nova-friend status --as bob --dir /w/bob")
	assert.Equal(t, []string{"bootout gui/501/com.nova.friend-bob", "print gui/501/com.nova.friend-bob", "bootstrap gui/501 " + plist}, r.launchctl)
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

// The verb wires the removable-volume rule (docs/SPEC-FRIEND.md). The binary
// path is fake and the copy is the test's, so nothing is installed on the machine.
func TestInstallVerbRefusesOrCopiesABinaryOnARemovableVolume(t *testing.T) {
	t.Parallel()
	const src = "/Volumes/disk/bin/nova-friend"

	r := newRig(t, "ada", "bob")
	dst := friend.InstalledBinary(r.home)
	var copied []string
	r.copy = func(from, to string) error {
		copied = append(copied, from+" -> "+to)
		return nil
	}
	w := r.world()
	w.binary = func() (string, error) { return src, nil }
	cli := testkit.Main(func(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
		return run(args, stdin, stdout, stderr, w)
	})
	plist := filepath.Join(r.home, "Library", "LaunchAgents", "com.nova.friend-bob.plist")
	cli.Do(t, "install", "--as", "bob", "--harness", "opencode", "--dir", "/w/bob", "--dry-run").Exit(0).
		Out(`INSTALL PLAN command="copy ` + src + " " + dst + `"`)
	assert.Empty(t, copied, "a dry run copies nothing")
	assert.Empty(t, r.launchctl)
	assert.NoFileExists(t, plist)

	cli.Do(t, "install", "--as", "bob", "--harness", "opencode", "--dir", "/w/bob").Exit(0).
		Out("INSTALL OK label=com.nova.friend-bob plist=" + plist)
	assert.Equal(t, []string{src + " -> " + dst}, copied)
	raw, err := os.ReadFile(plist)
	require.NoError(t, err)
	assert.Contains(t, string(raw), "<string>"+dst+"</string>")
	assert.NotContains(t, string(raw), "/Volumes/")
	assert.Equal(t, []string{"bootout gui/501/com.nova.friend-bob", "print gui/501/com.nova.friend-bob", "bootstrap gui/501 " + plist}, r.launchctl)

	refused := newRig(t, "ada", "bob")
	refused.copy = func(string, string) error { return errors.New("disk full") }
	w = refused.world()
	w.binary = func() (string, error) { return src, nil }
	cli = testkit.Main(func(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
		return run(args, stdin, stdout, stderr, w)
	})
	plist = filepath.Join(refused.home, "Library", "LaunchAgents", "com.nova.friend-bob.plist")
	cli.Do(t, "install", "--as", "bob", "--harness", "opencode", "--dir", "/w/bob").Exit(2).
		Err("INSTALL REFUSED", "removable volume", "disk full")
	assert.Empty(t, refused.launchctl)
	assert.NoFileExists(t, plist)
}

// The beat is a verb of the tool the daemon's agent runs, and the agent
// install writes is the only plist a friend needs: the daemon beats while it
// runs (docs/SPEC-FRIEND.md, the loop), so the beat needs no agent of its
// own and the hand plists are retired. The finding of 2026-10-04: a
// friend-beat agent copied in by hand, for a friend whose harness is the
// ChatGPT app, fails to bootstrap (launchd answers Input/output error on a
// plist that lints fine), where the daemon's own install already retries that
// bootstrap; the verb is the daemon's own call, on its own, for the canary.
func TestInstallWritesTheBeatVerbAndNoHandPlist(t *testing.T) {
	t.Parallel()
	r := newRig(t, "ada", "bob")
	w := r.world()
	w.beat = func(_ context.Context, server, name string, _ time.Time, _ friend.BeatWords) (string, error) {
		r.beats = append(r.beats, server+" "+name)
		return "FRIEND-BEAT " + name, r.beatErr
	}
	cli := testkit.Main(func(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
		return run(args, stdin, stdout, stderr, w)
	})
	agents := filepath.Join(r.home, "Library", "LaunchAgents")
	plist := filepath.Join(agents, "com.nova.friend-bob.plist")
	cli.Do(t, "install", "--as", "bob", "--harness", "opencode", "--dir", "/w/bob", "--width", "4", "--server", "127.0.0.1:6390").Exit(0).
		Out("INSTALL OK label=com.nova.friend-bob plist=" + plist)
	raw, err := os.ReadFile(plist)
	require.NoError(t, err)
	assert.Contains(t, string(raw), "<string>run</string>", "the agent runs the daemon, and the daemon beats while it runs (docs/SPEC-FRIEND.md, the loop)")
	assert.Contains(t, string(raw), "<string>--server</string>", "the beat's server is the agent's own flag")
	assert.NotContains(t, string(raw), "StartInterval", "the agent is the daemon's RunAtLoad shape, never a hand plist's timer")
	written, err := os.ReadDir(agents)
	require.NoError(t, err)
	assert.Len(t, written, 1, "install writes the daemon's agent alone; the beat needs no plist of its own")
	cli.Do(t, "beat", "--as", "bob", "--server", "127.0.0.1:6390", "--dry-run").Exit(0).Out("BEAT OK as=bob server=127.0.0.1:6390 dry_run=true")
	assert.Empty(t, r.beats, "a dry run sends no beat")
	cli.Do(t, "beat", "--as", "bob", "--server", "127.0.0.1:6390").Exit(0).Out("BEAT OK as=bob server=127.0.0.1:6390")
	assert.Equal(t, []string{"127.0.0.1:6390 bob"}, r.beats, "the verb makes the same call the daemon's loop makes")
	r.beatErr = errors.New("the sprint server at 127.0.0.1:6390 did not answer")
	cli.Do(t, "beat", "--as", "bob").Exit(2).Err("BEAT REFUSED", "did not answer")
}

// run over the fake store, the fake opencode session and a cancelled context: the
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
	w.beat = func(context.Context, string, string, time.Time, friend.BeatWords) (string, error) {
		beats++
		if beats == 3 {
			cancel()
		}
		return "FRIEND-BEAT OK bob at=2026-10-04T03:00:00Z row_mode=batch row_width=2", nil
	}
	dir := t.TempDir()
	var out, errb strings.Builder
	code := run([]string{"run", "--as", "bob", "--harness", "opencode", "--dir", dir, "--width", "4"}, strings.NewReader(""), &out, &errb, w)
	assert.Equal(t, 0, code, errb.String())
	s, found, err := friend.ReadStatus(friend.StateDirIn(dir))
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, "bob", s.Friend)
	assert.Equal(t, 3, beats)
	assert.GreaterOrEqual(t, s.Beats, 1, "the count in the file lags up to StatusEvery")
	assert.Equal(t, 2, s.Width, "the row's width, read from the beat's answer, over --width")
	assert.Equal(t, "batch", s.Mode, "the row's mode, read from the beat's answer")
}

// The beat's row_config_dir= (or --config-dir over it) is the directory a
// claude lane runs with: the row one-shot with one, the daemon runs lanes.
func TestRunReadsTheConfigDirOffTheBeat(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, answer string
		flags        []string
	}{
		{"from the beat", " row_config_dir=/accounts/heavy-a", nil},
		{"the override", "", []string{"--config-dir", "/accounts/heavy-a"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := newRig(t, "ada", "bob")
			w := r.world()
			var cancel context.CancelFunc
			w.signals = func(ctx context.Context) (context.Context, context.CancelFunc) {
				ctx, cancel = context.WithCancel(ctx)
				return ctx, cancel
			}
			beats := 0
			w.beat = func(context.Context, string, string, time.Time, friend.BeatWords) (string, error) {
				beats++
				if beats == 3 {
					cancel()
				}
				return "FRIEND-BEAT OK bob at=2026-10-04T03:00:00Z row_mode=one-shot row_width=2" + tc.answer, nil
			}
			var out, errb strings.Builder
			code := run(append([]string{"run", "--as", "bob", "--harness", "claude", "--dir", t.TempDir()}, tc.flags...), strings.NewReader(""), &out, &errb, w)
			assert.Equal(t, 0, code, errb.String())
			assert.NotContains(t, out.String(), "REFUSED")
			assert.Contains(t, out.String(), "push proof: owed by the folder: claude runs each card as a process of its own, so the session check goes in as")
			assert.Contains(t, out.String(), "mode: one-shot, from batch (the friend row)")
		})
	}
	var out, errb strings.Builder
	code := run([]string{"run", "--as", "bob", "--harness", "claude", "--dir", t.TempDir(), "--config-dir", "~/accounts"}, strings.NewReader(""), &out, &errb, newRig(t, "ada", "bob").world())
	assert.Equal(t, 2, code)
	assert.Contains(t, errb.String(), `--config-dir "~/accounts" wants an absolute path`)
}

// A claude row in one-shot mode with no config_dir (and no --config-dir) is
// refused on the daemon's record with the remedy, and no lane runs.
func TestRunRefusesAClaudeOneShotRowWithoutAConfigDir(t *testing.T) {
	t.Parallel()
	r := newRig(t, "ada", "bob")
	w := r.world()
	var cancel context.CancelFunc
	w.signals = func(ctx context.Context) (context.Context, context.CancelFunc) {
		ctx, cancel = context.WithCancel(ctx)
		return ctx, cancel
	}
	beats := 0
	w.beat = func(context.Context, string, string, time.Time, friend.BeatWords) (string, error) {
		beats++
		if beats == 3 {
			cancel()
		}
		return "FRIEND-BEAT OK bob at=2026-10-04T03:00:00Z row_mode=one-shot row_width=2", nil
	}
	dir := t.TempDir()
	var out, errb strings.Builder
	code := run([]string{"run", "--as", "bob", "--harness", "claude", "--dir", dir}, strings.NewReader(""), &out, &errb, w)
	assert.Equal(t, 0, code, errb.String())
	s, _, err := friend.ReadStatus(friend.StateDirIn(dir))
	require.NoError(t, err)
	assert.Equal(t, "batch", s.Mode, "the row says one-shot and names no config_dir: claude is refused")
	assert.Contains(t, out.String(), "mode: one-shot REFUSED: friend bob is a claude friend in one-shot mode with no config_dir")
	assert.Contains(t, out.String(), "run: nova-config friend set bob --config_dir <her account's absolute config directory>, or nova-friend run --config-dir <dir>")
}

// A claude one-shot lane runs its card inside the lane wall, and the wall's
// --config-dir is the row's config_dir when no --config-dir is given (the
// flag over it when it is): the card's process is a lane's, never the plain
// daemon's, so it cannot write outside the friend's directories.
func TestAClaudeOneShotLaneRunsWalledWithTheRowsConfigDir(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, want string
		flags      []string
	}{
		{"the row's", "/accounts/heavy-a", nil},
		{"the flag over it", "/accounts/other", []string{"--config-dir", "/accounts/other"}},
	} {
		t.Run(tc.name, func(t *testing.T) { // not parallel: a bubble at a time
			synctest.Test(t, func(t *testing.T) {
				r := newRig(t, "ada", "bob")
				w := r.world()
				var cancel context.CancelFunc
				w.signals = func(ctx context.Context) (context.Context, context.CancelFunc) {
					ctx, cancel = context.WithCancel(ctx)
					return ctx, cancel
				}
				w.sleep = func(context.Context, time.Duration) { synctest.Wait() }
				dir := t.TempDir()
				require.NoError(t, os.MkdirAll(filepath.Join(dir, "inbox", "c1~15"), 0o755))
				require.NoError(t, os.WriteFile(filepath.Join(dir, "inbox", "QUEUE.json"), []byte(`{"tasks":[{"id":"c1","state":"queued"}]}`), 0o644))
				require.NoError(t, os.WriteFile(filepath.Join(dir, "inbox", "c1~15", "BRIEF.md"), []byte("RESULT: c1\n"), 0o644))
				var mu sync.Mutex
				var walls []string // the config dir of each wall a lane's child ran in
				var plain int      // children run outside any lane's wall
				w.wall = func(wl friend.Wall, _ friend.Exec) friend.Exec {
					return func(ctx context.Context, _, _ string, _ []string, _ string) (string, int, error) {
						mu.Lock()
						defer mu.Unlock()
						if !friend.InLane(ctx) {
							plain++
							return "", 0, nil
						}
						walls = append(walls, wl.ConfigDir)
						out := filepath.Join(dir, "outbox", "c1~15")
						if err := os.MkdirAll(out, 0o755); err != nil {
							return "", 0, err
						}
						for _, f := range []string{"REPORT.md", "RESULT.md"} {
							if err := os.WriteFile(filepath.Join(out, f), []byte("done\n"), 0o644); err != nil {
								return "", 0, err
							}
						}
						return `{"type":"result","subtype":"success","is_error":false,"total_cost_usd":0.0125,"session_id":"s1","result":"ok"}` + "\n", 0, nil
					}
				}
				beats := 0
				w.beat = func(context.Context, string, string, time.Time, friend.BeatWords) (string, error) {
					beats++
					if beats == 12 {
						cancel()
					}
					return "FRIEND-BEAT OK bob at=2026-10-04T03:00:00Z row_mode=one-shot row_width=1 row_config_dir=/accounts/heavy-a", nil
				}
				var out, errb strings.Builder
				code := run(append([]string{"run", "--as", "bob", "--harness", "claude", "--dir", dir, "--coordinator", "ada"}, tc.flags...), strings.NewReader(""), &out, &errb, w)
				require.Equal(t, 0, code, errb.String())
				mu.Lock()
				defer mu.Unlock()
				require.NotEmpty(t, walls, "the card ran inside a lane's wall\n%s", out.String())
				assert.Equal(t, tc.want, walls[0], "the wall's --config-dir")
				assert.Zero(t, plain, "no child of the lane ran outside the wall")
				assert.Contains(t, out.String(), "card=done")
				assert.Equal(t, 1, strings.Count(out.String(), "spend: harness=claude runs=1 cost_usd=0.0125"), "the beat after the run says its cost once, not on every beat\n%s", out.String())
			})
		})
	}
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
	w.beat = func(context.Context, string, string, time.Time, friend.BeatWords) (string, error) {
		beats++
		if beats == 2 {
			cancel()
		}
		return "", nil
	}
	var out, errb strings.Builder
	code := run([]string{"run", "--as", "bob", "--harness", "opencode", "--dir", t.TempDir()}, strings.NewReader(""), &out, &errb, w)
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
	code = run([]string{"run", "--as", "bob", "--harness", "opencode", "--dir", t.TempDir()}, strings.NewReader(""), &out, &errb, w)
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
		Out(`delivered=0 envelope=0 envelope_bytes=0 session=broken mode=- held=- inbox=- missing=- session_id=ses_x broken_at=2026-10-04T02:59:00Z status=down reason="invalid_request_error: bad input"`,
			"NOTE the session is broken: the provider refused the same way turn after turn")
}

// A claude friend is reached by the open session's own wait: install
// prints the one line the session runs, and status says the route is
// passive with that line, never a silent loss.
func TestClaudeInstallPrintsTheSessionsWaitAndStatusSaysPassive(t *testing.T) {
	t.Parallel()
	r := newRig(t, "ada", "bob")
	cli := r.cli()
	dir := "/w/bob" // the rig's fake file system has it
	// install names the wake file in the state directory the daemon will keep, <dir>/.nova-friend
	planned := "nova-bus wait --as bob --after <cursor> --wake-file /w/bob/.nova-friend/bob.wake"
	cli.Do(t, "install", "--as", "bob", "--harness", "claude", "--dir", dir, "--config-dir", "/w/bob-claude", "--dry-run").Exit(0).Out("NOTE run as a background task", planned)
	// status names it in the state directory the daemon did keep
	state := friend.DefaultStateDir(r.home, "bob")
	wait := "nova-bus wait --as bob --after <cursor> --wake-file " + filepath.Join(state, "bob.wake")
	cli.Do(t, "install", "--as", "bob", "--harness", "opencode", "--dir", dir, "--dry-run").Exit(0).NotOut("nova-bus wait")
	require.NoError(t, friend.WriteStatus(state, friend.Status{Friend: "bob", Harness: "claude", At: start, Connection: friend.Connected, Challenge: friend.Quiet}))
	cli.Do(t, "status", "--as", "bob", "--dir", dir).Exit(0).Out("route=passive", wait)
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

// The row's one-shot mode, read from the beat, wires the lanes end to end:
// the lane opens its own session of the friend through opencode (a run with
// no --session, seeded from her own files), keeps it in the state directory,
// hands it the queued card with the bus line to send, and the friend's
// directory is allowed in her project config, the home directory's symlink
// to it too.
func TestRunInOneShotModeOpensALaneAndHandsItTheCard(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, "ada", "bob")
		r.onPath = map[string]string{"nova-bus": "/opt/nova/bin/nova-bus"}
		w := r.world()
		var cancel context.CancelFunc
		w.signals = func(ctx context.Context) (context.Context, context.CancelFunc) {
			ctx, cancel = context.WithCancel(ctx)
			return ctx, cancel
		}
		w.sleep = func(context.Context, time.Duration) { synctest.Wait() }
		dir := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(dir, "inbox", "c1~15"), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "inbox", "QUEUE.json"), []byte(`{"tasks":[{"id":"c1","state":"queued"}]}`), 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "inbox", "c1~15", "BRIEF.md"), []byte("RESULT: c1\n"), 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("I am bob.\n"), 0o644))
		require.NoError(t, os.Symlink(dir, filepath.Join(r.home, "bob-working")))
		var mu sync.Mutex
		var runs []string
		lists := 0
		checks := 0
		w.exec = func(_ context.Context, _, name string, args []string, _ string) (string, int, error) {
			mu.Lock()
			defer mu.Unlock()
			if name == "sysctl" {
				return "{ 0.00 0.00 0.00 }\n", 0, nil // the daemon's load read where there is no /proc/loadavg (world.load1): not a harness run
			}
			if args[0] == "--version" || len(args) > 1 && args[1] == "--help" {
				return "", 0, nil // the daemon's read of the installed opencode (OpenCode.CheckRun): it cannot tell
			}
			if checks < 1 { // the daemon's first check, the push proof, goes into her newest session first; her answer brings her up, and the beat with the row
				if args[0] == "session" {
					return `[{"id":"ses_main","directory":"` + dir + `","updated":1}]`, 0, nil
				}
				text := args[len(args)-1]
				if nonce, ok := strings.CutPrefix(strings.SplitN(text, "\n", 2)[0], friend.SessionCheckPrefix); ok {
					checks++
					r.answer(nonce)
					return "answered\n", 0, nil
				}
			}
			if args[0] == "session" {
				lists++
				if lists == 1 {
					return "[]", 0, nil
				}
				return `[{"id":"ses_lane1","directory":"` + dir + `","updated":1}]`, 0, nil
			}
			if args[0] == "export" { // the run's price, from her session record
				return `{"messages":[{"info":{"role":"assistant","cost":0.002}}]}`, 0, nil
			}
			runs = append(runs, strings.Join(args, " "))
			return "ok\n", 0, nil
		}
		beats := 0
		w.beat = func(context.Context, string, string, time.Time, friend.BeatWords) (string, error) {
			beats++
			if beats == 12 {
				cancel()
			}
			return "FRIEND-BEAT OK bob at=2026-10-04T03:00:00Z row_mode=one-shot row_width=1", nil
		}
		var out, errb strings.Builder
		code := run([]string{"run", "--as", "bob", "--harness", "opencode", "--dir", dir, "--coordinator", "ada"}, strings.NewReader(""), &out, &errb, w)
		require.Equal(t, 0, code, errb.String())
		require.GreaterOrEqual(t, len(runs), 2, "%v\n%s", runs, out.String())
		assert.True(t, strings.HasPrefix(runs[0], "run You are bob: one of 1 one-shot lanes of bob, this is lane 1"), runs[0])
		assert.Contains(t, runs[0], "Read "+filepath.Join(dir, "AGENTS.md")+" first")
		assert.True(t, strings.HasPrefix(runs[1], "run --session ses_lane1 nova-friend: lane 1 of 1: one card this turn, c1."), runs[1])
		assert.Contains(t, runs[1], `3. Send one bus line: /opt/nova/bin/nova-bus send --as bob --to ada --subject "card c1 done" --body "<the first line of your REPORT.md>" --redis store.test:6379`)
		lanes, err := friend.ReadLanes(friend.StateDirIn(dir))
		require.NoError(t, err)
		assert.Equal(t, map[int]string{1: "ses_lane1"}, lanes.Sessions)
		raw, err := os.ReadFile(filepath.Join(dir, "opencode.json"))
		require.NoError(t, err)
		assert.Contains(t, string(raw), `"`+filepath.Join(r.home, "bob-working")+`/**": "allow"`)
		s, _, err := friend.ReadStatus(friend.StateDirIn(dir))
		require.NoError(t, err)
		assert.Equal(t, "one-shot", s.Mode)
		assert.Contains(t, out.String(), "lane=1 session=ses_lane1")
		assert.Contains(t, out.String(), "opencode: session=ses_lane1 cost=$0.0020 total=$0.0020", "the daemon prices the lane's open from her session record")
	})
}

// A closed app is a friend down, however well its daemon runs: the verb wires
// the session check, beats up only while the session answers, each beat
// carrying its proof, says down with the check's nonce once it stops, and
// status says so (docs/SPEC-FRIEND.md, presence).
func TestRunWithNoSessionAnsweringBeatsDown(t *testing.T) {
	t.Parallel()
	r := newRig(t, "ada", "bob")
	w := r.world()
	var cancel context.CancelFunc
	w.signals = func(ctx context.Context) (context.Context, context.CancelFunc) {
		ctx, cancel = context.WithCancel(ctx)
		return ctx, cancel
	}
	nonces := []string{"pr00f1"} // the first check's nonce, the push proof; every later check's is r4nd0m
	w.random = func() string {
		if len(nonces) == 0 {
			return "r4nd0m"
		}
		n := nonces[0]
		nonces = nonces[1:]
		return n
	}
	var mu sync.Mutex
	var checks []string
	w.exec = func(_ context.Context, dir, _ string, args []string, _ string) (string, int, error) {
		if args[0] == "session" {
			return `[{"id":"ses_1","directory":"` + dir + `","updated":1}]`, 0, nil
		}
		text := args[len(args)-1]
		if nonce, ok := strings.CutPrefix(strings.SplitN(text, "\n", 2)[0], friend.SessionCheckPrefix); ok {
			mu.Lock()
			checks = append(checks, text)
			mu.Unlock()
			if nonce == "pr00f1" {
				r.answer(nonce) // the session answers the first check, and never again
			}
		}
		return "", 0, nil
	}
	var proofs []string
	w.beat = func(_ context.Context, _, _ string, _ time.Time, words friend.BeatWords) (string, error) {
		mu.Lock()
		if words.Pong != "" {
			proofs = append(proofs, words.Pong)
		}
		mu.Unlock()
		return "", nil
	}
	var downs []string
	w.beatDown = func(_ context.Context, _, _ string, _, _ time.Time, reason string, _ friend.BeatWords) error {
		mu.Lock()
		downs = append(downs, reason)
		mu.Unlock()
		return nil
	}
	stopAfter(&w, &cancel, 17*time.Minute) // the session's own blocking read never pauses the loop; the clock ends it
	dir := t.TempDir()
	var out, errb strings.Builder
	code := run([]string{"run", "--as", "bob", "--harness", "opencode", "--dir", dir, "--coordinator", "ada"}, strings.NewReader(""), &out, &errb, w)
	require.Equal(t, 0, code, errb.String())
	assert.Contains(t, out.String(), "push proof: proved: the session answered")
	assert.Contains(t, out.String(), "presence: down: no session answer within 5m0s")
	mu.Lock()
	assert.Equal(t, []string{"pr00f1"}, proofs, "her up beat names the check her session answered, once; the unanswered one never")
	require.NotEmpty(t, downs)
	assert.Contains(t, downs[0], "push unproven: session check pr00f1", "down until the first answer")
	assert.Contains(t, downs[len(downs)-1], "no session answer to session check r4nd0m within 5m0s", "down with the check's nonce once it stops answering")
	require.Len(t, checks, 2, "the first check, the push proof, then the next after the quiet")
	assert.Contains(t, checks[1], "nova-friend pong --as bob --nonce r4nd0m", "the check carries the one line to run")
	assert.Contains(t, checks[1], "--to ada")
	mu.Unlock()
	st, _, err := friend.ReadStatus(friend.StateDirIn(dir))
	require.NoError(t, err)
	r.now = st.At // read as the daemon last wrote it: up
	r.cli().Do(t, "status", "--as", "bob", "--dir", dir).Exit(0).
		Out("presence=down", `presence_reason="no session answer"`, "NOTE the daemon is up and the session is not (no session answer)")
}

// A harness at its limit is down until its reset, through the verb: the turn whose
// output says the limit is deferred, the message kept in hand; the friend is down on
// the presence file with the limit's reason until she is woken; nothing goes into her
// session before the reset; after it a wake turn whose answer carries its nonce, and
// then the message (docs/SPEC-FRIEND.md, a harness at its limit).
func TestRunHoldsAHarnessAtItsLimitUntilItsResetThenWakesIt(t *testing.T) {
	t.Parallel()
	r := newRig(t, "ada", "bob")
	_, err := (&bus.Bus{Store: r.store}).Send(context.Background(), bus.Message{From: "ada", To: []string{"bob"}, Subject: "card c9", Body: "go\n"})
	require.NoError(t, err)
	dir := t.TempDir()
	state := friend.StateDirIn(dir)
	w := r.world()
	checksStarted := 0
	w.checkGo = func(f func()) {
		checksStarted++
		if checksStarted == 1 { // establish initial proof before the fake clock jumps; later turns remain asynchronous
			f()
			return
		}
		go f()
	}
	w.friends = func(context.Context, string) ([]friend.WakeRow, string, error) {
		return nil, "ada", nil // the carried startup note comes from the actual seat
	}
	var mu sync.Mutex
	clock := start
	w.now = func() time.Time { mu.Lock(); defer mu.Unlock(); clock = clock.Add(time.Second); return clock }
	var cancel context.CancelFunc
	w.signals = func(ctx context.Context) (context.Context, context.CancelFunc) {
		ctx, cancel = context.WithCancel(ctx)
		return ctx, cancel
	}
	sleeps := 0
	w.sleep = func(context.Context, time.Duration) {
		mu.Lock()
		defer mu.Unlock()
		clock = clock.Add(5 * time.Second)
		if sleeps++; sleeps == 2000 {
			cancel() // a bound on the test, never reached when it passes
		}
	}
	var beats []time.Time
	w.beat = func(context.Context, string, string, time.Time, friend.BeatWords) (string, error) {
		mu.Lock()
		beats = append(beats, clock)
		mu.Unlock()
		return "FRIEND-BEAT OK bob at=2026-10-04T03:00:00Z row_mode=batch row_width=1", nil
	}
	type downBeat struct {
		at, until time.Time
		reason    string
	}
	var downBeats []downBeat
	w.beatDown = func(_ context.Context, _, _ string, _, until time.Time, reason string, _ friend.BeatWords) error {
		mu.Lock()
		downBeats = append(downBeats, downBeat{clock, until, reason})
		mu.Unlock()
		return nil
	}
	type turn struct {
		at       time.Time
		kind     string
		presence friend.PresenceStatus
	}
	var turns []turn
	limited := false
	w.exec = func(_ context.Context, _, _ string, args []string, _ string) (string, int, error) {
		text := args[len(args)-1]
		p, _, _ := friend.ReadPresence(state) // ignored: a missing file reads as the zero presence, which the test sees
		mu.Lock()
		defer mu.Unlock()
		now := clock
		switch {
		case strings.HasPrefix(text, friend.SessionCheckPrefix):
			nonce, _, _ := strings.Cut(strings.TrimPrefix(text, friend.SessionCheckPrefix), "\n")
			r.answer(nonce)
			return "answered\n", 0, nil
		case strings.HasPrefix(text, "nova-friend: your harness's usage limit has reset"):
			turns = append(turns, turn{now, "wake", p})
			fields := strings.Fields(text)
			return fields[len(fields)-1] + "\n", 0, nil
		case strings.Contains(text, "card c9"):
			if !limited {
				limited = true
				turns = append(turns, turn{now, "limit", p})
				return "working\nInsufficient AI Credits. Your credits will refresh in 10 minutes.\n", 1, nil
			}
			turns = append(turns, turn{now, "message", p})
			cancel()
			return "I ran card c9.\n", 0, nil
		}
		return "", 0, nil
	}
	out, errb := &lockedBuilder{mu: &mu}, &strings.Builder{}
	code := run([]string{"run", "--as", "bob", "--harness", "opencode", "--session", "ses_main", "--dir", dir, "--coordinator", "ada"}, strings.NewReader(""), out, errb, w)
	require.Equal(t, 0, code, errb.String())
	mu.Lock()
	defer mu.Unlock()
	require.Len(t, turns, 3, "the limited turn, one wake, the message; nothing between\n%s", out.String())
	assert.Equal(t, []string{"limit", "wake", "message"}, []string{turns[0].kind, turns[1].kind, turns[2].kind})
	assert.Contains(t, out.String(), "limit: down until ")
	assert.Contains(t, out.String(), "Insufficient AI Credits")
	assert.Contains(t, out.String(), "the turn hit the harness's limit", "the turn is deferred, the message in hand")
	assert.False(t, turns[1].at.Before(turns[0].at.Add(10*time.Minute)), "no wake before the reset: %s then %s", turns[0].at, turns[1].at)
	assert.Equal(t, friend.PresenceDown, turns[1].presence.Presence, "down on the presence file until woken")
	assert.Contains(t, turns[1].presence.Reason, "harness limit until ")
	assert.Contains(t, out.String(), "limit: woken: the session answered r4nd0m after the reset")
	// down from the record of it (a beat while the limited turn still ran was before anyone knew), to the wake
	_, rest, ok := strings.Cut(out.String(), "RUN ")
	for ok && !strings.Contains(strings.SplitN(rest, "\n", 2)[0], " limit: down until ") {
		_, rest, ok = strings.Cut(rest, "RUN ")
	}
	require.True(t, ok, "the limit is on the record")
	downAt, err := time.Parse(time.RFC3339, strings.Fields(rest)[0])
	require.NoError(t, err)
	assert.False(t, slices.ContainsFunc(beats, func(b time.Time) bool { return b.After(downAt) && b.Before(turns[1].at) }),
		"no up beat while she is down (%s to %s): %v", downAt, turns[1].at, beats)
	// her beat says down instead, with the until and the reason (limits-mean-down-w-r5.w1~15)
	require.NotEmpty(t, downBeats, "she beats down while limited")
	limitBeats := 0
	for _, b := range downBeats {
		if strings.HasPrefix(b.reason, "push unproven: ") {
			// the daemon's start, before its first check is answered: its own word, down
			assert.True(t, b.at.Before(turns[0].at), "a push-unproven down beat only at the start: %s", b.at)
			continue
		}
		if strings.HasPrefix(b.reason, "no session answer to session check ") {
			// The fake clock may outrun an asynchronous check turn. Its unanswered
			// nonce must still beat down, never manufacture an up session.
			continue
		}
		limitBeats++
		assert.False(t, b.at.Before(turns[0].at) || b.at.After(turns[2].at), "a down beat only while limited, the wake turn's answer ending it: %s", b.at)
		assert.False(t, b.until.Before(turns[0].at.Add(10*time.Minute)), "until the reset the text named: %s", b.until)
		assert.Equal(t, "harness limit: Insufficient AI Credits. Your credits will refresh in 10 minutes.", b.reason)
	}
	assert.Positive(t, limitBeats, "the harness limit itself must be reported down")
	got, err := r.store.Range(context.Background(), bus.StreamOf("ada"), "-", "+", 0)
	require.NoError(t, err)
	var told []string
	for _, e := range got {
		told = append(told, e.Fields["subject"]+"\n"+e.Fields["body"])
	}
	all := strings.Join(told, "\n")
	assert.Contains(t, all, "friend bob down: her harness is at its limit until ", "the coordinator is told she is down")
	assert.Contains(t, all, "nova-sprint friend down bob --reason 'harness limit: Insufficient AI Credits. Your credits will refresh in 10 minutes.' --until ")
	assert.Contains(t, all, "friend bob back", "and that she is back")
}

// lockedBuilder is a strings.Builder under the test's lock: the daemon writes
// its lines and the harness's output from the turn's goroutine and its own.
type lockedBuilder struct {
	mu *sync.Mutex
	b  strings.Builder
}

func (l *lockedBuilder) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

// String is read with the lock held by the caller.
func (l *lockedBuilder) String() string { return l.b.String() }

// A friend whose harness app is not running and whose session answers is up
// (the finding of 2026-10-05): run wires HarnessWatch beside the daemon's
// beat, advisory; the beats reach the sprint server once the session answers
// its check, the record says the app was not seen, and status says
// harness_seen=not-seen (docs/SPEC-FRIEND.md, the harness check).
func TestRunKeepsTheHarnessWatchAdvisory(t *testing.T) {
	t.Parallel()
	r := newRig(t, "ada", "bob")
	r.alive = fakeAlive{running: false, why: "the harness app is closed"}
	w := r.world()
	var cancel context.CancelFunc
	w.signals = func(ctx context.Context) (context.Context, context.CancelFunc) {
		ctx, cancel = context.WithCancel(ctx)
		return ctx, cancel
	}
	beats := 0
	w.beat = func(context.Context, string, string, time.Time, friend.BeatWords) (string, error) {
		beats++
		return "", nil
	}
	stopAfter(&w, &cancel, 5*time.Minute)
	dir := t.TempDir()
	var out, errb strings.Builder
	code := run([]string{"run", "--as", "bob", "--harness", "opencode", "--dir", dir, "--coordinator", "ada"}, strings.NewReader(""), &out, &errb, w)
	require.Equal(t, 0, code, errb.String())
	assert.Positive(t, beats, "the session answered: beats reach the sprint server with no app running")
	assert.Contains(t, out.String(), "harness: not seen: the harness app is closed; advisory")
	assert.NotContains(t, out.String(), "harness not running")

	st, _, err := friend.ReadStatus(friend.StateDirIn(dir))
	require.NoError(t, err)
	assert.Empty(t, st.BeatError)
	assert.Equal(t, friend.HarnessNotSeen, st.HarnessSeen)

	r.now = st.At
	r.cli().Do(t, "status", "--as", "bob", "--dir", dir).Exit(0).
		Out("status=up", "harness not seen", `harness_seen="not-seen"`).NotOut("the last beat failed")
}

// The daemon's state directory is under --dir by default, where a sandboxed
// session may write its pong (the finding of 2026-10-05: zhi's pong.json was
// refused at ~/.nova-friend/zhi, outside her --dir); --state-dir is still
// honoured; a directory that refuses it falls back to the home directory's,
// said on the record.
func TestTheDaemonsStateDirIsUnderItsDir(t *testing.T) {
	t.Parallel()
	runOnce := func(t *testing.T, r *rig, args ...string) string {
		t.Helper()
		w := r.world()
		var cancel context.CancelFunc
		w.signals = func(ctx context.Context) (context.Context, context.CancelFunc) {
			ctx, cancel = context.WithCancel(ctx)
			return ctx, cancel
		}
		w.beat = func(context.Context, string, string, time.Time, friend.BeatWords) (string, error) { return "", nil }
		stopAfter(&w, &cancel, 3*time.Minute)
		var out, errb strings.Builder
		code := run(append([]string{"run", "--as", "bob", "--harness", "opencode", "--coordinator", "ada"}, args...), strings.NewReader(""), &out, &errb, w)
		require.Equal(t, 0, code, errb.String())
		return out.String()
	}

	t.Run("default: <dir>/.nova-friend", func(t *testing.T) {
		t.Parallel()
		r := newRig(t, "ada", "bob")
		dir := t.TempDir()
		runOnce(t, r, "--dir", dir)
		_, found, err := friend.ReadStatus(friend.StateDirIn(dir))
		require.NoError(t, err)
		assert.True(t, found, "the status file is under --dir")
		assert.NoDirExists(t, friend.DefaultStateDir(r.home, "bob"), "nothing under the home directory")
		r.cli().Do(t, "status", "--as", "bob", "--dir", dir).Exit(0).Out("STATUS OK daemon=", "harness=opencode") // status finds it there with no --state-dir
		// the session's pong line names the same directory, so its pong lands where the daemon reads
		r.cli().Do(t, "pong", "--as", "bob", "--nonce", "n1", "--to", "ada", "--state-dir", friend.StateDirIn(dir)).Exit(0)
		p, found, err := friend.ReadPong(friend.StateDirIn(dir))
		require.NoError(t, err)
		assert.True(t, found)
		assert.Equal(t, "n1", p.Nonce)
	})

	t.Run("--state-dir is honoured", func(t *testing.T) {
		t.Parallel()
		r := newRig(t, "ada", "bob")
		dir, state := t.TempDir(), t.TempDir()
		runOnce(t, r, "--dir", dir, "--state-dir", state)
		_, found, err := friend.ReadStatus(state)
		require.NoError(t, err)
		assert.True(t, found)
		assert.NoDirExists(t, friend.StateDirIn(dir))
	})

	t.Run("a directory that refuses it: the home directory's, said", func(t *testing.T) {
		t.Parallel()
		r := newRig(t, "ada", "bob")
		file := filepath.Join(t.TempDir(), "not-a-dir")
		require.NoError(t, os.WriteFile(file, nil, 0o644))
		out := runOnce(t, r, "--dir", file)
		_, found, err := friend.ReadStatus(friend.DefaultStateDir(r.home, "bob"))
		require.NoError(t, err)
		assert.True(t, found)
		assert.Contains(t, out, "state: "+friend.StateDirIn(file)+" refused (")
		assert.Contains(t, out, "give --state-dir")
	})
}

// A daemon whose installed plist names other arguments says so on start: a
// launchctl kickstart restarts what launchd loaded, so a plist edited in place
// keeps running its old arguments (the finding of 2026-10-05).
func TestADaemonWhosePlistChangedSaysSoOnStart(t *testing.T) {
	t.Parallel()
	r := newRig(t, "ada", "bob")
	dir := t.TempDir()
	plist := friend.Agent{Friend: "bob", Harness: "opencode", Dir: dir, Width: 2, Binary: "/opt/nova/bin/nova-friend", Redis: "store.test:6379", Server: "127.0.0.1:6390", Home: r.home, Coordinator: "ada"}
	require.NoError(t, os.MkdirAll(filepath.Dir(plist.PlistPath()), 0o755))
	require.NoError(t, os.WriteFile(plist.PlistPath(), []byte(plist.Plist()), 0o644))
	start := func(args ...string) string {
		w := r.world()
		var cancel context.CancelFunc
		w.signals = func(ctx context.Context) (context.Context, context.CancelFunc) {
			ctx, cancel = context.WithCancel(ctx)
			return ctx, cancel
		}
		w.beat = func(context.Context, string, string, time.Time, friend.BeatWords) (string, error) { return "", nil }
		stopAfter(&w, &cancel, time.Minute)
		var out, errb strings.Builder
		require.Equal(t, 0, run(args, strings.NewReader(""), &out, &errb, w), errb.String())
		return out.String()
	}
	same := plist.Args()[1:] // what launchd runs, after the binary
	assert.NotContains(t, start(same...), "plist drift", "the plist's own arguments: no drift")

	old := append([]string(nil), same...)
	old[slices.Index(old, "--width")+1] = "4" // the arguments loaded before the plist was edited to --width 2
	out := start(old...)
	assert.Contains(t, out, "plist drift: this daemon's arguments differ from the installed plist")
	assert.Contains(t, out, "--width 4")
	assert.Contains(t, out, "plist: run --as bob --harness opencode --dir "+dir+" --redis store.test:6379 --server 127.0.0.1:6390 --width 2")
	assert.Contains(t, out, "nova-friend install again")
}

// check runs the delivery check against the live session: one line, OK with
// how long the pong took, or FAIL with the stage at exit 1. A harness with no
// deliver command fails at deliver with its reason; install says the check's
// line in a NOTE and stays installed whatever it says.
func TestCheckSaysOKOrTheStageThatFailed(t *testing.T) {
	t.Parallel()
	r := newRig(t, "ada", "bob")
	cli := r.cli()
	state := t.TempDir()
	cli.Do(t, "check", "--as", "bob", "--harness", "opencode", "--dir", "/w/bob", "--state-dir", state).Exit(0).Out("CHECK OK harness=opencode took=")
	got := r.store.Len(bus.LogKey)
	cli.Do(t, "check", "--as", "bob", "--harness", "opencode", "--dir", "/w/bob", "--state-dir", state, "--json").Exit(0).Out(`"status":"ok"`, `"harness":"opencode"`)
	assert.Equal(t, got+1, r.store.Len(bus.LogKey), "one pong a check")

	r.deaf = true
	cli.Do(t, "check", "--as", "bob", "--harness", "opencode", "--dir", "/w/bob", "--state-dir", state, "--within", "10s").Exit(1).
		Err(`CHECK FAIL harness=opencode stage=act why="no pong r4nd0m from bob within 10s: the session did not run the line the check carried"`)
	// claude: the check is one line in the wake file of the state directory, for the session's own wait
	cli.Do(t, "check", "--as", "bob", "--harness", "claude", "--dir", "/w/bob", "--state-dir", state, "--within", "10s").Exit(1).
		Err(`CHECK FAIL harness=claude stage=act why="no pong r4nd0m from bob within 10s`)
	assert.FileExists(t, filepath.Join(state, "bob.wake"))
	cli.Do(t, "check", "--as", "bob", "--harness", "cursor", "--dir", "/w/bob", "--state-dir", state).Exit(1).
		Err(`CHECK FAIL harness=cursor stage=deliver why="no deliver command for cursor (not installed here`)

	// the plan, from the same pong line, with nothing delivered and no store opened
	r.store.Fail = errors.New("store down")
	cli.Do(t, "check", "--as", "bob", "--harness", "opencode", "--dir", "/w/bob", "--state-dir", state, "--to", "ada", "--dry-run").Exit(0).
		Out("CHECK OK harness=opencode dir=/w/bob within=5m0s", "CHECK PLAN command=\"/opt/nova/bin/nova-friend pong --as bob --nonce r4nd0m --state-dir "+state+" --redis store.test:6379 --dir /w/bob --to ada\"", "NOTE nothing was delivered")
	cli.Do(t, "check", "--as", "bob", "--harness", "opencode", "--dir", "/w/bob", "--state-dir", state).Exit(2).Err("CHECK REFUSED: the store did not answer: store down")

	// install: the check's fail is a NOTE, the agent stays loaded
	r.store.Fail = nil
	cli.Do(t, "install", "--as", "bob", "--harness", "opencode", "--dir", "/w/bob", "--within", "5s").Exit(0).
		Out("INSTALL OK label=com.nova.friend-bob", "INSTALL NOTE check: CHECK FAIL harness=opencode stage=act")
	assert.FileExists(t, filepath.Join(r.home, "Library", "LaunchAgents", "com.nova.friend-bob.plist"))
}

// status decides the friend's status from evidence and shows it beside the
// status (docs/SPEC-FRIEND.md, "A friend's status, from evidence"): a fresh
// beat with no session answer is down; a session answer inside the bound is
// up; the messages waiting on her stream are counted; the last turn's line
// of the log is shown; a limit is down until its reset; a daemon gone is a
// bus that cannot deliver.
func TestStatusIsDecidedFromEvidenceAndShowsIt(t *testing.T) {
	t.Parallel()
	r := newRig(t, "ada", "bob")
	cli := r.cli()
	dir := t.TempDir()
	state := friend.DefaultStateDir(r.home, "bob")
	require.NoError(t, friend.WriteStatus(state, friend.Status{Friend: "bob", Harness: "opencode", At: start, Connection: friend.Connected, Challenge: friend.Quiet, Beats: 40, LastBeat: start}))
	cli.Do(t, "status", "--as", "bob", "--dir", dir).Exit(0).
		Out(`daemon=up`, `status=down why="no session answer ever" evidence="harness unknown; no session answer ever; no limit; 0 undelivered; no result yet"`)

	require.NoError(t, friend.WritePong(state, friend.Pong{Nonce: "n0", At: start.Add(-time.Minute)}))
	b := &bus.Bus{Store: r.store}
	for range 2 {
		_, err := b.Send(context.Background(), bus.Message{From: "ada", To: []string{"bob"}, Subject: "work", Body: "x"})
		require.NoError(t, err)
	}
	require.NoError(t, friend.Record(state, "2026-10-04T02:50:00Z subject=work messages=1 took=3s exit=0 acked=true"))
	cli.Do(t, "status", "--as", "bob", "--dir", dir).Exit(0).
		Out(`status=up why="session answer 1m" evidence="harness unknown; session answer 1m; no limit; 2 undelivered; last result 10m exit=0"`)

	require.NoError(t, friend.WritePong(state, friend.Pong{Nonce: "n0", At: start.Add(-12 * time.Minute)}))
	cli.Do(t, "status", "--as", "bob", "--dir", dir).Exit(0).Out(`status=down why="no session answer 12m"`)

	require.NoError(t, friend.WritePong(state, friend.Pong{Nonce: "n0", At: start}))
	limit, err := json.Marshal(friend.Limit{Reason: "weekly", Until: start.Add(34 * time.Hour)})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(state, friend.LimitFile), limit, 0o644))
	cli.Do(t, "status", "--as", "bob", "--dir", dir).Exit(0).Out(`status=down why="weekly limit until ` + start.Add(34*time.Hour).Local().Format("Mon 3:04 PM") + `"`)
	require.NoError(t, os.Remove(filepath.Join(state, friend.LimitFile)))

	r.now = start.Add(friend.DaemonStale)
	cli.Do(t, "status", "--as", "bob", "--dir", dir).Exit(0).Out(`daemon=down`, `status=down why="bus cannot deliver: daemon down 31s (2 undelivered)"`)

	cli.Do(t, "status", "--as", "bob", "--dir", dir, "--redis", "").Exit(0).Out(`undelivered not counted`)
}

// TestHostHelpExampleIsWhatTheToolPrints runs the host verb's help example as
// written, over a fake tmux, through the one comparator: the line a reader
// pastes prints the line the help shows (docs/SPEC-FRIEND.md, "Hosted in tmux").
func TestHostHelpExampleIsWhatTheToolPrints(t *testing.T) {
	t.Parallel()
	step := onboarding.Step{
		Line: "$ nova-friend host --as bob --harness aider --dir ./bob --dry-run -- aider",
		Args: []string{"host", "--as", "bob", "--harness", "aider", "--dir", "./bob", "--dry-run", "--", "aider"},
		Want: []string{"HOST DRY-RUN session=friend-bob dir=./bob dry_run=true command=\"tmux new-session -d -s friend-bob -c ./bob -- aider\""},
	}
	doc, err := os.ReadFile("../../docs/CLI.md")
	require.NoError(t, err)
	assert.Contains(t, string(doc), strings.TrimPrefix(step.Line, "$ "), "the executed command is also in the reference")
	var out, errb strings.Builder
	w := newRig(t).world()
	code := run(step.Args, strings.NewReader(""), &out, &errb, w)
	got := onboarding.Result{Code: code, Stdout: out.String(), Stderr: errb.String()}
	require.Equal(t, 0, code, errb.String())
	for _, p := range onboarding.CompareTranscript([]onboarding.Step{step}, []onboarding.Result{got}, nil) {
		assert.Fail(t, "the help example differs", p.Message)
	}
}

func TestRunPublishesBeatHostToRedisStore(t *testing.T) {
	t.Parallel()
	r := newRig(t, "ada", "bob")
	w := r.world()
	var cancel context.CancelFunc
	w.signals = func(ctx context.Context) (context.Context, context.CancelFunc) {
		ctx, cancel = context.WithCancel(ctx)
		return ctx, cancel
	}
	w.beat = func(context.Context, string, string, time.Time, friend.BeatWords) (string, error) {
		cancel()
		return "FRIEND-BEAT OK bob at=2026-10-04T03:00:00Z row_mode=batch row_width=2", nil
	}
	dir := t.TempDir()
	var out, errb strings.Builder
	code := run([]string{"run", "--as", "bob", "--harness", "opencode", "--dir", dir, "--host", "box", "--width", "4"}, strings.NewReader(""), &out, &errb, w)
	assert.Equal(t, 0, code, errb.String())

	marks, err := r.store.Marks(context.Background(), "friend:bob:beat")
	require.NoError(t, err)
	require.Len(t, marks, 1)
	assert.Equal(t, "box", marks[0]["host"])
	assert.NotEmpty(t, marks[0]["at"])
}

func TestRunPublishesDefaultSelfHostToRedisStore(t *testing.T) {
	t.Parallel()
	r := newRig(t, "ada", "bob")
	w := r.world()
	w.hostname = func() (string, error) { return "m1", nil }
	var cancel context.CancelFunc
	w.signals = func(ctx context.Context) (context.Context, context.CancelFunc) {
		ctx, cancel = context.WithCancel(ctx)
		return ctx, cancel
	}
	w.beat = func(context.Context, string, string, time.Time, friend.BeatWords) (string, error) {
		cancel()
		return "FRIEND-BEAT OK bob at=2026-10-04T03:00:00Z row_mode=batch row_width=2", nil
	}
	dir := t.TempDir()
	var out, errb strings.Builder
	code := run([]string{"run", "--as", "bob", "--harness", "opencode", "--dir", dir, "--width", "4"}, strings.NewReader(""), &out, &errb, w)
	assert.Equal(t, 0, code, errb.String())

	marks, err := r.store.Marks(context.Background(), "friend:bob:beat")
	require.NoError(t, err)
	require.Len(t, marks, 1)
	assert.Equal(t, "m1", marks[0]["host"])
	assert.NotEmpty(t, marks[0]["at"])
}
