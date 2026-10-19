package member

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/cardcost"
)

// A launch the native step refused before any child started is a launch refusal, never the
// card's failed work: the member finishes it with the staging kind and the native refusal
// line, so the sprint deals the card to another member and opens no judgment on it
// (tla/CardContract.tla, StageRefused).
func TestALaunchRefusedBeforeTheChildIsNoFailedAttempt(t *testing.T) {
	t.Parallel()
	g := newWidthRig(2, Config{As: "m"})
	p := pk("c1")
	p.Gen = 2
	g.s.set("queue", 0, queueJSON(t, 7, working("c1", 2, &p)))
	_, err := g.tick(t)
	require.NoError(t, err)
	refusal := "no wall: nova-sandbox is on no PATH entry and --sandbox names no file"
	g.r.child("c1").end(Result{Report: "no child ran", End: EndStaging, Staging: refusal})
	g.s.reset()
	_, err = g.tick(t)
	require.NoError(t, err)
	require.Equal(t, []string{"finish --as m c1@2 --report staging refused: " + refusal + "; no child ran --failed --epoch 7"}, g.s.lines("finish"), "a launch refusal is the member's staging kind, never the card's failed work")
}

// Whatever finish a launch refusal sends carries usage pkg/cardcost parses: an empty usage
// record, never invalid JSON, so the sprint's receipt contract never refuses the finish.
func TestALaunchRefusedFinishCarriesValidUsage(t *testing.T) {
	t.Parallel()
	g := newWidthRig(2, Config{As: "m"})
	p := pk("c1")
	p.Gen = 2
	g.s.set("queue", 0, queueJSON(t, 7, working("c1", 2, &p)))
	_, err := g.tick(t)
	require.NoError(t, err)
	empty := cardcost.NoUsage().String()
	require.NotEmpty(t, empty, "the empty usage record is a line, never an empty string")
	g.r.child("c1").end(Result{Report: "no child ran", End: EndStaging, Staging: "no wall: nova-sandbox is on no PATH entry", Usage: empty})
	g.s.reset()
	_, err = g.tick(t)
	require.NoError(t, err)
	lines := g.s.lines("finish")
	require.Len(t, lines, 1)
	require.Contains(t, lines[0], "--usage "+empty)
	seg := strings.Fields(lines[0][strings.Index(lines[0], "--usage ")+len("--usage "):])[0]
	_, perr := cardcost.ParseUsageChecked(seg)
	require.NoError(t, perr, "the finish's usage parses: %s", seg)
}
