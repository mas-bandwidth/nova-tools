package sprintdash

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fixture is a where --json --cards object: two friends, two machines, a card of each
// state on amy and on bench-a, one judgment on each, and a providers table.
func fixture(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/where_cards.json")
	require.NoError(t, err)
	return b
}

func copyOf(t *testing.T, body []byte) *sprintCopy {
	t.Helper()
	var c sprintCopy
	require.NoError(t, json.Unmarshal(body, &c))
	return &c
}

// A friend's view is the sprint line, her friends-table row, the cards dealt to her row
// (friend.<name>) and not finished, and the open judgments naming them: nobody else's.
func TestPullFriendViewIsHerOwn(t *testing.T) {
	t.Parallel()
	v, ok := pullView(copyOf(t, fixture(t)), KindFriend, "amy")
	require.True(t, ok)
	assert.Equal(t, SprintLine{Landed: 352, All: 1205, Held: 770, ETA: "2d7h", Machine: "running"}, v.Sprint)
	assert.Equal(t, PullRow{Status: "up", Ready: "1", Working: "1", Width: "8", Done: "9", OKPct: "33.3%"}, v.Row)
	var ids []string
	for _, c := range v.Cards {
		ids = append(ids, c.ID+" "+c.State)
		assert.Empty(t, c.Member+c.Primary, "the row and the primary are the view's, not repeated on each card")
	}
	assert.Equal(t, []string{"ci-03.w2 working", "ci-07.w1 ready"}, ids)
	assert.Equal(t, []PullJudgment{{ID: "ci-03-failed.2", Kind: "work came back failed", Card: "ci-03"}}, v.Judgments)

	v, ok = pullView(copyOf(t, fixture(t)), KindFriend, "bob")
	require.True(t, ok)
	assert.Empty(t, v.Cards, "a friend with no card dealt")
	b, err := json.Marshal(v)
	require.NoError(t, err)
	assert.Contains(t, string(b), `"cards":[],"judgments":[]`, "empty lists, never null")
}

// A name is a row of its table or nothing: a machine is not a friend, a friend's fleet row
// is not a machine, and a path trick is a name no table holds.
func TestPullNamesAreRowsOfTheirTable(t *testing.T) {
	t.Parallel()
	c := copyOf(t, fixture(t))
	for _, tc := range []struct{ kind, name string }{
		{KindFriend, "bench-a"}, {KindMachine, "amy"}, {KindMachine, "friend.amy"}, {KindFriend, ""},
		{KindFriend, "../amy"}, {KindFriend, "amy/x"}, {KindMachine, "bench-a%00"},
	} {
		_, ok := pullView(c, tc.kind, tc.name)
		assert.False(t, ok, "%s %q", tc.kind, tc.name)
	}
}

// The text form: one line an item, no markup; the sprint line, the row, a line per card,
// a line per judgment; times from the copy's at.
func TestPullTextIsOneLineAnItem(t *testing.T) {
	t.Parallel()
	c := copyOf(t, fixture(t))
	v, _ := pullView(c, KindFriend, "amy")
	assert.Equal(t, `sprint 352/1205 landed held 770 eta 2d7h machine running at 11:20:00 AM
friend amy up ready 1 working 1/8 done 9 ok 33.3%
ci-03.w2 ci working 16m due 1h10m sprint/ci-03.w2.g1.e15
ci-07.w1 ci ready 2m due 5h58m sprint/ci-07.w1.g1.e15
judgment ci-03-failed.2 work came back failed on ci-03
`, v.Text())
	v, _ = pullView(c, KindMachine, "bench-a")
	assert.Equal(t, `sprint 352/1205 landed held 770 eta 2d7h machine running at 11:20:00 AM
machine bench-a up ready 1 working 1/16 done 307 ok 70.4% load 18.6%
ci-04.w1 ci working 2h30m late 30m sprint/ci-04.w1.g1.e15
ci-05.w1 ci ready 30s due - sprint/ci-05.w1.g1.e15
judgment tick-late-ci-04.1 a work card is past its deadline on ci-04
`, v.Text())
	v, _ = pullView(c, KindMachine, "bench-b")
	assert.Equal(t, "machine bench-b down ready 0 working 0/4 done 60 ok 80.0% load -\n", strings.SplitAfterN(v.Text(), "\n", 2)[1])
}

// A friend holding 16 cards reads whole in about a kilobyte: what an AI pulls with one curl.
func TestPullTextOfSixteenCardsIsAboutAKilobyte(t *testing.T) {
	t.Parallel()
	c := copyOf(t, fixture(t))
	c.Cards = nil
	at := c.At.UTC()
	streams := []string{"ci", "classes", "contract", "negatives"}
	for i := range 16 {
		s := streams[i%len(streams)]
		id := fmt.Sprintf("%s-%02d.w1", s, i+1)
		state := "working"
		if i >= 8 {
			state = "ready"
		}
		c.Cards = append(c.Cards, PullCard{ID: id, Primary: id[:len(id)-3], Stream: s, Member: "friend.amy", State: state,
			Since: at.Add(-time.Duration(i+1) * 7 * time.Minute), Deadline: at.Add(time.Duration(16-i) * 9 * time.Minute), Branch: "sprint/" + id + ".g1.e15"})
	}
	v, _ := pullView(c, KindFriend, "amy")
	text := v.Text()
	assert.Equal(t, 2+16, strings.Count(text, "\n"))
	// 1,233 bytes with these stream names (up to nine letters, each in the id and the
	// branch too); 1,017 with every stream named ci
	assert.Less(t, len(text), 1280, "%d bytes:\n%s", len(text), text)
	assert.NotContains(t, text, "<")
}

func (r *rig) pull(path string) *httptest.ResponseRecorder {
	r.t.Helper()
	w := httptest.NewRecorder()
	r.s.Pull().ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
	return w
}

// Every route answers from the one cached copy: however many workers pull in a second,
// the sprint is read once; a second later, once more. Each answer is no-store and carries
// the copy's time.
func TestPullReadsTheSprintOncePerSecondHoweverManyPull(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.next = func() ([]byte, error) { return fixture(t), nil }
	for i := range 50 {
		path := []string{"/friend/amy", "/api/friend/amy", "/machine/bench-a", "/api/machine/bench-a", "/api/sprint"}[i%5]
		w := r.pull(path)
		require.Equal(t, http.StatusOK, w.Code, path)
		assert.Equal(t, "no-store, max-age=0", w.Header().Get("Cache-Control"), path)
		assert.Equal(t, "2026-10-03T11:20:00-04:00", w.Header().Get("Sprint-At"), path)
	}
	assert.Equal(t, 1, r.reads, "fifty pulls in one instant: one read")
	r.advance(time.Second)
	r.pull("/friend/amy")
	assert.Equal(t, 2, r.reads, "a second later: one more")
	r.api()
	assert.Equal(t, 2, r.reads, "the page reads the same copy")
}

// The routes on one server: the text and JSON forms, the whole copy, an unknown name or
// route a 404 of one line, a write refused, and nothing served before the first read.
func TestPullRoutes(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.next = func() ([]byte, error) { return nil, fmt.Errorf("where exited 2") }
	w := r.pull("/friend/amy")
	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
	assert.Equal(t, 1, strings.Count(w.Body.String(), "\n"))

	r.advance(time.Second)
	r.next = func() ([]byte, error) { return fixture(t), nil }
	w = r.pull("/friend/amy")
	assert.Equal(t, "text/plain; charset=utf-8", w.Header().Get("Content-Type"))
	assert.True(t, strings.HasPrefix(w.Body.String(), "sprint 352/1205 landed"), w.Body.String())

	w = r.pull("/api/friend/amy")
	assert.Equal(t, "application/json", w.Header().Get("Content-Type"))
	var v PullView
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &v))
	assert.Equal(t, "amy", v.Name)
	assert.Equal(t, KindFriend, v.Kind)
	assert.Len(t, v.Cards, 2)

	w = r.pull("/api/machine/bench-a")
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &v))
	assert.Equal(t, "18.6%", v.Row.Load)
	assert.Equal(t, "2026-10-03T14:50:00Z", v.Cards[0].Deadline.UTC().Format(time.RFC3339))

	var whole struct {
		Data json.RawMessage `json:"data"`
	}
	require.NoError(t, json.Unmarshal(r.pull("/api/sprint").Body.Bytes(), &whole))
	assert.JSONEq(t, string(fixture(t)), string(whole.Data))

	for path, want := range map[string]string{
		"/friend/zed":          `no friend named "zed" on the friends table`,
		"/friend/bench-a":      `no friend named "bench-a" on the friends table`,
		"/machine/amy":         `no machine named "amy" on the fleet table`,
		"/friend/../../etc":    `no friend named "../../etc" on the friends table`,
		"/apifriend/amy":       "not found",
		"/friend":              "not found",
		"/":                    "not found",
		"/index.html":          "not found",
		"/api/reader/reader-a": "not found",
	} {
		w := r.pull(path)
		assert.Equal(t, http.StatusNotFound, w.Code, path)
		assert.Contains(t, w.Body.String(), want, path)
		assert.Equal(t, 1, strings.Count(w.Body.String(), "\n"), path)
	}
	for _, m := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		w := httptest.NewRecorder()
		r.s.Pull().ServeHTTP(w, httptest.NewRequest(m, "/friend/amy", strings.NewReader("x")))
		assert.Equal(t, http.StatusMethodNotAllowed, w.Code, m)
	}
	assert.Equal(t, "ok\n", r.pull("/healthz").Body.String())

	w = r.pull("/team")
	assert.Equal(t, "text/plain; charset=utf-8", w.Header().Get("Content-Type"))
	assert.Contains(t, w.Body.String(), "\nfriend bob down ")
	w = r.pull("/api/team")
	var team TeamView
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &team))
	assert.Len(t, team.Friends, 2)
	assert.Equal(t, "no-store, max-age=0", w.Header().Get("Cache-Control"))
	assert.Equal(t, http.StatusNotFound, r.pull("/events/team").Code, "no team stream")
}

// The team: the sprint line, then every friend's row and the cards she holds, so each
// friend sees what every other is on with one curl.
func TestTeamIsEveryFriendAndHerCards(t *testing.T) {
	t.Parallel()
	tv := teamView(copyOf(t, fixture(t)))
	assert.Equal(t, `sprint 352/1205 landed held 770 eta 2d7h machine running at 11:20:00 AM
friend amy up working 1/8 ready 1 done 9 ok 33.3%
  ci-03.w2 ci working 16m
  ci-07.w1 ci ready 2m
friend bob down working 0/8 ready 0 done 0 ok 0.0%
`, tv.Text())
	b, err := json.Marshal(tv)
	require.NoError(t, err)
	assert.JSONEq(t, `{"at":"2026-10-03T11:20:00-04:00",
	  "sprint":{"landed":352,"all":1205,"held":770,"eta":"2d7h","machine":"running"},
	  "friends":[
	    {"name":"amy","row":{"status":"up","ready":"1","working":"1","width":"8","done":"9","okpct":"33.3%"},
	     "cards":[{"id":"ci-03.w2","stream":"ci","state":"working","since":"2026-10-03T15:04:00Z"},
	              {"id":"ci-07.w1","stream":"ci","state":"ready","since":"2026-10-03T15:18:00Z"}]},
	    {"name":"bob","row":{"status":"down","ready":"0","working":"0","width":"8","done":"0","okpct":"0.0%"},"cards":[]}]}`, string(b))
}

// Six friends at their full width of eight cards each read whole in about 2 KB: 2,100
// bytes with stream names of up to nine letters (each in the id too).
func TestTeamTextOfSixFullFriendsIsAboutTwoKilobytes(t *testing.T) {
	t.Parallel()
	c := copyOf(t, fixture(t))
	c.Cards, c.Tables["friends"] = nil, map[string]map[string]string{}
	streams := []string{"ci", "classes", "contract", "negatives"}
	for f := range 6 {
		name := fmt.Sprintf("friend-%d", f+1)
		c.Tables["friends"][name] = map[string]string{"status": "up", "ready": "4", "working": "4", "width": "8", "done": "12", "okpct": "75.0%"}
		for i := range 8 {
			s := streams[(f+i)%len(streams)]
			id := fmt.Sprintf("%s-%02d.w1", s, f*8+i+1)
			c.Cards = append(c.Cards, PullCard{ID: id, Stream: s, Member: "friend." + name, State: "working", Since: c.At.Add(-time.Duration(i+1) * 9 * time.Minute)})
		}
	}
	text := teamView(c).Text()
	assert.Equal(t, 1+6+48, strings.Count(text, "\n"))
	assert.Less(t, len(text), 2200, "%d bytes:\n%s", len(text), text)
}
