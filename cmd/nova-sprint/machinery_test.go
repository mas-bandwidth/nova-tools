package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/redisauth"
	"github.com/mas-bandwidth/nova-tools/internal/seatcheck"
	"github.com/mas-bandwidth/nova-tools/internal/sprintwire"
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
