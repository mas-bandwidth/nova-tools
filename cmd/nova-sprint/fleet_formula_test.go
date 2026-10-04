package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// verdicts runs the tick's verdicts part once (sprint.TickVerdicts): the readers'
// verdicts move the finished work cards to ok and failed.
func (ta *testApp) verdicts() {
	ta.t.Helper()
	st, err := ta.a.store(common{redis: "mem:0", actor: "tester"})
	require.NoError(ta.t, err)
	res, err := st.Run(context.Background(), store.TickPartStep("verdicts", sprint.TickVerdicts, sprint.TickReq{}, nil, nil, nil))
	require.NoError(ta.t, err, "verdicts: %+v %v", res.Refused, err)
	require.Empty(ta.t, res.Refused, "verdicts: %+v", res.Refused)
}

// The fleet table's done and ok% are the table's own formulas over the hidden
// ok and failed cells, which hold the readers' verdicts only (docs/SPEC-SPRINT.md
// section 1): a finish places a work card in the hidden finished cell, which counts
// in neither, and the verdicts part moves it on; nothing writes done or ok%. Four
// cards finished ok and one failed count nothing until read; then three read ok and
// one read broken on m1 is 4 done and 75.0%, and the failed finish no reader reads
// counts nowhere; m2 with none is 0 and the known-empty 0.0%; redealt is 0 on both;
// the footer pools over the members (3 of 4, 75.0%, not the mean 37.5%).
func TestFleetDoneAndOkPctAreTableFormulas(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("add --stream s1 --count 5")
	ta.deal(5)
	ta.ok("take --as m1 --limit 5")
	ta.ok("finish --as m1 s1-1.w1@1 s1-2.w1@1 s1-3.w1@1 s1-4.w1@1")
	ta.ok("finish --as m1 s1-5.w1@1 --failed --report 'the tests went red'")
	ta.ok("fleet up m2")
	m1 := func() map[string]string {
		var v whereView
		require.NoError(t, json.Unmarshal([]byte(ta.ok("where --json")), &v))
		return v.Tables["fleet"]["m1"]
	}
	require.Equal(t, "0", m1()["done"], "a worker's own word is no verdict: %v", m1())
	require.Equal(t, "5", m1()[sprint.Finished], "five finished, none read: %v", m1())

	// the ask asks a reader of each, in turn: s1-1 and s1-3 of reader-a, s1-2 and s1-4 of reader-b
	ta.ok("ask --limit 100")
	ta.ok("read --as reader-a --ok --limit 100")
	ta.ok("read --as reader-b --ok s1-2.r1.reader-b")
	ta.ok("read --as reader-b --broken s1-4.r1.reader-b --finding 'line 3: the test is missing'")
	ta.verdicts()
	out := ta.ok("where")
	i := strings.Index(out, "fleet |")
	require.GreaterOrEqual(t, i, 0, "where has no fleet table:\n%s", out)
	fleet := out[i:]
	if j := strings.Index(fleet, "\n\n"); j >= 0 {
		fleet = fleet[:j+1]
	}
	want := "fleet | ready | working | width | done | ok%   | redealt | status | load\n" +
		"------+-------+---------+-------+------+-------+---------+--------+-----\n" +
		"m1    |     0 |       0 |    64 |    4 | 75.0% |       0 | up     | 0.0%\n" +
		"m2    |     0 |       0 |    64 |    0 | 0.0%  |       0 | up     | 0.0%\n" +
		"------+-------+---------+-------+------+-------+---------+--------+-----\n" +
		"      |     0 |       0 |   128 |    4 | 75.0% |       0 |        |\n"
	require.Equal(t, want, fleet, "the fleet table")
	var v whereView
	require.NoError(t, json.Unmarshal([]byte(ta.ok("where --json")), &v))
	r1, r2 := v.Tables["fleet"]["m1"], v.Tables["fleet"]["m2"]
	require.Equal(t, "4", r1["done"], "where --json fleet: %v %v", r1, r2)
	require.Equal(t, "75.0%", r1["okpct"], "where --json fleet: %v %v", r1, r2)
	require.Equal(t, "3", r1["ok"], "where --json fleet: %v %v", r1, r2)
	require.Equal(t, "1", r1["failed"], "where --json fleet: %v %v", r1, r2)
	require.Equal(t, "1", r1[sprint.Finished], "the failed finish no reader reads stays finished: %v", r1)
	require.Equal(t, "0", r2["done"], "where --json fleet: %v %v", r1, r2)
	require.Equal(t, "0.0%", r2["okpct"], "where --json fleet: %v %v", r1, r2)
	ta.clean()
}
