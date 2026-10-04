package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// The deal and the take each say which table they moved in: the deal moves
// the primary in the work table and puts its work card in the member's ready
// queue of the fleet table; the take moves that card ready -> working in the
// fleet table.
func TestTheDealAndTheTakeSayTheirTable(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("add --stream s1 --count 1")
	ta.ok("start")
	assert.Contains(t, ta.ok("tick"), "MOVED deal: s1-1 work ready -> working card=s1-1.w1 member=m1 (fleet ready)\n")
	assert.Contains(t, ta.ok("take --as m1"), "MOVED s1-1.w1 fleet ready -> working member=m1 gen=1\n")
}

// Every stats table has a legend line under it saying what its columns count,
// so a member's 2 cards and its route's 7 takes (reads and a second take of a
// card are takes too) read without the code.
func TestEveryStatsTableHasALegend(t *testing.T) {
	t.Parallel()
	out := statsSprint(t).ok("stats")
	for _, table := range []string{"stages", "work", "reads", "routes"} {
		assert.Contains(t, out, "\n"+table+": per ", "the %s legend", table)
	}
	assert.Contains(t, out, "takes is every take on it, work and read alike, each take of a card again counted")
	assert.Contains(t, out, "cards is its work cards (one per attempt, counted to the member it was last dealt to)")
	for _, l := range strings.Split(out, "\n") {
		if strings.HasSuffix(strings.SplitN(l, ":", 2)[0], "s") && strings.Contains(l, ": per ") {
			assert.NotContains(t, l, " | ", "a legend is not read as a row: %s", l)
		}
	}
}

// card <id> tells every reader's finding of every attempt, two readers' same
// words included; words copied onto a later line (a finding onto its rework)
// are still told once.
func TestTheStoryTellsEveryReadersFinding(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("add --stream s1 --count 1 --brief-file " + proBriefFile(t))
	ta.ok("start")
	ta.ok("tick")
	ta.ok("take --as m1")
	ta.ok("finish --as m1 s1-1.w1@1 --report one")
	ta.ok("tick")
	ta.ok("read --as reader-a --broken --finding 'line 3: the empty case is not handled'")
	ta.ok("read --as reader-b --ok --finding 'looks fine'")
	ta.ok("rework s1-1")
	ta.ok("tick")
	ta.ok("take --as m1")
	ta.ok("finish --as m1 s1-1.w2@1 --report two")
	ta.ok("tick")
	ta.ok("read --as reader-a --ok --finding 'looks fine'")
	ta.ok("read --as reader-b --ok --finding 'looks fine'")
	story := ta.ok("card s1-1")
	_, texts, _ := strings.Cut(story, "\nreports and findings:\n")
	for _, want := range []string{
		"attempt 1, finding by reader-a (broken):\n",
		"attempt 1, finding by reader-b (ok):\n",
		"attempt 2, finding by reader-a (ok):\n",
		"attempt 2, finding by reader-b (ok):\n",
	} {
		assert.Contains(t, texts, want)
	}
	assert.Equal(t, 1, strings.Count(texts, "attempt 2, finding by reader-a"), "one reader's finding is told once")
}
