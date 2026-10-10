package main

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprintdash"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The dashboard reads the sprint in this process exactly as where --json --cards prints it, and
// serves that object as /api/sprint's data.
func TestDashboardReadsTheSprintAsWhereJSONDoes(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a --members m1,m2")
	ta.ok("add --stream s1 --count 3")
	want := ta.ok("where --json --cards")
	got, err := ta.a.whereJSON("", false)
	require.NoError(t, err)
	assert.JSONEq(t, ta.ok("where --json --cards --rows"), string(got), "the rows place the critical cards (sprintdash.placed)")

	srv := &sprintdash.Server{Read: func() ([]byte, error) { return ta.a.whereJSON("", false) }, Now: ta.a.now, Every: time.Second}
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/sprint", nil))
	var v struct {
		OK   bool            `json:"ok"`
		Data json.RawMessage `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &v))
	assert.True(t, v.OK)
	assert.JSONEq(t, want, string(v.Data))
}

// A read where refuses is the dashboard's failed read: the page is shown the exit alone,
// and where's own line goes to the log, never to the page.
func TestDashboardReadFailureIsWheresRefusal(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	_, err := ta.a.whereJSON("", false) // no init: no sprint here yet
	require.Error(t, err)
	assert.Contains(t, err.Error(), "where exited 1: nova-sprint where: this store: no sprint here yet")

	var log strings.Builder
	srv := ta.a.dashboardServer("", false, "", time.Second, "", &log)
	srv.Tick()
	var v struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	require.NoError(t, json.Unmarshal(srv.Snapshot(), &v))
	assert.False(t, v.OK)
	assert.Equal(t, "where exited 1", v.Error)
	assert.NotContains(t, string(srv.Snapshot()), "no sprint here yet")
	assert.Contains(t, log.String(), "read failed: where exited 1: nova-sprint where: this store: no sprint here yet")
}

// The page listens on loopback and the fleet's private network only, each address once.
func TestDashboardListensOnPrivateAddressesOnly(t *testing.T) {
	t.Parallel()
	for list, want := range map[string][]string{
		"127.0.0.1:7390":                {"127.0.0.1:7390"},
		"localhost:7390,localhost:7390": {"localhost:7390"},
		"[::1]:7390,,127.0.0.1:7390,":   {"[::1]:7390", "127.0.0.1:7390"},
	} {
		got, err := dashboardAddrs("--listen", list)
		require.NoError(t, err, list)
		assert.Equal(t, want, got, list)
	}
	for list, why := range map[string]string{
		"bench-a:7390": "never a name",
		"127.0.0.1":    "wants address:port",
		"":             "names no address",
	} {
		_, err := dashboardAddrs("--listen", list)
		if assert.Error(t, err, list) {
			assert.Contains(t, err.Error(), why, list)
		}
	}
	// the ranges, as addresses: none of them is dialled
	for _, ip := range []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback, net.IPv4(100, 64, 1, 2), net.IPv4(100, 127, 255, 9),
		net.IPv4(10, 0, 0, 2), net.IPv4(192, 168, 1, 2), net.IPv4(172, 16, 0, 2), {0xfd, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 2}} {
		assert.Empty(t, listenable(ip), "%s", ip)
	}
	for _, tc := range []struct {
		ip  net.IP
		why string
	}{
		{net.IPv4zero, "every network"},
		{net.IPv6unspecified, "every network"},
		{net.IPv4(100, 128, 0, 1), "a public address"},
		{net.IPv4(8, 8, 8, 8), "a public address"},
		{net.IPv4(203, 0, 113, 7), "a public address"},
		{net.IPv4(169, 254, 1, 1), "a link-local address"},
		{net.IP{0xfe, 0x80, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1}, "a link-local address"},
	} {
		assert.Contains(t, listenable(tc.ip), tc.why, "%s", tc.ip)
	}
}

// Every refusal comes before any listener: exit 2, one line, nothing served.
func TestDashboardRefusesBadUse(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	for line, why := range map[string]string{
		"dashboard now":                       "takes no words",
		"dashboard --every 0s":                "--every wants a duration above 0",
		"dashboard --listen 0.0.0.0:7390":     "does not listen on every network",
		"dashboard --listen 1.1.1.1:7390":     "a public address",
		"dashboard --pull 0.0.0.0:7395":       "--pull 0.0.0.0:7395: the page shows the sprint",
		"dashboard --pull 127.0.0.1":          "--pull wants address:port (or none)",
		"dashboard --listen none --pull none": "serve nothing",
		"dashboard --logo " + t.TempDir():     "is not a file",
		"dashboard --logo /no/such/logo.webp": "is not a file",
	} {
		code, out, errs := ta.do(line)
		assert.Equal(t, 2, code, "%s: %s%s", line, out, errs)
		assert.Contains(t, errs, why, line)
		assert.Contains(t, errs, "nova-sprint dashboard REFUSED", line)
		assert.Equal(t, 1, strings.Count(errs, "\n"), line)
		assert.Empty(t, out, line)
	}
}

// The sprint's server never runs the dashboard: it is run where it is typed and reads
// through the server.
func TestDashboardIsNotServed(t *testing.T) {
	t.Parallel()
	assert.Contains(t, readVerb([]string{"dashboard"}).unserved(), "dashboard is not run by the server")
	assert.Equal(t, classRead, verbClasses["dashboard"])
}

// where --json --cards carries the cards dealt and not finished, each with its row, state,
// since, deadline and branch, and nothing of the brief; the pull routes serve a friend's
// from it. Plain where --json is as it was, and --cards wants --json.
func TestWhereCardsIsWhatThePullRoutesRead(t *testing.T) {
	t.Parallel()
	ta, _ := friendCardApp(t, "friend amy", "amy")
	ta.ok("tick")
	ta.startFriend("amy", 1)
	assert.NotContains(t, ta.ok("where --json"), `"cards"`)
	code, _, errs := ta.do("where --cards")
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "--cards is a field of the JSON view: give --json with it")

	out := ta.ok("where --json --cards")
	assert.NotContains(t, out, "REPO:", "no brief in the view")
	var v whereView
	require.NoError(t, json.Unmarshal([]byte(out), &v))
	require.Len(t, v.Cards, 1, out)
	c := v.Cards[0]
	assert.Equal(t, dealtCard{ID: "s1-1.w1", Primary: "s1-1", Stream: "s1", Member: "friend.amy", State: "working",
		Since: c.Since, Deadline: c.Since.Add(2 * time.Hour), Branch: "sprint/s1-1.w1.g1.e0", Priority: "normal"}, c)
	assert.False(t, c.Since.IsZero())

	srv := &sprintdash.Server{Read: func() ([]byte, error) { return ta.a.whereJSON("", false) }, Now: ta.a.now, Every: time.Second}
	w := httptest.NewRecorder()
	srv.Pull().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/friend/amy", nil))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	lines := strings.Split(strings.TrimSpace(w.Body.String()), "\n")
	require.Len(t, lines, 3, w.Body.String())
	assert.True(t, strings.HasPrefix(lines[1], "friend amy up "), lines[1])
	assert.Equal(t, "s1-1.w1 s1 working 0s due 2h0m sprint/s1-1.w1.g1.e0", lines[2])
}

// The dashboard over a store of two releases (stream set --release) shows the current one's
// streams by default, and another's or every stream when the page asks.
func TestTheDashboardOverAStoreOfTwoReleasesShowsOneRelease(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a --members m1:16 --coordinator lead")
	ta.ok("add --stream sprint-v1-release --count 3 --actor lead")
	ta.ok("add --stream promote-red-2026-10-05 --count 2 --actor lead")
	ta.ok("add --stream jev --count 5 --actor lead")
	ta.ok("stream set sprint-v1-release promote-red-2026-10-05 --release v1.0.0 --actor lead")
	ta.ok("stream set jev --release v1.1.0 --actor lead")
	// a critical card of each release, waiting and dealt to no one: where's critical names
	// no stream, so the page's release places it by its row
	ta.ok("add --stream jev jev-after --one --needs sprint-v1-release-1 --actor lead")
	ta.ok("add --stream sprint-v1-release v1-after --one --needs jev-1 --actor lead")
	ta.ok("hold m1 --reason 'nothing dealt: the critical cards stay off the fleet' --actor lead")
	ta.ok("start --actor lead")
	ta.ok("tick") // the tick counts the critical path into the where record

	srv := &sprintdash.Server{Read: func() ([]byte, error) { return ta.a.whereJSON("", false) }, Now: ta.a.now, Every: time.Second}
	critical := func(path string) []string {
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		var v struct {
			Data struct {
				Critical []struct {
					ID     string `json:"id"`
					Stream string `json:"stream"`
				} `json:"critical"`
				Rows json.RawMessage `json:"rows"`
			} `json:"data"`
		}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &v), path)
		assert.Nil(t, v.Data.Rows, "the rows are not served")
		var ids []string
		for _, c := range v.Data.Critical {
			assert.NotEmpty(t, c.Stream, "%s: %s is placed by its row, never by the cards dealt or merging", path, c.ID)
			ids = append(ids, c.ID)
		}
		slices.Sort(ids)
		return ids
	}
	assert.Equal(t, []string{"sprint-v1-release-1"}, critical("/api/sprint"), "the critical path is the release's")
	assert.Equal(t, []string{"jev-1"}, critical("/api/sprint?release=v1.1.0"))
	assert.Equal(t, []string{"jev-1", "sprint-v1-release-1"}, critical("/api/sprint?release=all"))
	read := func(path string) (string, []string, int64) {
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		var v struct {
			Release string `json:"release"`
			Data    struct {
				All    int64                      `json:"all"`
				Tables map[string]json.RawMessage `json:"tables"`
			} `json:"data"`
		}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &v), path)
		var work map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(v.Data.Tables["work"], &work), path)
		var names []string
		for k := range work {
			names = append(names, k)
		}
		slices.Sort(names)
		return v.Release, names, v.Data.All
	}
	rel, streams, all := read("/api/sprint")
	assert.Equal(t, "v1.0.0", rel)
	assert.Equal(t, []string{"promote-red-2026-10-05", "sprint-v1-release"}, streams)
	assert.Equal(t, int64(6), all, "the summary counts the release's cards alone")
	rel, streams, all = read("/api/sprint?release=v1.1.0")
	assert.Equal(t, "v1.1.0", rel)
	assert.Equal(t, []string{"jev"}, streams)
	assert.Equal(t, int64(6), all)
	rel, streams, all = read("/api/sprint?release=all")
	assert.Equal(t, "all", rel)
	assert.Len(t, streams, 3)
	assert.Equal(t, int64(12), all)
}

// Given nothing to read (no store, no sprint's server, no dashboard to pull), the dashboard
// refuses at once on stderr in the one grammar, at exit 2, and writes nothing to stdout:
// it opens no listener and logs no failed read (internal/ci TestEveryRefusalFollowsTheGrammar).
func TestDashboardWithNoStoreRefusesOnStderr(t *testing.T) {
	t.Parallel()
	a := newApp(func(string) string { return "" })
	a.loginFile = nil
	var out, errb strings.Builder
	code := a.run([]string{"dashboard", "--listen", "127.0.0.1:0", "--pull", "none"}, &out, &errb)
	assert.Equal(t, 2, code, "stderr: %s", errb.String())
	assert.Empty(t, out.String(), "a refusal writes nothing to stdout")
	assert.Equal(t, "nova-sprint dashboard REFUSED: "+dashboardNoStore+"; run: nova-sprint dashboard -h\n", errb.String())
}
