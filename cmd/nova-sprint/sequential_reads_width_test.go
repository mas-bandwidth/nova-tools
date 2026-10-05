package main

import (
	"encoding/json"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Sequential reads at reader width (docs/SPEC-SPRINT.md section 6, the reads,
// sequential and by room; sprint.ReadsWanted and sprint.askPicks; the model is
// tla/ReadsByRoom.tla). The spending rules of 2026-10-04 ask a card's reads one
// at a time, the finder first on a rework; the readers' rules of 2026-10-03 ask
// each read of the reader with the most room under its machine's width. Both
// measured outcomes hold together: a pro card whose first read finds it broken
// lands on three reads, where the pair asked four, and no reader ever holds
// more reads than its machine's width, a read with no room waiting for it.

// readsRig is the readers' side of a sprint on the server rig: every read card
// each reader was asked, by primary, and the widths of their machines.
type readsRig struct {
	*serverRig
	width map[string]int
	asked map[string]map[string]bool // primary -> read card ids ever asked
}

// look reads every reader's queue once: each reader holds no more reads,
// asked and reading, than its machine's width, and no primary has two reads
// outstanding at once (the second read is asked only after the first came
// back ok). It records every read asked and returns the asked ones by reader.
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
		assert.Equal(r.t, 1, n, "%s has %d reads outstanding: its reads are asked one at a time", pr, n)
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

func TestSequentialReadsAtReaderWidthKeepThreeReadsPerLanding(t *testing.T) {
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

	// attempt 1: each card's first read alone, by room (the three fill both readers to
	// their widths); each reader finds its reads broken, and no second read is asked
	r.work(3)
	first := r.look()
	require.Len(t, first["reader-m1"], 2, "reader-m1, width 2, takes two first reads")
	require.Len(t, first["reader-m2"], 1, "reader-m2, width 1, takes one")
	finder := map[string]string{}
	for rd, ids := range first {
		for _, id := range ids {
			pr, _, _ := strings.Cut(id, ".r")
			finder[pr] = rd
		}
	}
	r.readAll(first, "--broken", "--finding", "internal/x.go:3: wrong; return the error")
	r.boss("nova-sprint tick")
	assert.Empty(t, r.look()["reader-m1"], "a broken first read costs no second read")
	cards := make([]string, 0, len(finder))
	for pr := range finder {
		cards = append(cards, pr)
	}
	sort.Strings(cards)
	require.Len(t, cards, 3)
	r.boss("nova-sprint rework " + strings.Join(cards, " ") + " --fix 'answer the finding'")

	// attempt 2: the first read of each card goes to the reader who found it broken
	// (each has room: its reads of attempt 1 are done), then the second to the
	// other reader once the first came back ok, while that reader has room
	r.work(3)
	checks := r.look()
	for rd, ids := range checks {
		for _, id := range ids {
			pr, _, _ := strings.Cut(id, ".r")
			assert.Equal(t, finder[pr], rd, "%s: the reader who found attempt 1 broken checks the fix", pr)
		}
	}
	for i := 0; i < 6; i++ {
		asked := r.look()
		r.readAll(asked, "--ok")
		r.boss("nova-sprint tick")
	}
	r.look()

	// the measure: reads asked per landing on a card that failed its first read once
	var w whereView
	require.NoError(t, json.Unmarshal([]byte(r.boss("nova-sprint where --json")), &w))
	for _, pr := range cards {
		assert.Len(t, r.asked[pr], 3, "%s: three reads to land, where the pair asked four: %v", pr, r.asked[pr])
	}
	assert.Equal(t, "3", w.Tables["work"]["s1"]["merging"], "every card accepted, in merging: %v", w.Tables["work"]["s1"])
}
