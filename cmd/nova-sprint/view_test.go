package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/config"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// The role views (view.go; docs/SPEC-SPRINT.md section 11, "Role views"; the owner,
// 2026-10-04: "i'd rather you hit this vs. hitting my dashboard which is for human eyes"):
// every test seeds a state on the in-memory twin with the fake clock and reads the view as a
// model would, with no socket.

// coordView reads view coordinator --json with the extra words given.
func (ta *testApp) coordView(extra string) coordinatorView {
	ta.t.Helper()
	var v coordinatorView
	ta.json(strings.TrimSpace("view coordinator "+extra), &v)
	require.Equal(ta.t, "coordinator", v.View)
	require.Equal(ta.t, viewSchema, v.Schema)
	return v
}

// item is the view's item of the key, and whether there is one.
func item(v coordinatorView, key string) (viewItem, bool) {
	for _, it := range v.Items {
		if it.K == key {
			return it, true
		}
	}
	return viewItem{}, false
}

// An idle fleet with a held wave: the alarms name it, each with the release that feeds the
// fleet, and a read given the cursor leaves out what it already showed.
func TestTheCoordinatorViewNamesAnIdleFleetAndItsRelease(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("add --stream s1 --count 2")
	ta.ok("add --stream s1 --sentinel s1-stop")
	ta.ok("add --stream s1 --count 2")
	ta.ok("start")
	ta.ok("tick")
	ta.ok("tick")
	ta.ok("take --as m1 s1-1.w1@1")
	ta.ok("finish --as m1 s1-1.w1@1 --report done")
	ta.ok("take --as m1 s1-2.w1@1")

	v := ta.coordView("")
	assert.Equal(t, 4, v.N.All, "the sentinel is no card of the counts: %+v", v.N)
	assert.Equal(t, 2, v.N.Waiting, "%+v", v.N)
	assert.Equal(t, 0, v.N.Ready, "%+v", v.N)
	idle, ok := item(v, "a:idle")
	require.True(t, ok, "an idle fleet is an alarm: %+v", v.Items)
	assert.Equal(t, itemAlarm, idle.T)
	assert.Equal(t, "nova-sprint release s1-stop --reason '<why the wave goes now>'", idle.Next)
	assert.Equal(t, v.N.Width-v.N.Busy, idle.B, "its weight is the width standing idle")
	dry, ok := item(v, "a:dry")
	require.True(t, ok, "nothing ready with cards waiting is an alarm: %+v", v.Items)
	assert.Equal(t, 2, dry.B)
	assert.Contains(t, dry.Next, "nova-sprint release s1-stop")
	assert.True(t, strings.HasPrefix(v.Sum, "seat=coordinator machine=running"), "the summary line comes first: %s", v.Sum)
	for i := 1; i < len(v.Items); i++ {
		assert.GreaterOrEqual(t, v.Items[i-1].B, v.Items[i].B, "ranked by the cards behind: %+v", v.Items)
	}
	assert.Empty(t, v.Rows, "the rows are --all's")
	assert.Len(t, ta.coordView("--all").Rows, 1, "--all carries every machine's row")

	// the same read again with its cursor: every alarm still stands, unchanged, and is left out
	again := ta.coordView("--since " + v.Cursor)
	assert.Empty(t, again.Items, "nothing changed: %+v", again.Items)
	assert.Equal(t, len(v.Items), again.Same)
	assert.Equal(t, 0, again.Gone)

	// the release feeds the fleet: the dry alarm is gone, and the next read with the old cursor counts it
	ta.ok("release s1-stop --reason 'the layer is green'")
	ta.ok("tick")
	after := ta.coordView("--since " + v.Cursor)
	_, still := item(ta.coordView(""), "a:dry")
	assert.False(t, still, "ready is no longer empty")
	assert.GreaterOrEqual(t, after.Gone, 1, "the dry alarm is gone: %+v", after)

	// the text: the summary first, at most twenty lines, the cursor last
	out := ta.ok("view coordinator")
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	assert.True(t, strings.HasPrefix(lines[0], "VIEW coordinator seat=coordinator"), "%s", out)
	assert.LessOrEqual(t, len(lines), viewTextLines+2, "%s", out)
	assert.True(t, strings.HasPrefix(lines[len(lines)-1], "cursor=1."), "%s", out)

	code, _, errs := ta.do("view coordinator --since nonsense")
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "--since wants the cursor a view printed")
}

// A judgment with cards behind it is the heaviest item, and its next is the inbox's first
// decision, whole.
func TestTheCoordinatorViewRanksAJudgmentByTheCardsBehindIt(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("add --stream s1 --count 1 --one")
	ta.ok("add --stream s1 s1-2 s1-3 --needs s1-1")
	ta.ok("add --stream s2 --count 1 --one")
	ta.deal(2)
	ta.failOnce("m1", "s1-1.w1@1", "tests red")

	v := ta.coordView("")
	var j viewItem
	at, dry := -1, -1
	for i, it := range v.Items {
		switch {
		case it.T == itemJudgment:
			j, at = it, i
		case it.K == "a:dry":
			dry = i
		}
	}
	require.GreaterOrEqual(t, at, 0, "the judgment is an item: %+v", v.Items)
	require.GreaterOrEqual(t, dry, 0, "nothing ready: %+v", v.Items)
	assert.Less(t, at, dry, "of two items of two cards behind, the judgment first: %+v", v.Items)
	assert.Equal(t, "a:stopped", v.Items[0].K, "a machine STOPPED with four cards to land outweighs it: %+v", v.Items)
	assert.Equal(t, sprint.NWorkFailed, j.W)
	assert.Equal(t, 2, j.B, "s1-2 and s1-3 wait on s1-1")
	assert.Equal(t, 1, j.N)
	assert.Contains(t, j.D, "rework")
	g := ta.group(sprint.NWorkFailed, "s1")
	assert.Equal(t, "j:"+g.ID, j.K)
	assert.Equal(t, strings.Join(g.Commands[0].Lines, " && "), j.Next, "next is the first decision's command, whole")
	assert.Equal(t, 1, v.N.J)
	assert.Contains(t, v.Sum, "j=1(max 2 behind)")

	// answered, the judgment is gone from the view
	ta.ok("rework s1-1 --fix 'the fix'")
	_, ok := item(ta.coordView(""), j.K)
	assert.False(t, ok, "an answered judgment needs the seat no more")
}

// A friend holding a card who has not reported for longer than viewStaleReport needs a look:
// the item names her and takes her cards back.
func TestTheCoordinatorViewNamesAFriendWithStaleReports(t *testing.T) {
	t.Parallel()
	ta, _ := friendCardApp(t, "friend amy", "amy")
	ta.ok("tick")
	ta.startFriend("amy", 1)
	v := ta.coordView("--all")
	_, ok := item(v, "f:amy")
	assert.False(t, ok, "she beat a moment ago: %+v", v.Items)
	var row viewRow
	for _, r := range v.Rows {
		if r.K == "f:amy" {
			row = r
		}
	}
	assert.Equal(t, viewRow{K: "f:amy", St: sprint.Up, W: 1, Wd: row.Wd, Rep: row.Rep}, row, "her row: one card working")

	ta.a.sleep(viewStaleReport + time.Minute)
	v = ta.coordView("")
	f, ok := item(v, "f:amy")
	require.True(t, ok, "a friend holding a card with no report for 16m: %+v", v.Items)
	assert.Equal(t, itemFriend, f.T)
	assert.Equal(t, 1, f.B)
	assert.Contains(t, f.S, "holds 0 ready, 1 working")
	assert.True(t, strings.HasPrefix(f.Next, "nova-sprint friend take amy --all-unstarted --reason 'amy "), "%s", f.Next)

	// her beat answers it
	ta.beatUp("amy")
	_, ok = item(ta.coordView(""), "f:amy")
	assert.False(t, ok, "she reported")
}

// The judgments the tick's lane check kept from rising are the coordinator's count
// (a-judgment-checks-the-lane-before-it-rises.w2): a friend whose beat names her card running
// inside its cap raises no finishes none, and the view counts it once, suppressed 1, its cause
// a live lane beside it, in the JSON and in the text's summary, however many ticks keep it
// quiet; when her beat names the running list empty her finishes none rises and the count stays.
func TestTheCoordinatorViewCountsTheJudgmentsTheLaneCheckSuppressed(t *testing.T) {
	t.Parallel()
	ta, _ := friendCardApp(t, "friend amy", "amy")
	ta.ok("tick")
	v := ta.coordView("")
	assert.Equal(t, 0, v.N.Suppressed, "nothing kept quiet yet: %+v", v.N)
	assert.Contains(t, v.Sum, "| suppressed 0 (lane 0 readers 0 tier 0)")

	// 41 minutes with no finish, past the friend-finish window, her beat naming the card running
	for range 4 {
		ta.a.sleep(10*time.Minute + 15*time.Second)
		ta.ok("friend beat amy --running s1-1.w1")
		ta.pong("amy")
		ta.ok("tick")
	}
	assert.Empty(t, groupsOf(ta, sprint.NFriendIdle), "a live lane inside its cap raises nothing")
	assert.Empty(t, groupsOf(ta, sprint.NStalled), "and no stall")
	v = ta.coordView("")
	assert.Equal(t, 1, v.N.Suppressed, "one judgment kept quiet, counted once over the ticks: %+v", v.N)
	assert.Equal(t, suppressedWhy{Lane: 1}, v.N.By, "its cause: a live lane")
	assert.Contains(t, v.Sum, "| suppressed 1 (lane 1 readers 0 tier 0)")
	assert.Contains(t, strings.SplitN(ta.ok("view coordinator"), "\n", 2)[0], "| suppressed 1 (lane 1 readers 0 tier 0)", "the text's summary line")
	var raw map[string]json.RawMessage
	ta.json("view coordinator", &raw)
	assert.Contains(t, string(raw["n"]), `"suppressed":1,"by":{"lane":1,"readers":0,"tier":0}`)
	var row viewRow
	for _, r := range ta.coordView("--all").Rows {
		if r.K == "f:amy" {
			row = r
		}
	}
	assert.Regexp(t, `^running [0-9]+m of [0-9]+m$`, row.Run, "her row says her run: %+v", row)

	// her beat names the list empty, so it stops naming the card (a bare beat leaves the
	// list): her finishes none rises (her running beat a minute ago is still activity to
	// the stall ladder, so it is not a stall), and what was kept quiet stays counted
	ta.a.sleep(time.Minute)
	ta.ok("friend beat amy --running=")
	ta.pong("amy")
	ta.ok("tick")
	idle := groupsOf(ta, sprint.NFriendIdle)
	require.Len(t, idle, 1, "no live lane: her finishes none rises, and nothing keeps it quiet: %+v", ta.inboxGroups())
	assert.Equal(t, []string{"friend.amy"}, idle[0].Primaries)
	assert.Equal(t, 1, ta.coordView("").N.Suppressed)
}

// groupsOf is the inbox's groups of the type, of any kind (a judgment a rule answered is
// decided).
func groupsOf(ta *testApp, typ string) []sprint.Group {
	ta.t.Helper()
	var out []sprint.Group
	for _, g := range ta.inboxGroups() {
		if g.Type == typ {
			out = append(out, g)
		}
	}
	return out
}

// A member's view: its cards in order, working first, each with its brief, base, paths and
// deadline, and next the step that moves its first card.
func TestTheWorkerViewOfAMember(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	dir := t.TempDir()
	// add reads each brief at its base (the brief checks): a twin of the repository holds both files
	remote, _ := twinRemote(t, ta, map[string]string{"cmd/nova-sprint/s1-1.go": "package main\n", "cmd/nova-sprint/s1-2.go": "package main\n"}, "sprint/s1")
	for _, id := range []string{"s1-1", "s1-2"} {
		brief := passingBrief(id + ": a card tier: flash\nREPO: " + remote + "\nBASE: sprint/s1\nPATHS: cmd/nova-sprint/" + id + ".go\nTEST: none a fixture of the worker view")
		require.NoError(t, os.WriteFile(filepath.Join(dir, id+".md"), []byte(brief), 0o644))
	}
	ta.ok("add --stream s1 --brief-dir " + dir)
	ta.deal(2)

	var v workerView
	ta.json("view worker --as m1", &v)
	assert.Equal(t, "worker", v.View)
	assert.Equal(t, viewSchema, v.Schema)
	assert.Equal(t, "member", v.Kind)
	require.Len(t, v.Cards, 2)
	c := v.Cards[0]
	assert.Equal(t, "s1-1.w1", c.ID)
	assert.Equal(t, sprint.Ready, c.St)
	assert.Equal(t, "sprint/s1", c.Base)
	assert.Equal(t, []string{"cmd/nova-sprint/s1-1.go"}, c.Paths)
	assert.Equal(t, "nova-sprint card s1-1 --brief", c.Brief)
	assert.Equal(t, 1, c.Att)
	assert.False(t, c.DL.IsZero(), "a dealt card has a deadline")
	assert.Equal(t, "nova-sprint take --as m1 s1-1.w1@1 --epoch 0", v.Next)

	ta.ok("take --as m1 s1-2.w1@1")
	ta.json("view worker --as m1", &v)
	assert.Equal(t, "s1-2.w1", v.Cards[0].ID, "working first")
	assert.Equal(t, sprint.Working, v.Cards[0].St)
	assert.True(t, strings.HasPrefix(v.Next, "nova-sprint finish --as m1 s1-2.w1@1 --epoch 0"), "%s", v.Next)

	// a finished result waits in review: listed until it lands
	ta.ok("finish --as m1 s1-2.w1@1 --report done")
	ta.json("view worker --as m1", &v)
	require.Len(t, v.Wait, 1, "%+v", v)
	assert.Equal(t, waitCard{ID: "s1-2.w1", P: "s1-2", St: sprint.Review, Age: "0s"}, v.Wait[0])

	// the cursor leaves out what it showed
	var again workerView
	ta.json("view worker --as m1 --since "+v.Cursor, &again)
	assert.Empty(t, again.Cards)
	assert.Empty(t, again.Wait)
	assert.Equal(t, 2, again.Same)

	out := ta.ok("view worker --as m1")
	assert.Contains(t, out, "VIEW worker member m1: working 0 ready 1, results not landed 1\n")
	assert.Contains(t, out, "CARD s1-1.w1 ready att=1 base=sprint/s1 paths=cmd/nova-sprint/s1-1.go")
	assert.Contains(t, out, "WAIT s1-2.w1 review 0s\n")

	code, _, errs := ta.do("view worker --as nobody")
	assert.Equal(t, 1, code)
	assert.Contains(t, errs, "nobody is no fleet member and no friend of the sprint")
	code, _, _ = ta.do("view worker")
	assert.Equal(t, 2, code, "--as is required")
}

// A friend's view: her card's brief is the BRIEF.md friend sync writes, and next is the
// report that finishes it.
func TestTheWorkerViewOfAFriend(t *testing.T) {
	t.Parallel()
	ta, _ := friendCardApp(t, "friend amy", "amy")
	ta.ok("tick")
	ta.startFriend("amy", 1) // dealt ready; working once she starts it
	var v workerView
	ta.json("view worker --as amy", &v)
	assert.Equal(t, "friend", v.Kind)
	require.Len(t, v.Cards, 1)
	briefPath := "~/amy-working/inbox/s1-1.w1/BRIEF.md"
	assert.Equal(t, briefPath, v.Cards[0].Brief)
	assert.Equal(t, "sprint/s1-1.w1.g1.e0", v.Cards[0].Br)
	reportPath := "~/amy-working/outbox/s1-1.w1/REPORT.md"
	assert.Equal(t, "finish s1-1.w1: push to sprint/s1-1.w1.g1.e0, then write "+reportPath+" with Verdict: LAND|HOLD|FAIL and Head: <sha>", v.Next)
}

// A machine row with a nova_root moves the friend's brief and report paths under
// that root: the view honours the row, not today's ~/<name>-working.
func TestTheWorkerViewOfAFriendHonoursTheMachineRowsNovaRoot(t *testing.T) {
	t.Parallel()
	ta, _ := friendCardApp(t, "friend amy", "amy")
	ta.a.inventory = func(context.Context, string) ([]config.MachineWidth, error) {
		return []config.MachineWidth{{Machine: "studio", NovaRoot: "/Volumes/nova"}}, nil
	}
	ta.ok("tick")
	ta.startFriend("amy", 1)
	var v workerView
	ta.json("view worker --as amy", &v)
	require.Len(t, v.Cards, 1)
	want := "/Volumes/nova/ai/amy/working"
	assert.Equal(t, want+"/inbox/s1-1.w1/BRIEF.md", v.Cards[0].Brief)
	assert.Equal(t, "finish s1-1.w1: push to sprint/s1-1.w1.g1.e0, then write "+want+"/outbox/s1-1.w1/REPORT.md with Verdict: LAND|HOLD|FAIL and Head: <sha>", v.Next)
}

// The server serves both views read-only on GET, through the handler with no socket: JSON,
// gzipped for a client that takes it; a name or a cursor of the wrong shape is a 400, an
// unknown worker a 404, any other method a 405.
func TestTheServerServesTheViews(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("add --stream s1 --count 2")
	ta.deal(2)
	ta.a.serveAddr = "mem:0"
	get := func(method, target string, gz bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, target, nil)
		if gz {
			r.Header.Set("Accept-Encoding", "gzip")
		}
		w := httptest.NewRecorder()
		ta.a.ServeHTTP(w, r) // the fleet's listener
		return w
	}
	w := get(http.MethodGet, "/api/view/coordinator?all=1", false)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Equal(t, "application/json", w.Header().Get("Content-Type"))
	var v coordinatorView
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &v))
	assert.Equal(t, "coordinator", v.View)
	assert.Len(t, v.Rows, 1)
	assert.Equal(t, strings.TrimRight(ta.ok("view coordinator --json --all"), "\n"), strings.TrimRight(w.Body.String(), "\n"), "the route serves the verb's JSON")

	w = get(http.MethodGet, "/api/view/worker?as=m1", true)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Equal(t, "gzip", w.Header().Get("Content-Encoding"))
	zr, err := gzip.NewReader(bytes.NewReader(w.Body.Bytes()))
	require.NoError(t, err)
	body, err := io.ReadAll(zr)
	require.NoError(t, err)
	var wv workerView
	require.NoError(t, json.Unmarshal(body, &wv))
	assert.Len(t, wv.Cards, 2)

	w = get(http.MethodGet, "/api/view/worker?as=m1&since="+wv.Cursor, false)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &wv))
	assert.Empty(t, wv.Cards)
	assert.Equal(t, 2, wv.Same)

	for target, code := range map[string]int{
		"/api/view/worker?as=no+body":        http.StatusBadRequest,
		"/api/view/worker?as=nobody":         http.StatusNotFound,
		"/api/view/coordinator?since=x":      http.StatusBadRequest,
		"/api/view/coordinator?all=sometime": http.StatusBadRequest,
		"/api/view/reader":                   http.StatusNotFound,
	} {
		assert.Equal(t, code, get(http.MethodGet, target, false).Code, target)
	}
	assert.Equal(t, http.StatusMethodNotAllowed, get(http.MethodPost, "/api/view/coordinator", false).Code)

	// the loopback listener serves them too
	r := httptest.NewRequest(http.MethodGet, "/api/view/coordinator", nil)
	lw := httptest.NewRecorder()
	localHandler{ta.a}.ServeHTTP(lw, r)
	assert.Equal(t, http.StatusOK, lw.Code, lw.Body.String())
}

// After a clear the cards dealt are read at the sprint's epoch: where --json --cards (the
// dashboard's pull routes) and the worker's view list them. The cell read gives stored ids,
// and binding them to the epoch a second time found no card at any epoch after the first
// (found on the live store at epoch 15, 2026-10-04: where --json --cards listed none).
func TestTheCardsDealtAreReadAfterAClear(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("clear --confirm sprint")
	ta.ok("add --stream s1 --count 2")
	ta.deal(2)
	var w whereView
	ta.json("where --cards", &w)
	assert.Len(t, w.Cards, 2, "the cards dealt at epoch 1: %+v", w.Cards)
	var v workerView
	ta.json("view worker --as m1", &v)
	assert.Equal(t, uint64(1), v.Epoch)
	require.Len(t, v.Cards, 2, "%+v", v)
	assert.Equal(t, "nova-sprint take --as m1 s1-1.w1@1 --epoch 1", v.Next)
}
