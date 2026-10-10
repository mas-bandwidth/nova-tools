package main

import (
	"cmp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// The work table's cost column (the owner, 2026-10-01: "can you please add a final
// column to the work stream table, which is "cost". This is the sum of each landed
// card's total cost for that work stream, and then a total at the bottom."): a
// stream's landed cards' total, each consumer's actual cost else its predicted one,
// "$" and four places, "-" when none was priced, and the footer the sum over the
// streams. internal/sprint/cost.go and steps_merge.go; pkg/ntable moneyFold.

// landStream plays the stream's n cards to landed: dealt, taken and finished on m1
// (each with its usage, "" for none), read ok by the readers asked, reader-a and reader-b
// (each with its usage), accepted, merged and drained by the tick.
func (ta *testApp) landStream(stream string, work []string, readA, readB []string, mergeOp ...string) {
	ta.t.Helper()
	ta.ok("tick")
	for i := range work {
		id := stream + "-" + strconv.Itoa(i+1)
		ta.ok("take --as m1 " + id + ".w1@1")
		line := "finish --as m1 " + id + ".w1@1"
		if work[i] != "" {
			line += " --usage '" + work[i] + "'"
		}
		ta.ok(line)
	}
	// a card's reads are asked together: every read it needs in one ask (a pro card's two, a
	// flash card's one), reader-a's read with readA's usage and reader-b's with readB's
	ta.ok("ask")
	for i := range work {
		id := stream + "-" + strconv.Itoa(i+1)
		require.True(ta.t, slices.Contains(ta.askedOf("reader-a"), id+".r1.reader-a") || slices.Contains(ta.askedOf("reader-b"), id+".r1.reader-b"), "%s is asked its reads", id)
	}
	for who, usages := range map[string][]string{"reader-a": readA, "reader-b": readB} {
		for i := range work {
			id := stream + "-" + strconv.Itoa(i+1)
			if !slices.Contains(ta.askedOf(who), id+".r1."+who) {
				continue
			}
			// a read with no cost is a subscription reader's: its tokens, no dollar (a
			// routed read with no usage is refused, sprint.ReadUsageMissing)
			usage := cmp.Or(usages[i], "input=1 "+sprint.UsageSubscription)
			ta.ok("read --as " + who + " --ok " + id + ".r1." + who + " --usage '" + usage + "'")
		}
	}
	ta.ok("accept --stream " + stream)
	merge := "merge --stream " + stream
	if len(mergeOp) > 0 {
		// the same merge twice under one op id: the second returns the first's result
		merge += " --op " + mergeOp[0]
		ta.ok(merge)
	}
	ta.ok(merge)
	ta.ok("tick")
}

// costCells are the work table's cost cells by row, and the footer's under "". A stream
// the tick archived as its last card landed is brought back first, so its row is drawn.
func (ta *testApp) costCells() map[string]string {
	ta.t.Helper()
	var v whereView
	ta.json("where", &v)
	if v.Archived != nil {
		ta.ok("stream unarchive " + strings.Join(v.Archived.Streams, " "))
	}
	out := map[string]string{}
	lines := strings.Split(ta.ok("where"), "\n")
	in, at := false, -1
	for _, l := range lines {
		f := strings.Split(l, "|")
		switch {
		case strings.HasPrefix(l, "work "):
			in = true
			// cost, then per landed, are the work table's last columns
			require.Equal(ta.t, "per landed", strings.TrimSpace(f[len(f)-1]), "per landed is the work table's last column")
			at = len(f) - 2
			require.Equal(ta.t, "cost", strings.TrimSpace(f[at]), "cost is the column before it")
		case in && strings.TrimSpace(l) == "":
			return out
		case in && len(f) > at && !strings.HasPrefix(l, "-"):
			out[strings.TrimSpace(f[0])] = strings.TrimSpace(f[at])
		}
	}
	return out
}

func TestTheWorkTableCostColumnIsEachStreamsLandedCostAndTheTotal(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1:8")
	ta.m.SetRoutes(costRoutes())
	ta.readersReadPro() // a fleet row reads flash unless it says more (sprint fleetReadsFlashOnly)
	ta.ok("add --stream s1 --count 2 --brief-file " + proBriefFile(t))
	ta.ok("add --stream s2 --count 2 --brief-file " + proBriefFile(t))
	ta.ok("add --stream s3 --count 1 --one --brief-file " + proBriefFile(t))
	for _, id := range []string{"s1-1", "s1-2", "s2-1", "s2-2", "s3-1"} {
		ta.tierNow(id, "pro") // read by two readers: pro cards on pro
	}
	ta.ok("start")
	// s1-1: actual 0.0012 + 0.0003 + 0.0001; s1-2: actual 0.002, a read with no actual
	// priced by its route (2000 input at 0.5 a million: 0.001), a read with nothing
	ta.landStream("s1",
		[]string{"input=10 actual_usd=0.0012 actual_by=harness", "input=10 actual_usd=0.002 actual_by=harness"},
		[]string{"input=1 actual_usd=0.0003 actual_by=harness", "input=2000 model=opencode/deepseek-v4-pro"},
		[]string{"input=1 actual_usd=0.0001 actual_by=harness", ""})
	// s2-1: 0.00005 + 0.000004, shown to four places; s2-2: nothing priced
	ta.landStream("s2",
		[]string{"input=10 actual_usd=0.00005 actual_by=harness", ""},
		[]string{"input=1 actual_usd=0.000004 actual_by=harness", ""},
		[]string{"", ""})
	cells := ta.costCells()
	assert.Equal(t, "$0.01", cells["s1"], "0.0016 + 0.003, shown in cents, rounded up")
	assert.Equal(t, "$0.01", cells["s2"], "0.000054 rounds up to a cent, and the unpriced card adds nothing")
	assert.Equal(t, "-", cells["s3"], "nothing landed: a dash, never $0.00")
	assert.Equal(t, "$0.02", cells[""], "the total at the bottom: the cells as shown, summed")

	var landed, unpriced cardView
	ta.json("card s1-2", &landed)
	assert.Equal(t, "0.003", landed.Primary.F("cost"), "the landed card carries its total")
	ta.json("card s2-2", &unpriced)
	assert.Equal(t, "", unpriced.Primary.F("cost"), "an unpriced card carries none")

	// a landing replayed by its op id writes nothing again: s3 counts its card once
	ta.landStream("s3", []string{"input=1 actual_usd=0.01 actual_by=harness"}, []string{""}, []string{""}, "land-s3")
	cells = ta.costCells()
	assert.Equal(t, "$0.01", cells["s3"], "a replayed merge does not count twice")
	assert.Equal(t, "$0.03", cells[""])

	// clear empties it with the tables
	ta.ok("stop --reason r --until 9999h")
	ta.ok("clear --confirm sprint")
	for row, cell := range ta.costCells() {
		assert.NotContains(t, cell, "$", "after clear, row %q", row)
	}
}

// A landing's total leaves no consumer out: a read handed back and retired is in the
// card's own records, written when it was returned (cost.go: the cost is in the card).
func TestALandingCountsAReadReturnedAndRetired(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b,reader-c --members m1:8")
	ta.ok("add --stream s1 --count 1 --one --brief-file " + proBriefFile(t))
	ta.ok("start")
	ta.ok("tick")
	ta.ok("take --as m1 s1-1.w1@1")
	ta.ok("finish --as m1 s1-1.w1@1 --usage 'input=1 actual_usd=0.1 actual_by=harness'")
	ta.ok("ask")
	var asked []string
	for _, rd := range []string{"reader-a", "reader-b", "reader-c"} {
		if code, _, _ := ta.do("read --as " + rd + " --begin s1-1.r1." + rd); code == 0 {
			asked = append(asked, rd)
		}
	}
	require.Len(t, asked, 2)
	ta.ok("read --as " + asked[0] + " --return s1-1.r1." + asked[0] + " --reason 'no verdict' --usage 'input=1 actual_usd=0.02 actual_by=harness'")
	ta.ok("tick") // the tick asks the returned read of the reader free
	for _, rd := range []string{"reader-a", "reader-b", "reader-c"} {
		if rd == asked[0] {
			continue
		}
		ta.ok("read --as " + rd + " --ok s1-1.r1." + rd + " --usage 'input=1 actual_usd=0.003 actual_by=harness'")
	}
	ta.ok("accept --stream s1")
	ta.ok("merge --stream s1")
	ta.ok("tick")
	var v cardView
	ta.json("card s1-1", &v)
	assert.Equal(t, "0.126", v.Primary.F("cost"), "the work, the returned read and the two reads: 0.1 + 0.02 + 0.003 + 0.003")
	assert.Equal(t, "$0.13", ta.costCells()["s1"])
}
