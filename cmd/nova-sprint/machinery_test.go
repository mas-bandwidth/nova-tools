package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/redisauth"
	"github.com/mas-bandwidth/nova-tools/internal/seatcheck"
	"github.com/mas-bandwidth/nova-tools/internal/sprintwire"
	"github.com/mas-bandwidth/nova-tools/internal/workgh"
)

// upOutside is the check's reaches past the store with everything up: a server
// that answers, a dashboard at 200 with a build, no bus, a host that is not
// the friends' host.
func upOutside() outside {
	return outside{
		serverAddr:    func() (string, bool) { return "127.0.0.1:6390", false },
		roundTrip:     func(context.Context, string) (time.Duration, error) { return 12 * time.Millisecond, nil },
		listenerPID:   func(string) int { return 4242 },
		dbsize:        func(context.Context, string) int64 { return -1 },
		httpGet:       func(context.Context, string) (int, []byte, error) { return 200, []byte(`{"build":"b1"}`), nil },
		ping:          func(context.Context, string) error { return nil },
		hostname:      func() string { return "bench-a" },
		uid:           func() int { return 501 },
		launchdLoaded: func(context.Context, int, string) (bool, bool) { return false, true },
	}
}

// machinery on a sprint whose loop ticked just now: every thing up, exit 0, the
// summary counts nine lines in the order server, store, loop, fleet, friends,
// readers, dashboard, bus, inbox.
func TestMachineryEverythingUp(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a --members m1,m2 --owner glenn")
	ta.ok("start")
	ta.ok("tick")
	out := ta.ok("machinery")
	var things []string
	for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
		things = append(things, strings.Fields(l)[1])
	}
	assert.Equal(t, []string{"server", "store", "loop", "fleet", "friends", "readers", "dashboard", "bus", "inbox", "OK"}, things, out)
	assert.Contains(t, out, "MACHINERY server OK addr=127.0.0.1:6390 ms=12 pid=4242\n", out)
	assert.Contains(t, out, "MACHINERY store OK redis=mem:0 dbsize=- machine=running epoch=", out)
	assert.Contains(t, out, "MACHINERY loop OK tick_age=0s ticks=1\n", out)
	assert.Contains(t, out, "MACHINERY fleet OK up=2 held=0 down=0\n", out)
	assert.Contains(t, out, "MACHINERY readers OK total=1 up=1 reading=0\n", out)
	assert.Contains(t, out, "MACHINERY dashboard OK addr=127.0.0.1:7390 status=200 build=b1\n", out)
	assert.Contains(t, out, `MACHINERY bus OK redis=none note="not configured: NOVA_BUS_REDIS is not set"`, out)
	assert.Contains(t, out, "MACHINERY OK n=9\n", out)

	var r seatcheck.Report
	ta.json("machinery", &r)
	assert.Equal(t, 0, r.Down)
	assert.Len(t, r.Lines, 9)
}

// The night of 2026-10-03: the friends' beat agents never came back after a
// reboot. A friend down is a DOWN line naming her label and the bootstrap
// line; the fleet member down names its loop record; the loop silent names
// the kickstart; machinery exits 1 and says how many are DOWN.
func TestMachineryDownLines(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a --members m1,m2 --owner glenn")
	ta.ok("start")
	ta.ok("tick")
	ta.a.friends = friendRows("friend-a", "friend-b")
	ta.ok("friend sync --root " + t.TempDir())
	o := upOutside()
	o.hostname = func() string { return "host-a" }
	o.launchdLoaded = func(_ context.Context, _ int, label string) (bool, bool) {
		return label == seatcheck.Label("friend-b"), true
	}
	o.roundTrip = func(context.Context, string) (time.Duration, error) { return 0, context.DeadlineExceeded }
	ta.a.outside = o
	ta.ok("friend beat friend-b")
	ta.mu.Lock()
	ta.live = []string{"m1"} // m2 stops beating
	ta.mu.Unlock()
	ta.a.sleep(2 * time.Minute)
	code, out, _ := ta.do("machinery")
	assert.Equal(t, 1, code, out)
	assert.Contains(t, out, `MACHINERY server DOWN addr=127.0.0.1:6390 why="context deadline exceeded" remedy="launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/com.nova.loop.sprint-server-host-a.plist"`+"\n", out)
	assert.Contains(t, out, `MACHINERY loop DOWN tick_age=2m0s ticks=1 why="silent past 15s" remedy="launchctl kickstart -k gui/$(id -u)/com.nova.loop.sprint-server-host-a"`+"\n", out)
	assert.Contains(t, out, `MACHINERY fleet DOWN up=1 held=0 down=m2:2m0s remedy="nova-config loop show member-m2"`+"\n", out)
	assert.Contains(t, out, `MACHINERY friends DOWN friend=friend-a beat_age=never label=com.nova.loop.friend-beat-friend-a agent="not loaded" remedy="launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/com.nova.loop.friend-beat-friend-a.plist"`+"\n", out)
	assert.Contains(t, out, `MACHINERY friends DOWN friend=friend-b beat_age=2m0s label=com.nova.loop.friend-beat-friend-b agent="loaded" remedy="launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/com.nova.loop.friend-beat-friend-b.plist"`+"\n", out)
	assert.Contains(t, out, `MACHINERY friends DOWN up=0 held=0 down=2 remedy="nova-sprint where"`+"\n", out)
	assert.Contains(t, out, "MACHINERY DOWN n=6 of=11\n", out)
}

// handover starts with the check and coordinator prints it before the move;
// both keep their own word and exit (the seat moved: OK, 0) whatever the check
// says, and its DOWN lines carry the remedies.
func TestSeatVerbsStartWithTheCheck(t *testing.T) {
	t.Parallel()
	ta := seatSprint(t)
	out := ta.ok("handover")
	assert.True(t, strings.HasPrefix(out, "MACHINERY server OK "), out)
	assert.Contains(t, out, "MACHINERY OK n=9\nHANDOVER seat=coordinator since=init\n", out)

	o := upOutside()
	o.httpGet = func(context.Context, string) (int, []byte, error) { return 503, nil, nil }
	ta.a.outside = o
	out = ta.ok("coordinator rowan --reason 'rowan is back'")
	assert.True(t, strings.HasPrefix(out, "MACHINERY server OK "), out)
	assert.Contains(t, out, `MACHINERY dashboard DOWN addr=127.0.0.1:7390 status=503 remedy="nova-sprint dashboard --listen 127.0.0.1:7390"`+"\n", out)
	assert.Contains(t, out, "MACHINERY DOWN n=1 of=9\nCOORDINATOR OK holder=rowan from=coordinator by=coordinator given\nHANDOVER seat=rowan", out)
	assert.Equal(t, 1, strings.Count(out, "MACHINERY dashboard DOWN"), "the check prints once:\n%s", out)
	assert.Equal(t, "rowan", ta.holder())

	out = ta.ok("handover --json")
	var h struct {
		Check seatcheck.Report `json:"check"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &h))
	assert.Equal(t, 1, h.Check.Down)
}

// The check against the twin store with the dashboard's handler behind the
// app's HTTP transport and a sprintwire round trip to a fake server: the
// transport's own readers of a body and an answer, not the fakes', and no
// socket (the GET is handed to the handler in this process).
func TestMachineryOverHTTP(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a --members m1 --owner glenn")
	ta.ok("start")
	ta.ok("tick")
	ta.a.transport = handlerTransport{http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != "dash.test:7390" || r.URL.Path != "/api/sprint" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"build":"3f2a","landed":0}`))
	})}
	env := map[string]string{"NOVA_SPRINT_REDIS": "mem:0", "NOVA_SPRINT_ACTOR": "coordinator", DashboardEnv: "dash.test:7390", ServerEnv: "127.0.0.1:1"}
	ta.a.getenv = func(k string) string { return env[k] }
	ta.a.forward = func(_ context.Context, addr string, verbs ...[]string) ([]sprintwire.Result, error) {
		require.Equal(t, "127.0.0.1:1", addr)
		require.Equal(t, []string{"routes", "--actor", "coordinator"}, verbs[0])
		return []sprintwire.Result{{Code: 0, Stdout: "ROUTES OK\n"}}, nil
	}
	real := ta.a.realOutside()
	o := upOutside()
	o.serverAddr, o.roundTrip, o.httpGet = real.serverAddr, real.roundTrip, real.httpGet
	o.listenerPID = func(string) int { return 0 }
	ta.a.outside = o
	out := ta.ok("machinery")
	assert.Contains(t, out, "MACHINERY server OK addr=127.0.0.1:1 ms=0\n", out)
	assert.Contains(t, out, "MACHINERY dashboard OK addr=dash.test:7390 status=200 build=3f2a\n", out)
	assert.Contains(t, out, "MACHINERY OK n=9\n", out)
}

// The bus is dialed with the one fleet Redis seat every tool dials with
// (internal/nsprint/redisauth, through openConn), never a variable of its
// own: with the ACL user named and its password variable empty the ping is
// refused before any dial, naming the variable to export.
func TestMachineryBusPingUsesTheFleetSeat(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	env := map[string]string{redisauth.UserEnv: "bench"}
	ta.a.getenv = func(k string) string { return env[k] }
	err := ta.a.realOutside().ping(context.Background(), "/nowhere/bus.sock")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "as user bench (password from "+redisauth.DefaultPasswordEnv+")", err.Error())
	assert.Contains(t, err.Error(), redisauth.DefaultPasswordEnv+" is empty", err.Error())
}

// A served check (coordinator or handover answered by the server) runs inside
// the server's single-threaded step, so it reaches nothing outside the store:
// no GET on the dashboard, no dial of the bus, no launchctl per friend down.
// The lines say those were not measured, and are not DOWN; nova-sprint
// machinery, run where it is typed, measures them.
func TestServedCheckRunsNoOutsideProbe(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a --members m1 --owner glenn")
	ta.ok("start")
	ta.ok("tick")
	ta.a.friends = friendRows("friend-a")
	ta.ok("friend sync --root " + t.TempDir())
	ta.a.sleep(2 * time.Minute)
	env := ta.a.getenv
	ta.a.getenv = func(k string) string {
		if k == BusEnv {
			return "127.0.0.1:6381"
		}
		return env(k)
	}
	var gets, pings, launchds int
	o := upOutside()
	o.serverAddr = ta.a.realOutside().serverAddr
	o.httpGet = func(context.Context, string) (int, []byte, error) { gets++; return 200, []byte(`{"build":"b1"}`), nil }
	o.ping = func(context.Context, string) error { pings++; return nil }
	o.launchdLoaded = func(context.Context, int, string) (bool, bool) { launchds++; return true, true }
	ta.a.outside = o
	ta.a.serveAddr = "mem:0"
	ta.ok("tick")
	out := ta.ok("handover")
	assert.Equal(t, [3]int{0, 0, 0}, [3]int{gets, pings, launchds}, "GETs, pings, launchctls:\n%s", out)
	note := `"not measured: the check ran in the server; run nova-sprint machinery"`
	assert.Contains(t, out, "MACHINERY server OK addr=mem:0 self=true pid=", out)
	assert.Contains(t, out, `MACHINERY friends DOWN friend=friend-a beat_age=never label=com.nova.loop.friend-beat-friend-a agent=`+note+" remedy=", out)
	assert.Contains(t, out, "MACHINERY dashboard OK addr=127.0.0.1:7390 note="+note+"\n", out)
	assert.Contains(t, out, "MACHINERY bus OK redis=127.0.0.1:6381 note="+note+"\n", out)
	assert.Contains(t, out, "MACHINERY DOWN n=2 of=10\n", out)
}

// The help says what handover does now: the seat check first, then the brief;
// the words section, the verbs' prose and handover's effect line agree.
func TestHelpSaysHandoverStartsWithTheCheck(t *testing.T) {
	t.Parallel()
	const says = "handover runs the seat check, then prints what the next seat needs"
	unwrap := func(s string) string { return strings.Join(strings.Fields(s), " ") } // the help wraps its lines
	assert.Contains(t, unwrap(wordsSection()), says)
	assert.Equal(t, 2, strings.Count(unwrap(banner()), says), "the words section and the verbs' prose:\n%s", banner())
	assert.Contains(t, verbEffect["handover"], "the seat check")
}

// coordinator --json carries the check once, under handover (the brief
// starts with it), never a second time at the top.
func TestCoordinatorJSONCarriesTheCheckOnce(t *testing.T) {
	t.Parallel()
	ta := seatSprint(t)
	out := ta.ok("coordinator rowan --reason 'rowan is back' --json")
	assert.Equal(t, 1, strings.Count(out, `"check":`), out)
	var v struct {
		Holder   string          `json:"holder"`
		Handover json.RawMessage `json:"handover"`
		Check    json.RawMessage `json:"check"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &v), out)
	assert.Equal(t, "rowan", v.Holder)
	assert.Nil(t, v.Check, "no top-level check")
	var h struct {
		Check seatcheck.Report `json:"check"`
	}
	require.NoError(t, json.Unmarshal(v.Handover, &h), string(v.Handover))
	assert.Len(t, h.Check.Lines, 9)
}

// The merge queue's and the installed versions' fakes (rows 10 and 11 of the
// check): what GitHub and each machine answer, and what was asked.
type fakeQueue struct {
	read  queueRead
	err   error
	calls int
	since time.Time
}

func (f *fakeQueue) MergeQueue(_ context.Context, repo, branch string, since time.Time) (queueRead, error) {
	f.calls++
	f.since = since
	if repo != "mas-bandwidth/nova-tools" || branch != "dev" {
		return queueRead{}, fmt.Errorf("asked %s:%s", repo, branch)
	}
	return f.read, f.err
}

type fakeVersions struct {
	dev, sha  string
	tipErr    error
	installed map[string]string
	mu        sync.Mutex
	tips      int
	asked     map[string]bool // machine -> here
}

func (f *fakeVersions) Tip(_ context.Context, repo, branch string) (string, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tips++
	return f.dev, f.sha, f.tipErr
}

func (f *fakeVersions) Installed(_ context.Context, machine string, here bool) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.asked == nil {
		f.asked = map[string]bool{}
	}
	f.asked[machine] = here
	v, ok := f.installed[machine]
	if !ok {
		return "", errors.New("ssh: connect to host " + machine + " port 22: Operation timed out")
	}
	return v, nil
}

type fakeMark struct {
	last time.Time
	seen bool
	set  []time.Time
}

func (f *fakeMark) Last(string, string) (time.Time, bool) { return f.last, f.seen }
func (f *fakeMark) Set(_, _ string, at time.Time) error {
	f.set = append(f.set, at)
	return nil
}

const (
	testDevSHA = "2d720d219ff5c0ffee0123456789abcdef012345"
	testDev    = "20261004190000-2d720d219ff5"
)

// queueApp is a ticked sprint of members m1 and m2 checked from m1, with
// NOVA_SPRINT_MERGE_QUEUE naming spec, and fakes for the queue, the
// versions and the mark.
func queueApp(t *testing.T, spec string) (*testApp, *fakeQueue, *fakeVersions, *fakeMark) {
	t.Helper()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a --members m1,m2 --owner glenn")
	ta.ok("start")
	ta.ok("tick")
	env := ta.a.getenv
	ta.a.getenv = func(k string) string {
		if k == QueueEnv {
			return spec
		}
		return env(k)
	}
	q := &fakeQueue{read: queueRead{Queued: []int{5281}, Green: []int{}}}
	v := &fakeVersions{dev: testDev, sha: testDevSHA, installed: map[string]string{"m1": testDev, "m2": "v1.2.0-dev.2d720d21"}}
	mk := &fakeMark{}
	o := upOutside()
	o.hostname = func() string { return "m1" }
	o.queue, o.versions, o.mark = q, v, mk
	ta.a.outside = o
	return ta, q, v, mk
}

// machinery with NOVA_SPRINT_MERGE_QUEUE set reads the queue and each fleet
// machine's version through the seams: a removal after the previous check is
// a DOWN line naming the pull request, one before it is not news; a machine
// whose installed version is not dev's tip is a DOWN line naming it, its
// version and dev's; the machine the check runs on is asked here, the others
// over the wire; the mark moves to now once the queue was read.
func TestMachineryReadsTheMergeQueueAndEachMachinesVersion(t *testing.T) {
	t.Parallel()
	ta, q, v, mk := queueApp(t, "mas-bandwidth/nova-tools:dev")
	now := ta.a.now()
	mk.last, mk.seen = now.Add(-time.Hour), true
	q.read.Removed = []queueRemoval{
		{PR: 5200, At: now.Add(-2 * time.Hour), Reason: "FAILED_CHECKS"},
		{PR: 5299, At: now.Add(-12 * time.Minute), Reason: "FAILED_CHECKS"},
	}
	v.installed["m2"] = "20261003120000-aaaaaaaaaaaa"
	code, out, _ := ta.do("machinery")
	assert.Equal(t, 1, code, out)
	assert.True(t, strings.HasSuffix(out, strings.Join([]string{
		"MACHINERY inbox OK open=0",
		`MACHINERY queue DOWN pr=5299 thrown_age=12m0s reason="FAILED_CHECKS" remedy="gh pr checks 5299 --repo mas-bandwidth/nova-tools"`,
		"MACHINERY queue OK repo=mas-bandwidth/nova-tools branch=dev entries=1 queued=5281 thrown=1",
		`MACHINERY versions DOWN machine=m2 installed=20261003120000-aaaaaaaaaaaa dev=` + testDev + ` remedy="nova-update release cycle -h"`,
		"MACHINERY versions OK dev=" + testDev + " machines=2 fresh=1 stale=1 unread=0",
		"MACHINERY DOWN n=2 of=13",
	}, "\n")+"\n"), out)
	assert.NotContains(t, out, "pr=5200", "a removal before the previous check is not news")
	assert.Equal(t, now.Add(-time.Hour), q.since)
	assert.Equal(t, []time.Time{now}, mk.set)
	assert.Equal(t, map[string]bool{"m1": true, "m2": false}, v.asked)

	// everything fresh and queued: the two rows are OK
	q.read.Removed = nil
	v.installed["m2"] = testDev
	out = ta.ok("machinery")
	assert.Contains(t, out, "MACHINERY queue OK repo=mas-bandwidth/nova-tools branch=dev entries=1 queued=5281 thrown=0\n"+
		"MACHINERY versions OK dev="+testDev+" machines=2 fresh=2 stale=0 unread=0\nMACHINERY OK n=11\n", out)
}

// The first check (no mark) counts removals from the last QueueFirstWindow;
// an empty queue while green pull requests wait is DOWN; a queue that could
// not be read is DOWN with GitHub's word and leaves the mark where it was; a
// machine that does not answer is DOWN with the error and the command.
func TestMachineryQueueFirstCheckEmptyAndUnread(t *testing.T) {
	t.Parallel()
	ta, q, v, mk := queueApp(t, "mas-bandwidth/nova-tools:dev")
	now := ta.a.now()
	q.read = queueRead{Queued: []int{}, Green: []int{5305, 5309}}
	delete(v.installed, "m2")
	code, out, _ := ta.do("machinery")
	assert.Equal(t, 1, code, out)
	assert.Equal(t, now.Add(-QueueFirstWindow), q.since)
	assert.Contains(t, out, `MACHINERY queue DOWN repo=mas-bandwidth/nova-tools branch=dev entries=0 green=5305,5309 thrown=0 why="the queue is empty while green pull requests wait" remedy="gh pr list --repo mas-bandwidth/nova-tools --base dev --search 'status:success -is:draft'"`+"\n", out)
	assert.Contains(t, out, `MACHINERY versions DOWN machine=m2 why="ssh: connect to host m2 port 22: Operation timed out" remedy="ssh m2 ~/.local/bin/nova-update version"`+"\n", out)
	assert.Contains(t, out, "MACHINERY DOWN n=2 of=12\n", out)

	q.err = errors.New("gh: HTTP 401: Bad credentials")
	v.tipErr = errors.New("gh: HTTP 401: Bad credentials")
	mk.set = nil
	code, out, _ = ta.do("machinery")
	assert.Equal(t, 1, code, out)
	assert.Contains(t, out, `MACHINERY queue DOWN repo=mas-bandwidth/nova-tools branch=dev why="gh: HTTP 401: Bad credentials" remedy="gh auth status"`+"\n", out)
	assert.Contains(t, out, `MACHINERY versions DOWN dev=unread why="gh: HTTP 401: Bad credentials" remedy="gh auth status"`+"\n", out)
	assert.Empty(t, mk.set, "an unread queue moves no mark")
}

// A NOVA_SPRINT_MERGE_QUEUE that names no <owner>/<repo>:<branch> is DOWN on
// both rows, saying what it wants, and asks GitHub nothing.
func TestMachineryQueueSpecRefused(t *testing.T) {
	t.Parallel()
	for _, spec := range []string{"nova-tools:dev", "mas-bandwidth/nova-tools", "mas-bandwidth/nova-tools:", "/x:dev"} {
		ta, q, v, _ := queueApp(t, spec)
		code, out, _ := ta.do("machinery")
		assert.Equal(t, 1, code, out)
		want := QueueEnv + " wants <owner>/<repo>:<branch>, got " + spec
		assert.Contains(t, out, `MACHINERY queue DOWN repo=`+spec+` branch= why="`+want+`" remedy="gh auth status"`+"\n", out)
		assert.Contains(t, out, `MACHINERY versions DOWN dev=unread why="`+want+`"`, out)
		assert.Equal(t, 0, q.calls, spec)
		assert.Equal(t, 0, v.tips, spec)
	}
}

// A served check reads neither the queue nor a machine: the rows say not
// measured, are not DOWN, and the mark stays.
func TestServedCheckReadsNoQueueAndNoMachine(t *testing.T) {
	t.Parallel()
	ta, q, v, mk := queueApp(t, "mas-bandwidth/nova-tools:dev")
	o := ta.a.outside
	o.serverAddr = ta.a.realOutside().serverAddr
	ta.a.outside = o
	ta.a.serveAddr = "mem:0"
	out := ta.ok("handover")
	note := `"not measured: the check ran in the server; run nova-sprint machinery"`
	assert.Contains(t, out, "MACHINERY queue OK repo=mas-bandwidth/nova-tools branch=dev note="+note+"\nMACHINERY versions OK note="+note+"\n", out)
	assert.Equal(t, [3]int{0, 0, 0}, [3]int{q.calls, v.tips, len(v.asked)}, out)
	assert.Empty(t, mk.set)
}

// ghQueue over canned GitHub answers (no gh is run): the document is a query
// with the branch's searches, the queue's entries are read, a green pull
// request already queued is not counted green, removals come back oldest
// first; GitHub's errors and a branch with no queue are errors.
func TestGhQueueReadsGitHubsAnswer(t *testing.T) {
	t.Parallel()
	since := time.Date(2026, 10, 4, 18, 0, 0, 0, time.UTC)
	var vars map[string]any
	answer := `{"data":{"repository":{"mergeQueue":{"entries":{"nodes":[{"pullRequest":{"number":5281}},{"pullRequest":{"number":5299}}]}}},
		"green":{"nodes":[{"number":5309,"isInMergeQueue":false},{"number":5281,"isInMergeQueue":true},{"number":5305,"isInMergeQueue":false},{}]},
		"touched":{"nodes":[{"number":5300,"timelineItems":{"nodes":[{"createdAt":"2026-10-04T18:40:00Z","reason":"FAILED_CHECKS"}]}},
			{"number":5290,"timelineItems":{"nodes":[{"createdAt":"2026-10-04T18:20:00Z","reason":"MANUAL"}]}},{"number":5281,"timelineItems":{"nodes":[]}}]}}}`
	g := ghQueue{query: func(_ context.Context, doc string, v map[string]any) ([]byte, error) {
		require.NoError(t, workgh.RefuseMutation(doc))
		vars = v
		return []byte(answer), nil
	}}
	r, err := g.MergeQueue(context.Background(), "mas-bandwidth/nova-tools", "dev", since)
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"owner": "mas-bandwidth", "name": "nova-tools", "branch": "dev",
		"green":   "repo:mas-bandwidth/nova-tools is:pr base:dev is:open draft:false status:success",
		"touched": "repo:mas-bandwidth/nova-tools is:pr base:dev updated:>=2026-10-04T18:00:00Z"}, vars)
	assert.Equal(t, []int{5281, 5299}, r.Queued)
	assert.Equal(t, []int{5305, 5309}, r.Green)
	assert.Equal(t, []queueRemoval{
		{PR: 5290, At: time.Date(2026, 10, 4, 18, 20, 0, 0, time.UTC), Reason: "MANUAL"},
		{PR: 5300, At: time.Date(2026, 10, 4, 18, 40, 0, 0, time.UTC), Reason: "FAILED_CHECKS"},
	}, r.Removed)

	for body, says := range map[string]string{
		`{"errors":[{"message":"Could not resolve to a Repository"}]}`: "GitHub: Could not resolve to a Repository",
		`{"data":{"repository":{"mergeQueue":null}}}`:                  "mas-bandwidth/nova-tools has no merge queue on dev",
		`not json`: "the merge queue's answer is not GitHub's",
	} {
		answer = body
		_, err := g.MergeQueue(context.Background(), "mas-bandwidth/nova-tools", "dev", since)
		require.Error(t, err, body)
		assert.Contains(t, err.Error(), says)
	}
}

// fleetVersions over canned answers (no gh, no ssh): the tip's identity is the
// one a vcs build of it states, <utc commit time>-<12 hex>; a machine is asked
// with nova-update's version verb, here or over ssh non-interactively, and its
// answer is field two of the version line.
func TestFleetVersionsReadsTheTipAndEachMachine(t *testing.T) {
	t.Parallel()
	var argvs [][]string
	answers := map[string]string{
		"nova-update": "nova-update " + testDev + " darwin/arm64 go1.26.6\n",
		"ssh":         "nova-update v1.2.0-dev.2d720d21 linux/amd64 go1.26.6\n",
	}
	f := fleetVersions{
		query: func(_ context.Context, doc string, v map[string]any) ([]byte, error) {
			require.NoError(t, workgh.RefuseMutation(doc))
			assert.Equal(t, map[string]any{"owner": "mas-bandwidth", "name": "nova-tools", "ref": "refs/heads/dev"}, v)
			return []byte(`{"data":{"repository":{"ref":{"target":{"oid":"` + testDevSHA + `","committedDate":"2026-10-04T15:00:00-04:00"}}}}}`), nil
		},
		run: func(_ context.Context, argv []string) (string, error) {
			argvs = append(argvs, argv)
			if a, ok := answers[argv[0]]; ok {
				return a, nil
			}
			return "", errors.New("no such program")
		},
	}
	dev, sha, err := f.Tip(context.Background(), "mas-bandwidth/nova-tools", "dev")
	require.NoError(t, err)
	assert.Equal(t, testDev, dev)
	assert.Equal(t, testDevSHA, sha)

	v, err := f.Installed(context.Background(), "m1", true)
	require.NoError(t, err)
	assert.Equal(t, testDev, v)
	v, err = f.Installed(context.Background(), "m2", false)
	require.NoError(t, err)
	assert.Equal(t, "v1.2.0-dev.2d720d21", v)
	assert.True(t, seatcheck.Fresh(v, dev, sha))
	assert.Equal(t, [][]string{{"nova-update", "version"}, {"ssh", "-o", "BatchMode=yes", "-o", "ConnectTimeout=5", "m2", "~/.local/bin/nova-update version"}}, argvs)

	answers["ssh"] = "bash: nova-update: command not found\n"
	_, err = f.Installed(context.Background(), "m3", false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `nova-update version answered "bash: nova-update: command not found", not a version line`)

	f.query = func(context.Context, string, map[string]any) ([]byte, error) {
		return []byte(`{"data":{"repository":{"ref":null}}}`), nil
	}
	_, _, err = f.Tip(context.Background(), "mas-bandwidth/nova-tools", "nope")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "mas-bandwidth/nova-tools has no branch nope")
}

// The mark is one file per repository and branch: no file is no previous
// check, and a time set is the time read back.
func TestCacheMarkRoundTrip(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "seat-check")
	c := cacheMark{dir: func() (string, error) { return dir, nil }}
	_, seen := c.Last("mas-bandwidth/nova-tools", "dev")
	assert.False(t, seen)
	at := time.Date(2026, 10, 4, 19, 30, 0, 123, time.UTC)
	require.NoError(t, c.Set("mas-bandwidth/nova-tools", "dev", at))
	got, seen := c.Last("mas-bandwidth/nova-tools", "dev")
	assert.True(t, seen)
	assert.True(t, at.Equal(got), "%v %v", at, got)
	_, seen = c.Last("mas-bandwidth/nova-tools", "main")
	assert.False(t, seen, "another branch's mark is its own")
	assert.FileExists(t, filepath.Join(dir, "queue-mas-bandwidth-nova-tools-dev"))
}
