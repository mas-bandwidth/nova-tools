package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// The fleet table's done and ok% are the table's own formulas over the hidden
// ok and failed cells: finish places a work card in one of them, and nothing
// writes done or ok%. Three ok and one failed on m1 is 4 done and 75.0%; m2
// with none is 0 and the known-empty 0.0%; the footer pools over the members
// (3 of 4, 75.0%, not the mean 37.5%).
func TestFleetDoneAndOkPctAreTableFormulas(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("add --stream s1 --count 4")
	ta.deal(4)
	ta.ok("take --as m1 --limit 4")
	ta.ok("finish --as m1 s1-1.w1@1 s1-3.w1@1 s1-4.w1@1")
	ta.ok("finish --as m1 s1-2.w1@1 --failed --report 'the tests went red'")
	ta.ok("fleet up m2")
	out := ta.ok("where")
	i := strings.Index(out, "fleet |")
	if i < 0 {
		t.Fatalf("where has no fleet table:\n%s", out)
	}
	fleet := out[i:]
	if j := strings.Index(fleet, "\n\n"); j >= 0 {
		fleet = fleet[:j+1]
	}
	want := "fleet | ready | working | done | ok%   | status | load\n" +
		"------+-------+---------+------+-------+--------+-----\n" +
		"m1    |     0 |       0 |    4 | 75.0% | up     | 0.0%\n" +
		"m2    |     0 |       0 |    0 | 0.0%  | up     | 0.0%\n" +
		"------+-------+---------+------+-------+--------+-----\n" +
		"      |     0 |       0 |    4 | 75.0% |        |\n"
	if fleet != want {
		t.Fatalf("the fleet table:\n%s\nwant:\n%s", fleet, want)
	}
	var v whereView
	if err := json.Unmarshal([]byte(ta.ok("where --json")), &v); err != nil {
		t.Fatal(err)
	}
	m1, m2 := v.Tables["fleet"]["m1"], v.Tables["fleet"]["m2"]
	if m1["done"] != "4" || m1["okpct"] != "75.0%" || m1["ok"] != "3" || m1["failed"] != "1" || m2["done"] != "0" || m2["okpct"] != "0.0%" {
		t.Fatalf("where --json fleet: %v %v", m1, m2)
	}
	ta.clean()
}
