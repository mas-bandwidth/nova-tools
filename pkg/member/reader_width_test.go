package member

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// queueJSONWidth is `nova-sprint queue --json` carrying the row's width.
func queueJSONWidth(t *testing.T, width int, cards ...queueCard) string {
	t.Helper()
	b, err := json.Marshal(queueOut{As: "reader-m", Epoch: 7, Width: width, Cards: cards})
	require.NoError(t, err)
	return string(b)
}

// A reader runs the width of its machine's fleet row, as a member runs its own
// (the owner, 2026-10-02: "why not just have as many readers as workers
// per-machine"): started with no --width it reads the width with its queue
// every tick, begins that many reads, and follows a change of the row.
func TestAReaderRunsTheWidthOfItsMachinesRow(t *testing.T) {
	t.Parallel()
	g := newRig(Config{As: "reader-m", Reader: true})
	var ps [3]Packet
	var cards []queueCard
	for i := range ps {
		id := "r" + string(rune('1'+i))
		ps[i] = Packet{Card: id, Kind: "read", As: "reader-m", Attempt: 1, Epoch: 7, Head: "h" + id}
		cards = append(cards, asked(id, &ps[i]))
	}
	g.s.set("queue", 0, queueJSONWidth(t, 2, cards...))
	_, err := g.tick(t)
	require.NoError(t, err)
	assert.Contains(t, g.out.String(), "width 0 -> 2 (the fleet row)\n", "the row's width is read with the queue")
	assert.Equal(t, []string{"r1", "r2"}, g.r.started(), "two reads at width 2")

	g.s.set("queue", 0, queueJSONWidth(t, 3, cards...))
	_, err = g.tick(t)
	require.NoError(t, err)
	assert.Contains(t, g.out.String(), "width 2 -> 3 (the fleet row)\n", "a raised row is followed")
	assert.Equal(t, []string{"r1", "r2", "r3"}, g.r.started(), "the third read at width 3")
}

// A worker whose queue carries no width (a reader named for no fleet row, a
// member with no row yet) takes nothing and says so once, naming the rule.
func TestAWorkerWithNoRowWidthSaysSoOnce(t *testing.T) {
	t.Parallel()
	g := newRig(Config{As: "reader-m", Reader: true})
	p := Packet{Card: "r1", Kind: "read", As: "reader-m", Attempt: 1, Epoch: 7, Head: "h1"}
	g.s.set("queue", 0, queueJSONWidth(t, 0, asked("r1", &p)))
	for i := 0; i < 3; i++ {
		_, err := g.tick(t)
		require.NoError(t, err)
	}
	assert.Empty(t, g.r.started(), "nothing starts at width 0")
	assert.Equal(t, 1, strings.Count(g.out.String(), "NOTE width 0: no fleet row names this worker's width"), "said once:\n%s", g.out.String())
	assert.Contains(t, g.out.String(), "reader-<m> runs at machine m's", "the note names the rule")
}
