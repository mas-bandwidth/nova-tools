package main

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/member"
	"github.com/mas-bandwidth/nova-tools/internal/sprintwire"
)

// readRig is a sprint whose two readers, reader-m1 (named for the member m1,
// width 2) and reader-x (named for no row), each hold three asked reads.
func readRig(t *testing.T) *serverRig {
	t.Helper()
	r := newServerRig(t,
		"nova-sprint init --readers reader-m1,reader-x --members m1:2",
		"nova-sprint add --stream s1 --count 3 --brief-file "+proBriefFile(t), // pro: read by both readers
		"nova-sprint start",
		"nova-sprint tick",
		"nova-sprint tick",
	)
	cards := taken(t, r.one("take", "--as", "m1", "--limit", "2", "--epoch", "0", "--json"))
	require.Len(t, cards, 2)
	for _, c := range cards {
		res := r.one("finish", "--as", "m1", c, "--epoch", "0", "--report", "done", "--head", "0123456789abcdef0123456789abcdef01234567")
		require.Equal(t, 0, res.Code, res.Stderr)
	}
	r.boss("nova-sprint tick")
	cards = taken(t, r.one("take", "--as", "m1", "--limit", "1", "--epoch", "0", "--json"))
	require.Len(t, cards, 1)
	res := r.one("finish", "--as", "m1", cards[0], "--epoch", "0", "--report", "done", "--head", "0123456789abcdef0123456789abcdef01234567")
	require.Equal(t, 0, res.Code, res.Stderr)
	r.queue("reader-m1") // a reader's queue is its beat: a reader that never beat is asked nothing
	r.queue("reader-x")
	r.boss("nova-sprint tick")
	r.boss("nova-sprint tick")
	require.Len(t, r.queue("reader-m1")["asked"], 3)
	require.Len(t, r.queue("reader-x")["asked"], 3)
	return r
}

// queueWidth is the width a worker's queue answer carries: 0 when none.
func (r *serverRig) queueWidth(as string) int {
	r.t.Helper()
	res := r.one("queue", "--as", as, "--json")
	require.Equal(r.t, 0, res.Code, res.Stderr)
	var q struct{ Width int }
	require.NoError(r.t, json.Unmarshal([]byte(res.Stdout), &q), res.Stdout)
	return q.Width
}

// A reader's queue carries the width of its machine's fleet row (reader-<m> is
// the reader on m), as a member's carries its own; a reader named for no row
// carries none (the owner, 2026-10-02: "why not just have as many readers as
// workers per-machine").
func TestAReadersQueueCarriesItsMachinesWidth(t *testing.T) {
	t.Parallel()
	r := readRig(t)
	assert.Equal(t, 2, r.queueWidth("reader-m1"), "the fleet row m1 is width 2")
	assert.Equal(t, 0, r.queueWidth("reader-x"), "no fleet row is named x")
	r.boss("nova-sprint fleet up m1 --width 1")
	assert.Equal(t, 1, r.queueWidth("reader-m1"), "the row changed, the reader's width with it")
	assert.Equal(t, 1, r.queueWidth("m1"), "the member's own is unchanged in shape")
}

// A reader started with no --width begins reads to its machine's row width,
// read with its queue every tick, and a row lowered mid-run begins nothing
// new until the running fall under it.
func TestAReaderRunsTheWidthOfItsMachinesFleetRow(t *testing.T) {
	t.Parallel()
	r := readRig(t)
	rn := &twinRunner{children: map[string]*twinChild{}}
	var log bytes.Buffer
	m := member.New(member.Config{As: "reader-m1", Reader: true}, &sprintwire.Worker{Send: r.send}, rn, nil, &log)
	tick := func() {
		t.Helper()
		_, err := m.Tick(time.Unix(0, 0))
		require.NoError(t, err, log.String())
	}
	tick() // the first pass learns the width (its packets were asked for at width 0)
	require.Contains(t, log.String(), "width 0 -> 2 (the fleet row)", "the row's width is read with the queue")
	tick()
	require.Len(t, rn.packets, 2, "two reads at width 2: %s", log.String())

	r.boss("nova-sprint fleet up m1 --width 1")
	tick()
	assert.Contains(t, log.String(), "width 2 -> 1 (the fleet row)")
	assert.Len(t, rn.packets, 2, "nothing new while the running fill the lowered width: %s", log.String())
}
