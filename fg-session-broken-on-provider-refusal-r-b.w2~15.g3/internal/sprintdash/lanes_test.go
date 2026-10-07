package sprintdash

import (
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// lanesRowCSS is the page's rule that lays the lanes panel's rows out, found in its style.
var lanesRowCSS = regexp.MustCompile(`(?s)\.lanes \.row \{[^}]*grid-template-columns:\s*([^;]+);`)

// gridTracks is how many tracks a grid-template-columns value names: its top-level tracks,
// which whitespace separates; a track written minmax(9rem, 1fr) is one, comma and all.
func gridTracks(v string) int {
	n, depth, in := 0, 0, false
	for _, r := range v {
		switch {
		case r == '(':
			depth, in = depth+1, true
		case r == ')':
			depth--
		case depth == 0 && (r == ' ' || r == '\n' || r == '\t'):
			in = false
		case depth == 0 && !in:
			n, in = n+1, true
		}
	}
	return n
}

// TestPageShowsPerMachineLanes pins the lanes panel (docs/SPEC-SPRINT-DASHBOARD.md,
// "Lanes"): the `lanes` array `nova-sprint where --json --cards` prints (verb-lane-take-give)
// is read, the page carries a Lanes panel, and app.js draws each machine's holders and
// waiters from it. The fixture is a where --json --cards body with lanes.
func TestPageShowsPerMachineLanes(t *testing.T) {
	t.Parallel()

	// The data path: the fixture's lanes array is read as the machine's row.
	c := copyOf(t, fixture(t))
	require.Len(t, c.Lanes, 1, "the fixture carries one machine's lanes")
	row := c.Lanes[0]
	assert.Equal(t, LaneRow{Kind: "go", Machine: "bench-a", Width: 1, Held: []string{"amy"}, Waiting: []string{"bob"}}, row,
		"the machine's lane: its kind, width, holder and waiter")

	// The page path: a Lanes panel with its title.
	doc := parsePage(t, file("index.html"))
	panel := doc.one(t, "a lanes panel", byClass("lanes"))
	assert.Equal(t, "Lanes", textOf(panel.one(t, "the lanes panel's title", func(n *node) bool { return n.name == "h2" })))
	assert.Contains(t, string(file("index.html")), `id="lanes"`)

	// The lanes rows are a table: the page's CSS gives the panel's row one track a column, so
	// its five columns sit beside each other and never stack into one column.
	m := lanesRowCSS.FindStringSubmatch(string(file("index.html")))
	require.NotNil(t, m, "the page's CSS gives .lanes .row no grid tracks, so its columns stack")
	assert.Equal(t, 5, gridTracks(m[1]), "the lanes row's grid tracks, one a column (machine, kind, width, held, waiting)")

	// The render path: app.js draws the rows from d.lanes, holders and waiters.
	js := string(file("app.js"))
	assert.Contains(t, js, "d.lanes", "app.js reads the lanes array")
	assert.Contains(t, js, "function renderLanes", "app.js has the lanes renderer")
	assert.Contains(t, js, "renderLanes(d)", "render() calls the lanes renderer")
	assert.Contains(t, js, "held", "app.js names the holders")
	assert.Contains(t, js, "waiting", "app.js names the waiters")

	// The spec carries the section the page draws.
	sp := readSpec(t)
	assert.Contains(t, sp.section(t, "Lanes"), "lanes", "the spec's Lanes section names the lanes")
}
