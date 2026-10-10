package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// where --json carries each reader's spend (the owner, 2026-10-05: "I would ask that you need
// to track spend on readers, can you do this before we start?"): a pro card read by two
// readers with their harnesses' costs, one of them a return first, shows each reader's sum
// all time and in the last hour, per priced read and how many were priced, on the stream's
// stream_costs from the tick's where record, so nobody tallies the log to know it.
func TestWhereReadersCarryEachReadersSpend(t *testing.T) {
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
	first := asked[0]
	ta.ok("read --as " + first + " --return s1-1.r1." + first + " --reason 'no verdict' --usage 'input=1 actual_usd=0.02 actual_by=harness'")
	ta.ok("tick")
	var read []string
	for _, rd := range []string{"reader-a", "reader-b", "reader-c"} {
		if rd == first {
			continue
		}
		ta.ok("read --as " + rd + " --ok s1-1.r1." + rd + " --usage 'input=1 actual_usd=0.003 actual_by=harness'")
		read = append(read, rd)
	}
	ta.ok("tick") // the where record is the tick's

	var v struct {
		StreamCosts map[string]sprint.TierCosts `json:"stream_costs"`
	}
	ta.json("where", &v)
	spends := v.StreamCosts["s1"].Readers // the one stream's record carries every reader's sum
	want := map[string]map[string]string{
		first:   {sprint.ReaderSpendCol: "$0.02", sprint.ReaderSpendHourCol: "$0.02", sprint.ReaderPerReadCol: "$0.02", sprint.ReaderPricedCol: "1"},
		read[0]: {sprint.ReaderSpendCol: "$0.01", sprint.ReaderSpendHourCol: "$0.01", sprint.ReaderPerReadCol: "$0.01", sprint.ReaderPricedCol: "1"},
		read[1]: {sprint.ReaderSpendCol: "$0.01", sprint.ReaderSpendHourCol: "$0.01", sprint.ReaderPerReadCol: "$0.01", sprint.ReaderPricedCol: "1"},
	}
	for rd, cells := range want {
		assert.Equal(t, cells, spends[rd].Cells(), "where --json stream_costs: %s", rd)
	}
	require.Len(t, spends, 3, "three readers read: %v", spends)
}
