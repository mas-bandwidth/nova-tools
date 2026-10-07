package main

import (
	"encoding/json"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Reads at reader width (docs/SPEC-SPRINT.md section 6, the reads, by room;
// sprint.ReadsWanted and sprint.askPicks; the model is tla/ReadsByRoom.tla). Under
// the interim rule of 2026-10-06 a card's reads are asked together, every read it
// needs at once (the owner, 6:02 PM ET: "send out multiple consumer cards in ||");
// the readers' rules of 2026-10-03 ask each read of the reader with the most room
// under its machine's width. Both hold together: a pro card whose two reads at its
// first attempt find it broken lands on four reads, two an attempt, and no reader
// ever holds more reads than its machine's width, a card's reads waiting together
// for room.

// readsRig is the readers' side of a sprint on the server rig: every read card
// each reader was asked, by primary, and the widths of their machines.
type readsRig struct {
	*serverRig
	width map[string]int
	asked map[string]map[string]bool // primary -> read card ids ever asked
}

// look reads every reader's queue once: each reader holds no more reads,
// asked and reading, than its machine's width, and every primary with a read
// outstanding has both its reads outstanding (a card's reads are asked
// together). It records every read asked and returns the asked ones by reader.
func (r *readsRig) look() map[string][]string {
	r.t.Helper()
	out := map[string][]string{}
	outstanding := map[string]int{}
	for rd, w := range r.width {
		q := r.queue(rd)
		assert.LessOrEqual(r.t, len(q["asked"])+len(q["reading"]), w, "%s holds %v: over its machine's width %d", rd, q, w)
		for _, id := range append(append([]string{}, q["asked"]...), q["reading"]...) {
			pr, _, _ := strings.Cut(id, ".r")
			outstanding[pr]++
			if r.asked[pr] == nil {
				r.asked[pr] = map[string]bool{}
			}
			r.asked[pr][id] = true
		}
		out[rd] = q["asked"]
	}
	for pr, n := range outstanding {
		assert.Equal(r.t, 2, n, "%s has %d reads outstanding: its reads are asked together", pr, n)
	}
	return out
}

// readAll has each reader begin and give its verdict on every read it was asked.
func (r *readsRig) readAll(asked map[string][]string, verdict ...string) {
	r.t.Helper()
	for rd, ids := range asked {
		if len(ids) == 0 {
			continue
		}
		res := r.one(append(append([]string{"read", "--as", rd, "--begin"}, ids...), "--epoch", "0")...)
		require.Equal(r.t, 0, res.Code, res.Stderr)
		res = r.one(append(append(append([]string{"read", "--as", rd}, verdict...), ids...), "--epoch", "0")...)
		require.Equal(r.t, 0, res.Code, res.Stderr)
	}
}

// work takes and finishes every card dealt, ticking between, until n are finished.
func (r *readsRig) work(n int) {
	r.t.Helper()
	done := 0
	for i := 0; i < 8 && done < n; i++ {
		for _, m := range []string{"m1", "m2"} {
			for _, c := range taken(r.t, r.one("take", "--as", m, "--limit", "4", "--epoch", "0", "--json")) {
				res := r.one("finish", "--as", m, c, "--epoch", "0", "--report", "done", "--head", "0123456789abcdef0123456789abcdef01234567")
				require.Equal(r.t, 0, res.Code, res.Stderr)
				done++
			}
		}
		r.boss("nova-sprint tick")
	}
	require.Equal(r.t, n, done, "every card's attempt finished")
}

func TestReadsTogetherAtReaderWidthTakeFourReadsPerLanding(t *testing.T) {
	t.Parallel()
	r := &readsRig{
		serverRig: newServerRig(t,
			"nova-sprint init --readers reader-m1,reader-m2 --members m1:2,m2:1",
			"nova-sprint add --stream s1 --count 3 --brief-file "+proBriefFile(t), // pro: two reads each
			"nova-sprint start",
			"nova-sprint tick",
			"nova-sprint tick",
		),
		width: map[string]int{"reader-m1": 2, "reader-m2": 1}, // reader-<m> runs at m's width
		asked: map[string]map[string]bool{},
	}
	r.queue("reader-m1") // a reader's queue is its beat: a reader that never beat is asked nothing
	r.queue("reader-m2")

	// readRound reads every read asked with the verdict, a tick between, until no read is
	// asked: reader-m2's width of 1 lets one card's pair out at a time, the next card's
	// pair waiting for its room
	readRound := func(verdict ...string) {
		t.Helper()
		for i := 0; i < 8; i++ {
			asked := r.look()
			if len(asked["reader-m1"])+len(asked["reader-m2"]) == 0 {
				return
			}
			require.Len(t, asked["reader-m2"], 1, "reader-m2, width 1, holds one card's read")
			r.readAll(asked, verdict...)
			r.boss("nova-sprint tick")
		}
		t.Fatalf("reads still asked after eight rounds")
	}

	// attempt 1: each card's two reads asked together, by room; both find it broken
	r.work(3)
	readRound("--broken", "--finding", "internal/x.go:3: wrong; return the error")
	cards := make([]string, 0, len(r.asked))
	for pr := range r.asked {
		cards = append(cards, pr)
	}
	sort.Strings(cards)
	require.Len(t, cards, 3)
	for _, pr := range cards {
		require.Len(t, r.asked[pr], 2, "%s: both reads of attempt 1 asked together: %v", pr, r.asked[pr])
	}
	r.boss("nova-sprint rework " + strings.Join(cards, " ") + " --fix 'answer the finding'")

	// attempt 2: each card's two reads asked together again, by room; both ok
	r.work(3)
	readRound("--ok")
	r.look()

	// the measure: reads asked per landing on a card whose first attempt was found broken
	var w whereView
	require.NoError(t, json.Unmarshal([]byte(r.boss("nova-sprint where --json")), &w))
	for _, pr := range cards {
		assert.Len(t, r.asked[pr], 4, "%s: four reads to land, two an attempt, asked together: %v", pr, r.asked[pr])
	}
	assert.Equal(t, "3", cellText(w.Tables["work"]["s1"]["merging"]), "every card accepted, in merging: %v", w.Tables["work"]["s1"])
}
