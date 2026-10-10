package sprintdash

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fixCopy is a where --json --cards copy of one stream, s1, with the cards given dealt: s1's
// work row has every primary of the cards in working, amy (a friend) and m1 (a machine) the
// rows they are dealt to; prio is where's priorities, "" for none.
func fixCopy(prio string, cards ...string) []byte {
	p := ""
	if prio != "" {
		p = `"priorities":` + prio + `,`
	}
	return []byte(`{"landed":0,"all":5,` + p + `"tables":{` +
		`"work":{"s1":{"waiting":"1","ready":"0","working":"` + strconv.Itoa(len(cards)) + `","review":"1","merging":"0","landed":"0","cost":"$1.00"}},` +
		`"fleet":{"m1":{"status":"up","width":"4","working":"1","ready":"0","done":"0","ok":"0"}},` +
		`"friends":{"amy":{"status":"up","width":"8","working":"1","ready":"0","done":"0","ok":"0"}}},` +
		`"cards":[` + strings.Join(cards, ",") + `]}`)
}

// dealt is a dealt card of s1 as where --json --cards prints it, with extra fields after.
func dealt(id, primary, member, state, extra string) string {
	return `{"id":"` + id + `","primary":"` + primary + `","stream":"s1","member":"` + member + `","state":"` + state + `","branch":"b"` + extra + `}`
}

// served is the view of body, read back as a map.
func served(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var v map[string]any
	require.NoError(t, json.Unmarshal(fixView(body), &v))
	return v
}

func workRow(v map[string]any) map[string]any {
	return v["tables"].(map[string]any)["work"].(map[string]any)["s1"].(map[string]any)
}

func tableRow(v map[string]any, table, name string) map[string]any {
	return v["tables"].(map[string]any)[table].(map[string]any)[name].(map[string]any)
}

// A card at the fix level is at fix on its first attempt: marked, counted on its row, taken
// off its stream's working into fix, listed at fix, and the copy's fix counts it.
func TestFixViewMarksACardAtTheFixLevel(t *testing.T) {
	t.Parallel()
	v := served(t, fixCopy(`{"fix":["s1-1"]}`, dealt("s1-1.w1", "s1-1", "friend.amy", "working", `,"priority":"fix"`)))
	card := v["cards"].([]any)[0].(map[string]any)
	assert.Equal(t, true, card["fix"])
	assert.Equal(t, "1", tableRow(v, "friends", "amy")["fix"], "amy's row counts the fix card she works")
	assert.NotContains(t, tableRow(v, "fleet", "m1"), "fix", "a row with no fix card has no mark")
	w := workRow(v)
	assert.Equal(t, "1", w["fix"])
	assert.Equal(t, "0", w["working"], "the primary at fix is off working, so the row still sums to its cards")
	assert.Equal(t, "1", w["review"], "review is untouched")
	assert.InDelta(t, 1, v["fix"], 0)
	assert.Equal(t, map[string]any{"fix": []any{"s1-1"}}, v["priorities"])
}

// While where prints no fix level, a card on its second or later attempt is at fix, and is
// taken off the high list; a first attempt is not.
func TestFixViewMarksASecondAttemptWhileTheFixLevelIsAbsent(t *testing.T) {
	t.Parallel()
	v := served(t, fixCopy(`{"high":["s1-2","s1-3"]}`,
		dealt("s1-2.w2", "s1-2", "m1", "working", ""),
		dealt("s1-3.w1", "s1-3", "friend.amy", "working", ""),
		dealt("s1-4.w1", "s1-4", "m1", "ready", `,"attempt":3`)))
	cards := v["cards"].([]any)
	assert.Equal(t, true, cards[0].(map[string]any)["fix"], "the second attempt, read off its id")
	assert.NotContains(t, cards[1].(map[string]any), "fix", "a first attempt is not at fix")
	assert.Equal(t, true, cards[2].(map[string]any)["fix"], "the attempt field, when where prints it, before the id")
	assert.Equal(t, "1", tableRow(v, "fleet", "m1")["fix"], "a row counts its fix cards working, not ready")
	assert.NotContains(t, tableRow(v, "friends", "amy"), "fix")
	w := workRow(v)
	assert.Equal(t, "2", w["fix"])
	assert.Equal(t, "1", w["working"])
	assert.Equal(t, map[string]any{"high": []any{"s1-3"}, "fix": []any{"s1-2", "s1-4"}}, v["priorities"])
}

// A blocker or a critical keeps its red on any attempt: never at fix. A copy with no card at
// fix is served exactly as where printed it, byte for byte.
func TestFixViewNeverMarksABlockerOrACritical(t *testing.T) {
	t.Parallel()
	body := fixCopy(`{"blocker":["s1-5"],"critical":["s1-6"],"critical (by weight, not yet ordered)":["s1-7"]}`,
		dealt("s1-5.w3", "s1-5", "m1", "working", ""),
		dealt("s1-6.w2", "s1-6", "friend.amy", "working", ""),
		dealt("s1-7.w2", "s1-7", "friend.amy", "ready", ""),
		dealt("s1-8.w4", "s1-8", "m1", "ready", `,"priority":"blocker"`))
	assert.Equal(t, string(body), string(fixView(body)), "nothing at fix: the copy as where printed it")
	plain := fixCopy("", dealt("s1-1.w1", "s1-1", "m1", "working", ""))
	assert.Equal(t, string(plain), string(fixView(plain)))
	assert.Equal(t, "not json", string(fixView(json.RawMessage("not json"))))
}

// Where's own counts win: a work row that prints its fix is left as printed (its columns leave
// those cards out already), and a row's fix_working is its fix.
func TestFixViewTakesWheresOwnCounts(t *testing.T) {
	t.Parallel()
	body := fixCopy("", dealt("s1-2.w2", "s1-2", "m1", "working", ""))
	body = bytes.Replace(body, []byte(`"review":"1",`), []byte(`"review":"1","fix":"3",`), 1)
	body = bytes.Replace(body, []byte(`"amy":{"status":"up",`), []byte(`"amy":{"fix_working":"2","status":"up",`), 1)
	v := served(t, body)
	w := workRow(v)
	assert.Equal(t, "3", w["fix"])
	assert.Equal(t, "1", w["working"], "where's row is not taken from again")
	assert.InDelta(t, 3, v["fix"], 0)
	assert.Equal(t, "2", tableRow(v, "friends", "amy")["fix"])
	assert.Equal(t, "1", tableRow(v, "fleet", "m1")["fix"])
}

// The page's constants, read in Go with no node: the purple, its classes, the state and flow
// orders, and Work's fix column between review and merging.
func TestFixPageConstants(t *testing.T) {
	t.Parallel()
	page, js := string(file("index.html")), string(file("app.js"))
	for name, want := range map[string]string{"--s-fix": "#8b5cf6", "--p-fix": "var(--s-fix)"} {
		m := regexp.MustCompile(regexp.QuoteMeta(name) + `:\s*([^;]+);`).FindStringSubmatch(page)
		require.NotNil(t, m, "the page defines %s", name)
		assert.Equal(t, want, strings.TrimSpace(m[1]), name)
	}
	assert.Contains(t, page, ".fix { background: var(--s-fix); }")
	assert.Contains(t, page, ".p-fix { background: var(--p-fix); }")
	assert.Contains(t, js, `FLOW.forEach`, "Work headers and cells follow the flow order")
	assert.Contains(t, js, `var STATES = ["landed", "merging", "fix", "review", "working", "ready", "waiting"];`)
	assert.Contains(t, js, `var FLOW = ["waiting", "ready", "working", "review", "fix", "merging", "landed"];`)
}

// fixDriver draws a copy with app.js on the scroll test's DOM shim and reads back the state
// legend, the progress bar, Work's ci row and total, a fleet and a friends track, the friends
// total, the in-flight tooltip and the priority marks.
const fixDriver = `
context.render(input.data);
const kids = id => doc.getElementById(id).children;
const row = (id, name) => kids(id).find(r => r.children[0] && r.children[0].textContent === name);
const cls = n => n._classes.filter(c => c !== 'cell' && c !== 'flash').join(' ');
const total = id => kids(id).find(r => r._classes.includes('total'));
process.stdout.write(JSON.stringify({
  legend: kids('legend').map(i => i.textContent),
  bar: kids('overall').map(cls),
  ci: row('streams', 'ci').children.map(c => c.textContent),
  workTotal: total('streams').children.map(c => c.textContent),
  bench: row('fleet', 'bench-a').children[3].children.map(cls),
  benchTitle: row('fleet', 'bench-a').children[3].title,
  amy: row('friends', 'amy').children[3].children.map(cls),
  friendsTotal: total('friends').children[3].textContent,
  inflight: doc.getElementById('inflight-sub').title,
}));
`

// The page draws fix purple in the right place: the legend and the bar carry fix between review
// and merging, Work's row and total its fix column between them, a track's lit cells run
// blocker, critical, fix, reads, working, and the mark is purple after the reds.
func TestFixPageDrawsFixBetweenReviewAndMerging(t *testing.T) {
	t.Parallel()
	nodePath, err := exec.LookPath("node")
	if err != nil {
		if os.Getenv("NOVA_CI") == "1" {
			require.NoError(t, err, "node is required for the dashboard JS behavioural test")
		}
		t.Skip("node is not installed on this machine; the dashboard JS behavioural test needs it")
	}
	var d map[string]any
	require.NoError(t, json.Unmarshal(fixView(fixture(t)), &d))
	bench := d["tables"].(map[string]any)["fleet"].(map[string]any)["bench-a"].(map[string]any)
	for k, v := range map[string]string{"working": "9", "blocker_working": "1", "critical_working": "1", "fix": "2", "reads_working": "3", "high_working": "1", "normal_working": "2"} {
		bench[k] = v
	}
	d["priorities"].(map[string]any)["blocker"] = []string{"ci-09"}

	shim, _, ok := strings.Cut(scrollShim, "// the viewer:")
	require.True(t, ok, "the scroll test's shim has its viewer")
	in, err := json.Marshal(map[string]any{"appJS": string(file("app.js")), "data": d})
	require.NoError(t, err)
	cmd := exec.Command(nodePath, "-e", shim+fixDriver)
	cmd.Stdin = bytes.NewReader(in)
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &outBuf, &errBuf
	require.NoError(t, cmd.Run(), "node runner failed: %s", errBuf.String())
	require.Empty(t, errBuf.String(), "app.js threw while drawing")
	var res struct {
		Legend, Bar, CI, WorkTotal, Bench, Amy []string
		BenchTitle, FriendsTotal, Inflight     string
	}
	require.NoError(t, json.Unmarshal(outBuf.Bytes(), &res), outBuf.String())

	assert.Equal(t, []string{"landed 3", "merging 1", "fix 1", "review 0", "working 1", "ready 1", "waiting 9"}, res.Legend)
	want := []string{"landed", "landed", "landed", "merging", "fix", "working", "ready"}
	for i := 0; i < 9; i++ {
		want = append(want, "waiting")
	}
	assert.Equal(t, want, res.Bar, "one cell a card, fix between merging and review")
	// stream, status, waiting, ready, working, review, fix, merging, then landed (markup) and cost
	require.Len(t, res.CI, 10)
	assert.Equal(t, []string{"ci", "working", "9", "1", "1", "0", "1", "1"}, res.CI[:8])
	assert.Equal(t, "$2.15", res.CI[9])
	assert.Equal(t, "1", res.WorkTotal[6], "the total row's fix")
	assert.Equal(t, []string{"p-blocker", "p-critical", "p-fix", "p-fix", "p-reader", "p-reader", "p-reader", "working", "working", "", "", "", "", "", "", ""}, res.Bench,
		"blocker, critical, fix, reads (one a cell), then the working blue")
	assert.Equal(t, "9 working of 16: 1 blocker, 1 critical, 2 fix, 3 reads", res.BenchTitle)
	assert.Equal(t, []string{"p-fix", "", "", "", "", "", "", ""}, res.Amy, "amy's second attempt is her purple cell")
	assert.Equal(t, "", res.FriendsTotal, "no fix figure under the bars (unasked text)")
	assert.Equal(t, "1 working, 0 review, 1 fix, 1 merging", res.Inflight)
}

// New servers state the priority; legacy attempt inference must not override it.
func TestFixViewRespectsExplicitReworkPolicy(t *testing.T) {
	t.Parallel()
	for _, level := range []string{"normal", "low", "high", "reader", "critical", "blocker"} {
		t.Run(level, func(t *testing.T) {
			t.Parallel()
			body := fixCopy("", dealt("s1-1.w3", "s1-1", "friend.amy", "working", `,"priority":"`+level+`"`))
			assert.Equal(t, string(body), string(fixView(body)))
		})
	}
}
