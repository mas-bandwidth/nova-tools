package sprintdash

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// twoReleases is where --json --cards over a sprint of two releases (the owner, 2026-10-05
// 7:05 PM: "Please make sure the sprint dashboard shows only the v1.0.0 work streams."):
// v1.0.0 is sprint-v1-release and promote-red-2026-10-05, v1.1.0 is jev and comfort, and
// ci carries no label. 8 cards are left of v1.0.0 and 251 of v1.1.0.
func twoReleases(v1Left int) []byte {
	v1Waiting := v1Left - 3 // the v1.0.0 cards left but the 3 under way
	return []byte(`{"at":"2026-10-05T23:05:00Z","landed":60,"all":` + itoa(60+v1Left+251+4) + `,"held":40,
"summary":"60/` + itoa(60+v1Left+251+4) + ` 18.0% held=40 -> ETA 2d7h","machine":"machine: running",
"tables":{
 "work":{
  "sprint-v1-release":{"waiting":"` + itoa(v1Waiting) + `","ready":"1","working":"1","review":"0","merging":"0","landed":"10","cost":"$5.00"},
  "promote-red-2026-10-05":{"waiting":"0","ready":"0","working":"0","review":"1","merging":"0","landed":"2","cost":"$1.00"},
  "jev":{"waiting":"200","ready":"10","working":"5","review":"0","merging":"0","landed":"30","cost":"$20.00"},
  "comfort":{"waiting":"30","ready":"4","working":"2","review":"0","merging":"0","landed":"8","cost":"$9.00"},
  "ci":{"waiting":"2","ready":"1","working":"1","review":"0","merging":"0","landed":"10","cost":"$3.00"}},
 "merge":{"sprint-v1-release":{"queued":"0"},"promote-red-2026-10-05":{"queued":"1"},"jev":{"queued":"2"},"comfort":{"queued":"0"},"ci":{"queued":"0"}},
 "fleet":{"m1":{"status":"up","width":"4"}}},
"streams":[
 {"Stream":"sprint-v1-release","Release":"v1.0.0","State":"moving"},
 {"Stream":"promote-red-2026-10-05","Release":"v1.0.0","State":"moving"},
 {"Stream":"jev","Release":"v1.1.0","State":"moving"},
 {"Stream":"comfort","Release":"v1.1.0","State":"moving"},
 {"Stream":"ci","State":"moving"}],
"releases":{"v1.0.0":` + itoa(v1Left) + `,"v1.1.0":251},
"stream_costs":{"sprint-v1-release":{"total_cost":"$5.00"},"jev":{"total_cost":"$20.00"},"ci":{"total_cost":"$3.00"}},
"stalled":["promote-red-2026-10-05","jev"],
"critical":[{"id":"v1-a","behind":9,"state":"working"},{"id":"jev-a","behind":7,"state":"ready"},{"id":"v1-c","behind":5,"state":"waiting"},{"id":"v1-b","behind":3,"state":"review"},{"id":"lost","behind":1,"state":"waiting"}],
"cards":[{"id":"v1-a.w1","primary":"v1-a","stream":"sprint-v1-release","member":"m1","state":"working","branch":"b1"},{"id":"jev-a.w1","primary":"jev-a","stream":"jev","member":"m1","state":"ready","branch":"b2"}],
"merging":[{"id":"x","stream":"jev","head":"abc"}],` + twoReleasesRows + `}`)
}

// twoReleasesRows is where --json --rows's rows of twoReleases' critical cards: where's
// critical names no stream, and v1-c (waiting) and v1-b (in review) are dealt to no one,
// so their rows alone place them. lost has no row.
const twoReleasesRows = `"rows":[{"id":"v1-a","stream":"sprint-v1-release","state":"working","score":1,"fields":{}},{"id":"v1-c","stream":"sprint-v1-release","state":"waiting","score":2,"fields":{}},{"id":"v1-b","stream":"promote-red-2026-10-05","state":"review","score":1,"fields":{}},{"id":"jev-a","stream":"jev","state":"ready","score":1,"fields":{}}]`

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }

// apiOf is /api/sprint's answer at path, decoded, its data decoded beside it.
func apiOf(t *testing.T, h http.Handler, path string) (snapshot, map[string]any) {
	t.Helper()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
	require.Equal(t, http.StatusOK, w.Code, path)
	var snap snapshot
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &snap), path)
	var data map[string]any
	require.NoError(t, json.Unmarshal(snap.Data, &data), path)
	return snap, data
}

func keysOf(m any) []string {
	var out []string
	for k := range m.(map[string]any) {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

func fieldOf(list any, key string) []string {
	var out []string
	for _, it := range list.([]any) {
		out = append(out, it.(map[string]any)[key].(string))
	}
	return out
}

// The page shows the streams of one release: by default the current one, the earliest
// with cards left (today v1.0.0); ?release= switches to another or to all. The summary
// line, the work table, the critical path and the costs follow the release; the fleet is
// the sprint's. Once v1.0.0 has nothing left, v1.1.0 is the current one.
func TestTheDashboardShowsOnlyTheCurrentReleasesStreams(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.next = func() ([]byte, error) { return twoReleases(8), nil }

	snap, d := apiOf(t, r.s, "/api/sprint")
	assert.Equal(t, "v1.0.0", snap.Release, "the default is the current release")
	assert.Equal(t, "v1.0.0", snap.Current)
	assert.Equal(t, []string{"v1.0.0", "v1.1.0"}, snap.Releases)
	v1 := []string{"promote-red-2026-10-05", "sprint-v1-release"}
	assert.Equal(t, v1, snap.ReleaseStreams)
	tables := d["tables"].(map[string]any)
	assert.Equal(t, v1, keysOf(tables["work"]), "the work table is the release's streams alone")
	assert.Equal(t, v1, keysOf(tables["merge"]))
	assert.Equal(t, []string{"m1"}, keysOf(tables["fleet"]), "the fleet is the sprint's")
	assert.Equal(t, []string{"sprint-v1-release", "promote-red-2026-10-05"}, fieldOf(d["streams"], "Stream"))
	assert.Equal(t, []string{"sprint-v1-release"}, keysOf(d["stream_costs"]), "the cost panel reads the release's costs")
	assert.Equal(t, []any{"promote-red-2026-10-05"}, d["stalled"])
	assert.Equal(t, []string{"v1-a", "v1-c", "v1-b"}, fieldOf(d["critical"], "id"), "the critical path is the release's cards, dealt or not")
	assert.Equal(t, []string{"sprint-v1-release", "sprint-v1-release", "promote-red-2026-10-05"}, fieldOf(d["critical"], "stream"))
	assert.NotContains(t, d, "rows", "the rows place the critical cards and are not served")
	assert.Equal(t, []string{"v1-a.w1"}, fieldOf(d["cards"], "id"))
	assert.Empty(t, d["merging"])
	// v1.0.0: 12 landed of 20 (8 left); the sprint's 263 left take 2d7h (55 h), so 8 take 1h41m
	assert.InDelta(t, 12, d["landed"], 0)
	assert.InDelta(t, 20, d["all"], 0)
	assert.Equal(t, "12/20 60.0% -> ETA 1h41m", d["summary"])
	assert.NotContains(t, d, "held", "held is the sprint's count, never a release's")

	snap, d = apiOf(t, r.s, "/api/sprint?release=v1.1.0")
	assert.Equal(t, "v1.1.0", snap.Release)
	assert.Equal(t, "v1.0.0", snap.Current)
	assert.Equal(t, []string{"comfort", "jev"}, keysOf(d["tables"].(map[string]any)["work"]))
	assert.Equal(t, []string{"jev"}, keysOf(d["stream_costs"]))
	assert.Equal(t, []string{"jev-a"}, fieldOf(d["critical"], "id"))
	assert.Equal(t, "38/289 13.1% -> ETA 2d5h", d["summary"])

	snap, d = apiOf(t, r.s, "/api/sprint?release=all")
	assert.Equal(t, "all", snap.Release)
	assert.Nil(t, snap.ReleaseStreams)
	assert.Len(t, keysOf(d["tables"].(map[string]any)["work"]), 5, "all is every stream, labelled or not")
	_, d = apiOf(t, r.s, "/api/sprint?release=all")
	assert.Equal(t, []string{"v1-a", "jev-a", "v1-c", "v1-b", "lost"}, fieldOf(d["critical"], "id"), "all is the whole critical path")
	assert.NotContains(t, d, "rows")
	var want map[string]any
	require.NoError(t, json.Unmarshal(twoReleases(8), &want))
	delete(want, "rows")
	delete(want, "critical")
	delete(d, "critical")
	assert.Equal(t, want, d, "all is the copy as where printed it, its critical cards placed")

	snap, _ = apiOf(t, r.s, "/api/sprint?release=v9.9.9")
	assert.Equal(t, "v1.0.0", snap.Release, "a release no stream carries shows the current one")

	// the pull routes' /api/sprint takes the same switch
	snap, _ = apiOf(t, r.s.Pull(), "/api/sprint?release=v1.1.0")
	assert.Equal(t, "v1.1.0", snap.Release)

	// the page carries the switch and asks for the release it shows, dark as ever
	html, js := string(file("index.html")), string(file("app.js"))
	assert.Contains(t, html, `<nav class="release" id="release" aria-label="release" hidden></nav>`)
	assert.Contains(t, js, `fetch("/api/sprint" + RELEASE_Q`)
	assert.Contains(t, js, `new EventSource("/events" + RELEASE_Q)`)
	assert.Contains(t, html, `<html lang="en" data-theme="dark">`)
	assert.NotContains(t, html, `id="theme"`)

	// v1.0.0 done: v1.1.0 is the current release
	r.advance(2 * r.s.Every)
	r.next = func() ([]byte, error) {
		return []byte(strings.Replace(string(twoReleases(3)), `"v1.0.0":3`, `"v1.0.0":0`, 1)), nil
	}
	snap, _ = apiOf(t, r.s, "/api/sprint")
	assert.Equal(t, "v1.1.0", snap.Current)
	assert.Equal(t, "v1.1.0", snap.Release)
}

// A cached fix is partitioned out of working by where. Release totals must count
// that column when they reconstruct the selected release from its Work rows.
func TestReleaseTotalIncludesCachedFix(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	body := strings.Replace(string(twoReleases(8)), `"working":"1","review":"0"`, `"working":"0","review":"0","fix":"1"`, 1)
	r.next = func() ([]byte, error) { return []byte(body), nil }

	_, d := apiOf(t, r.s, "/api/sprint?release=v1.0.0")
	assert.InDelta(t, 20, d["all"], 0, "a fix card remains in the selected release's total")
	assert.InDelta(t, 12, d["landed"], 0)
	assert.Equal(t, "12/20 60.0% -> ETA 1h41m", d["summary"])
	work := d["tables"].(map[string]any)["work"].(map[string]any)
	assert.Equal(t, "1", work["sprint-v1-release"].(map[string]any)["fix"])
}

func TestScopedReleaseLabelsSprintWidePriorityMarks(t *testing.T) {
	t.Parallel()
	nodePath, err := exec.LookPath("node")
	if err != nil {
		if os.Getenv("NOVA_CI") == "1" {
			require.NoError(t, err, "node is required for dashboard JS tests")
		}
		t.Skip("node is not installed")
	}
	shim, _, ok := strings.Cut(scrollShim, "// the viewer:")
	require.True(t, ok)
	input, err := json.Marshal(map[string]any{"appJS": string(file("app.js"))})
	require.NoError(t, err)
	const probe = `
const box = doc.getElementById('priority-marks');
context.priorityScope = 'v1.0.0';
context.renderPriorityMarks({priorities: {fix: ['other-release-card']}, reads_waiting: 1});
const scoped = [box._scopeLabel.textContent, box._scopeLabel.hidden, box.children.length];
context.priorityScope = 'all';
context.renderPriorityMarks({priorities: {fix: ['other-release-card']}, reads_waiting: 1});
process.stdout.write(JSON.stringify({scoped, allHidden: box._scopeLabel.hidden}));
`
	cmd := exec.Command(nodePath, "-e", shim+probe)
	cmd.Stdin = bytes.NewReader(input)
	out, err := cmd.Output()
	require.NoError(t, err)
	assert.JSONEq(t, `{"scoped":["Priorities and waiting reads across all releases",false,2],"allHidden":true}`, string(out))
}

// A sprint whose streams carry no release is shown whole, as where printed it.
func TestTheDashboardShowsAnUnlabelledSprintWhole(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	body := strings.Replace(strings.Replace(string(twoReleases(8)), `"Release":"v1.0.0",`, "", -1), `"Release":"v1.1.0",`, "", -1)
	body = strings.Replace(body, `,`+twoReleasesRows, "", 1)
	r.next = func() ([]byte, error) { return []byte(body), nil }
	snap, _ := apiOf(t, r.s, "/api/sprint")
	assert.Equal(t, "all", snap.Release)
	assert.Empty(t, snap.Releases)
	assert.JSONEq(t, body, string(snap.Data))
}

// Releases sort by version, numbers as numbers.
func TestReleasesSortByVersion(t *testing.T) {
	t.Parallel()
	got := []string{"v1.10.0", "next", "v1.2.0", "v1.1.0", "v2.0.0"}
	slices.SortFunc(got, compareRelease)
	assert.Equal(t, []string{"v1.1.0", "v1.2.0", "v1.10.0", "v2.0.0", "next"}, got)
}
