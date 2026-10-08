package sprintdash

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// barShimTail renders the shipped app.js against one snapshot (scrollShim's DOM, cut before its
// own driver) and reports, for the Fleet and Friends tables, what the page decided about each
// row's bar: the --track-w the table carries and each row's cell count and cell grid.
const barShimTail = `
(async () => {
  nextBody = { build: 'b1', data: input.snap }; await tick();
  const out = {};
  for (const id of ['fleet', 'friends']) {
    const box = doc.getElementById(id);
    out[id] = {
      trackW: box.style.getPropertyValue('--track-w'),
      rows: box.children.filter(r => r._classes.includes('row') && !r._classes.includes('head') && !r._classes.includes('total')).map(r => {
        const c = r.children.find(x => x._classes.includes('cells'));
        return { name: r.children[0].textContent, cells: c.children.length, cols: c.style.gridTemplateColumns || '' };
      }),
    };
  }
  process.stdout.write(JSON.stringify(out));
})();
`

type barRow struct {
	Name  string `json:"name"`
	Cells int    `json:"cells"`
	Cols  string `json:"cols"`
}

type barTable struct {
	TrackW string   `json:"trackW"`
	Rows   []barRow `json:"rows"`
}

// remOf reads "1.5rem", ".5rem" or "0" as rem.
func remOf(t *testing.T, s string) float64 {
	t.Helper()
	s = strings.TrimSpace(s)
	if s == "0" || s == "" {
		return 0
	}
	require.True(t, strings.HasSuffix(s, "rem"), "%q is not a rem length", s)
	v, err := strconv.ParseFloat(strings.TrimSuffix(s, "rem"), 64)
	require.NoError(t, err, s)
	return v
}

// cssDecl is the value of prop in the rule whose selector list is exactly sel ("" when the rule
// or the property is absent).
func cssDecl(t *testing.T, css, sel, prop string) string {
	t.Helper()
	rule := regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(sel) + `\s*\{([^}]*)\}`)
	prop = regexp.QuoteMeta(prop)
	for _, m := range rule.FindAllStringSubmatch(css, -1) { // a selector list can have several rules
		if d := regexp.MustCompile(`(?:^|;|\s)` + prop + `:\s*([^;]*)`).FindStringSubmatch(m[1]); d != nil {
			return strings.TrimSpace(d[1])
		}
	}
	return ""
}

// topLevel splits a grid-template-columns value on spaces outside parentheses.
func topLevel(s string) []string {
	var out []string
	depth, start := 0, 0
	for i, r := range s {
		switch {
		case r == '(':
			depth++
		case r == ')':
			depth--
		case r == ' ' && depth == 0:
			if i > start {
				out = append(out, s[start:i])
			}
			start = i + 1
		}
	}
	return append(out, s[start:])
}

// trackLen evaluates a track that is a rem length or calc(a + b + ...) of rem lengths and
// var(--track-w, fallback), with --track-w as the page set it.
func trackLen(t *testing.T, track, trackW string) float64 {
	t.Helper()
	track = regexp.MustCompile(`var\(--track-w,\s*[^)]*\)`).ReplaceAllString(track, trackW)
	if strings.HasPrefix(track, "calc(") {
		sum := 0.0
		for _, term := range strings.Split(strings.TrimSuffix(strings.TrimPrefix(track, "calc("), ")"), "+") {
			sum += remOf(t, term)
		}
		return sum
	}
	return remOf(t, track)
}

// TestTheBarColumnHasEqualInsets: in the Fleet and Friends tables the bar's column is the
// widest bar plus equal insets, so the first cell sits as far from the column's left edge as
// the widest row's last cell sits from its right edge (Glenn, 2026-10-05: "the spacing on the
// left of the segmented bar is the same as the right exactly"). No browser is available, so the
// page is rendered by app.js on the shim and the box model is resolved from index.html's own
// rules, in rem (16 px each).
func TestTheLiveTrackKeepsItsSpanAndAlignment(t *testing.T) {
	t.Parallel()
	nodePath, err := exec.LookPath("node")
	if err != nil {
		if os.Getenv("NOVA_CI") == "1" {
			require.NoError(t, err, "node is required for the dashboard JS behavioural test")
		}
		t.Skip("node is not installed on this machine; the dashboard JS behavioural test needs it")
	}

	var snap map[string]any
	require.NoError(t, json.Unmarshal(fixture(t), &snap))
	tables := snap["tables"].(map[string]any)
	fleet, friends := map[string]any{}, map[string]any{}
	for _, w := range []int{2, 8, 16, 24, 0} {
		name := fmt.Sprintf("w%d", w)
		if w == 0 {
			name = "nolanes"
		}
		m := func() map[string]any {
			return map[string]any{"status": "up", "width": strconv.Itoa(w), "working": strconv.Itoa(w / 2), "ready": "3", "done": "10", "ok": "9", "load": "40"}
		}
		fleet[name], friends[name] = m(), m()
	}
	tables["fleet"], tables["friends"] = fleet, friends

	in, err := json.Marshal(map[string]any{"appJS": string(file("app.js")), "snap": snap})
	require.NoError(t, err)
	cmd := exec.Command(nodePath, "-e", strings.SplitN(scrollShim, "let draws = 0;", 2)[0]+barShimTail)
	cmd.Stdin = bytes.NewReader(in)
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &outBuf, &errBuf
	require.NoError(t, cmd.Run(), "node runner failed: %s", errBuf.String())
	var res map[string]barTable
	require.NoError(t, json.Unmarshal(outBuf.Bytes(), &res), outBuf.String())

	css := string(file("index.html"))
	gap := remOf(t, cssDecl(t, css, ".fleet .cells, .friends .cells", "gap"))
	require.NotZero(t, gap, "the Fleet and Friends cells' gap")
	cellsPad := func(side string) float64 { // padding-<side>, else the `padding: v h` / `padding: all` shorthand
		const sel = ".fleet .cells, .friends .cells"
		if v := cssDecl(t, css, sel, "padding-"+side); v != "" {
			return remOf(t, v)
		}
		f := strings.Fields(cssDecl(t, css, sel, "padding"))
		switch len(f) {
		case 0:
			return 0
		case 1:
			return remOf(t, f[0])
		default:
			return remOf(t, f[1])
		}
	}
	barMargin := func(side string) float64 {
		return remOf(t, cssDecl(t, css, ".fleet .row > :nth-child(4), .friends .row > :nth-child(4)", "margin-"+side))
	}
	countPad := remOf(t, cssDecl(t, css, ".fleet .row > :nth-child(5), .friends .row > :nth-child(5)", "padding-left"))

	for _, id := range []string{"fleet", "friends"} {
		tb := res[id]
		require.Len(t, tb.Rows, 5, id)
		widest := 0
		for _, r := range tb.Rows {
			widest = max(widest, r.Cells)
		}
		require.Equal(t, 24, widest, "%s: the widest row", id)

		// the bar column: the fourth track of the table's row
		tracks := topLevel(cssDecl(t, css, ".fleet .row, .friends .row", "grid-template-columns"))
		require.GreaterOrEqual(t, len(tracks), 5, "%s: the row's grid", id)
		barTrack := strings.TrimSuffix(strings.TrimPrefix(tracks[3], "minmax(0, "), ")")
		colW := trackLen(t, barTrack, tb.TrackW)

		// every row's cells sit on the widest row's grid, so each starts at the same x
		// (a row with no lanes has no cells and nothing to place)
		var cellW float64
		for _, r := range tb.Rows {
			m := regexp.MustCompile(`^repeat\((\d+), minmax\(0, ([\d.]+rem)\)\)$`).FindStringSubmatch(r.Cols)
			require.NotNil(t, m, "%s %s: cell grid %q", id, r.Name, r.Cols)
			assert.Equal(t, "24", m[1], "%s %s: every row's grid is the widest row's, so cells stay left-aligned at one x", id, r.Name)
			cellW = remOf(t, m[2])
		}
		cellsW := 24*cellW + 23*gap

		left := barMargin("left") + cellsPad("left")
		right := colW - (left + cellsW)
		assert.InDelta(t, 0.0, right, 1.0/16/2, "%s: first cell %.4frem from the bar column's left edge, widest row's last cell %.4frem from its right edge (width 24 of %d rows)", id, left, right, len(tb.Rows))
		assert.InDelta(t, 30.75, cellsW, 1.0/16/2, "the live track spans sixteen original cells at any width")
		assert.Greater(t, left, 0.0, "%s: the insets are real, not zero", id)

		// The live count box begins 4rem and ends 13.5rem after the final cell.
		// Its fixed 10rem track keeps rows and the longer total aligned.
		gapRem := remOf(t, cssDecl(t, css, ".fleet .row, .friends .row", "--gap"))
		countW := trackLen(t, tracks[4], tb.TrackW)
		fromFirstCell := (colW - left) + gapRem + countPad
		assert.InDelta(t, cellsW+4.0, fromFirstCell, 1.0/16/2, "%s: the count's content began elsewhere", id)
		assert.InDelta(t, cellsW+13.5, (colW-left)+gapRem+countW, 1.0/16/2, "%s: the count's column ended elsewhere", id)

		// the head's "working" label stays where it was, .5rem into the column
		assert.InDelta(t, 0.5, remOf(t, cssDecl(t, css, ".fleet .row > :nth-child(4), .friends .row > :nth-child(4)", "margin-left")), 1.0/16/2, "%s: the head label moved", id)
	}
}
