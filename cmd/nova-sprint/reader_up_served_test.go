package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// reader up releases a hold; it adds no capacity. A reader no process serves
// (no beat within the bound: reads are served by a bud's reader loop or a
// friend's harness, never by the row) goes straight back to away or down on
// the next tick, so the verb refuses it, names the beat that would serve it,
// and writes nothing for any of the names (docs/SPEC-SPRINT.md section 6).
func TestReaderUpRefusesAReaderNobodyServes(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.quiet = map[string]bool{"reader-b": true, "reader-c": true}
	ta.ok("init --readers reader-a,reader-b,reader-c --members m1")
	ta.ok("reader away reader-a reader-b")
	code, out, errs := ta.do("reader up reader-a reader-b")
	assert.Equal(t, 1, code, "%s%s", out, errs)
	assert.Contains(t, errs, "reader-b")
	assert.NotContains(t, errs, "reader-a is", "reader-a beats: only the reader nobody serves is named")
	assert.Contains(t, errs, "nova-sprint queue --as reader-b", "the refusal names the beat that would serve it")
	assert.Contains(t, errs, "nothing was changed")
	assert.Equal(t, "held", ta.readerState("reader-a"), "all or none: reader-a's hold stands")
	// the reader's loop beats, and the same verb releases it
	ta.ok("queue --as reader-b")
	ta.ok("reader up reader-a reader-b")
	assert.Equal(t, "up", ta.readerState("reader-a"))
	assert.Equal(t, "up", ta.readerState("reader-b"))
}

// A reader row says whether a process serves it, from its beat alone: where
// --json carries served on each row, and the text row counts them.
func TestAReaderRowShowsServedFromItsBeat(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.quiet = map[string]bool{"reader-c": true}
	ta.ok("init --readers reader-a,reader-b,reader-c --members m1")
	ta.beat()
	var v struct {
		Tables map[string]map[string]map[string]string
	}
	ta.json("where", &v)
	assert.Equal(t, "served", v.Tables["readers"]["reader-a"]["served"])
	assert.Equal(t, "unserved", v.Tables["readers"]["reader-c"]["served"], "no beat: nobody serves the row")
	ta.ok("reader away reader-a")
	ta.json("where", &v)
	assert.Equal(t, "served", v.Tables["readers"]["reader-a"]["served"], "held away is the coordinator's; the beat still serves it")
}

// fewer than two readers up is one open judgment, updated in place as the
// readers change, never a new log line each tick.
func TestFewReadersIsOneJudgmentLineWhateverTheTicks(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.quiet = map[string]bool{"reader-b": true, "reader-c": true}
	ta.ok("init --readers reader-a,reader-b,reader-c --members m1")
	ta.inReview(2)
	ta.ok("start")
	for i := 0; i < 12; i++ {
		ta.a.sleep(2 * 1e9)
		ta.ok("tick")
	}
	log := ta.ok("log")
	n := 0
	for _, l := range strings.Split(log, "\n") {
		if strings.Contains(l, "fewer than two readers up") {
			n++
		}
	}
	require.Equal(t, 1, n, "one judgment line, not one per tick:\n%s", log)
}
