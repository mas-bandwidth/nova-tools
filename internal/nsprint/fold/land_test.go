package fold_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fold"
)

// landGate is one gate the fixture receipts: its batch record, its receipt
// and the GATE event (at, ms) the worker's ns_gate_receipt writes.
type landGate struct {
	base, batch, state, class, members, fromTip string
	verdict, trainHead, selection, steps, coreS string
	at                                          string
}

// seedLand writes the lander's records for sprint S the way land.lua does
// (#3139 rev 7 section 2.2): s:<S>:units and s:<S>:u:<unit>, the batch and
// receipt hashes, the GATE events on land:<repo>:events and the landing
// receipts landed:<repo>:<unit>:<head>.
func seedLand(t *testing.T, sprint string, gates []landGate) *redis.Client {
	t.Helper()
	mr := miniredis.RunT(t)
	c := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = c.Close() })
	ctx := context.Background()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	s := "s:" + sprint
	must(c.HSet(ctx, s, "status", "closed", "opened_at", "100000", "closed_at", "900000").Err())
	units := map[string][]string{ // unit: head, state, landed_head
		"u1": {"h1", "landed", "h1"},
		"u2": {"h2", "dropped", ""},
		"u3": {"h3", "landed", "h3"},
		"u4": {"h4", "landable", ""},
	}
	for u, v := range units {
		must(c.SAdd(ctx, s+":units", u).Err())
		must(c.HSet(ctx, s+":u:"+u, "repo", "nova-tools", "base", "dev", "head", v[0], "state", v[1], "landed_head", v[2]).Err())
	}
	must(c.Set(ctx, "landed:nova-tools:u1:h1", "m1 b6 land:nova-tools:receipt:b6:1", 0).Err())
	must(c.Set(ctx, "landed:nova-tools:u3:h3", "m1 b6 land:nova-tools:receipt:b6:1", 0).Err())
	// A landing receipt of another sprint's unit is not this sprint's.
	must(c.Set(ctx, "landed:nova-tools:x9:h9", "m0 b0 land:nova-tools:receipt:b0:1", 0).Err())
	for _, g := range gates {
		must(c.HSet(ctx, "land:nova-tools:"+g.base+":batch:"+g.batch,
			"state", g.state, "class", g.class, "members", g.members, "from_tip", g.fromTip, "attempt", "1").Err())
		must(c.HSet(ctx, "land:nova-tools:receipt:"+g.batch+":1",
			"verdict", g.verdict, "from_tip", g.fromTip, "train_head", g.trainHead, "selection", g.selection,
			"steps", g.steps, "core_s", g.coreS, "at", g.at).Err())
		must(c.XAdd(ctx, &redis.XAddArgs{Stream: "land:nova-tools:events", ID: g.at + "-0", Values: map[string]any{
			"event": "GATE", "repo": "nova-tools", "base": g.base, "batch": g.batch, "attempt": "1", "verdict": g.verdict, "at": g.at,
		}}).Err())
	}
	return c
}

// TestFoldLandFiveNumbers is B17 of #3139 rev 7 (section 9): the fold prints
// core-s per landed unit, voided share, bisect share, selection misses and
// git ops, each from the gate receipts in the sprint's window.
func TestFoldLandFiveNumbers(t *testing.T) {
	sel := "checks=selected packages=3"
	gates := []landGate{
		// Outside the window: before opened_at, never counted.
		{base: "dev", batch: "b0", state: "landed", class: "go", members: "x9@h9", fromTip: "t9", verdict: "GREEN", trainHead: "t9a", coreS: "999", at: "50000"},
		// A red train of three on T0; its steps name one git op.
		{base: "dev", batch: "b1", state: "red", class: "go", members: "u1@h1,u2@h2,u3@h3", fromTip: "T0", verdict: "RED", trainHead: "T0r", selection: sel, steps: "git-merge-tree:1:0.5:0;go-test:60:90:0", coreS: "100", at: "200000"},
		// Gated green behind it, then voided with the chain.
		{base: "dev", batch: "b2", state: "void", class: "go", members: "u4@h4", fromTip: "T0r", verdict: "GREEN", trainHead: "T0v", selection: sel, coreS: "50", at: "210000"},
		// Attribution: u1 alone, u2 alone, the prefix u1,u2, all on T0.
		{base: "dev", batch: "b3", state: "green", class: "go", members: "u1@h1", fromTip: "T0", verdict: "GREEN", trainHead: "T0a", selection: sel, coreS: "40", at: "300000"},
		{base: "dev", batch: "b4", state: "red", class: "go", members: "u2@h2", fromTip: "T0", verdict: "RED", trainHead: "T0b", selection: sel, coreS: "40", at: "300001"},
		{base: "dev", batch: "b5", state: "red", class: "go", members: "u1@h1,u2@h2", fromTip: "T0", verdict: "RED", trainHead: "T0c", selection: sel, coreS: "40", at: "300002"},
		// The survivors land on a new tip; the selected green gate's train is T1.
		{base: "dev", batch: "b6", state: "landed", class: "go", members: "u1@h1,u3@h3", fromTip: "T0x", verdict: "GREEN", trainHead: "T1", selection: sel, steps: "git-fetch:2:0.1:0;git-merge-tree:1:0.4:0;go-test:20:28:0", coreS: "30", at: "400000"},
		// The full tip gate of T1 is red: the selection missed it.
		{base: "dev", batch: "b7", state: "red", class: "full", members: "", fromTip: "T1", verdict: "RED", trainHead: "T1", coreS: "200", at: "500000"},
		// A bench fault: a receipt, no core-s.
		{base: "dev", batch: "b8", state: "error", class: "go", members: "u4@h4", fromTip: "T1", verdict: "ERROR", coreS: "0", at: "600000"},
		// Another base the sprint has no unit on: not this sprint's.
		{base: "main", batch: "m1", state: "green", class: "go", members: "z1@hz", fromTip: "M0", verdict: "GREEN", trainHead: "M1", coreS: "777", at: "610000"},
		// After closed_at: not counted.
		{base: "dev", batch: "b9", state: "green", class: "go", members: "u4@h4", fromTip: "T1", verdict: "GREEN", trainHead: "T2", coreS: "888", at: "950000"},
	}
	c := seedLand(t, "s17", gates)
	sum, err := fold.Read(context.Background(), c, "s17")
	if err != nil {
		t.Fatal(err)
	}
	l := sum.Land
	if l.Receipts != 8 || l.CoreS != 500 || l.Landed != 2 || l.Voided != 1 || l.Bisect != 3 ||
		l.SelectionMisses != 1 || l.SelectedGreen != 3 || l.GitOps != 3 || l.GitUnmeasured != 6 {
		t.Fatalf("land cost = %+v; want receipts 8, core_s 500, landed 2, voided 1, bisect 3, selection misses 1 of 3 selected green, git ops 3 with 6 unmeasured", l)
	}
	var out bytes.Buffer
	fold.PrintLines(&out, sum)
	want := "FOLD LAND sprint=s17 core_s_per_landed=250.0 voided_share=12.5% bisect_share=37.5% selection_misses=1 git_ops=3 receipts=8 core_s=500.0 landed=2 voided=1 bisect=3 selected_green=3 git_unmeasured=6 core_unmeasured=0\n"
	if !strings.Contains(out.String(), want) {
		t.Fatalf("fold output has no land line %q:\n%s", want, out.String())
	}
}

// TestFoldLandNoReceipts: a sprint the lander never gated prints the line with
// every quotient as -, never a zero that reads as measured.
func TestFoldLandNoReceipts(t *testing.T) {
	c := seedLand(t, "s0", nil)
	sum, err := fold.Read(context.Background(), c, "s0")
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	fold.PrintLines(&out, sum)
	want := "FOLD LAND sprint=s0 core_s_per_landed=- voided_share=- bisect_share=- selection_misses=0 git_ops=- receipts=0 core_s=0.0 landed=2 voided=0 bisect=0 selected_green=0 git_unmeasured=0 core_unmeasured=0\n"
	if !strings.Contains(out.String(), want) {
		t.Fatalf("fold output has no land line %q:\n%s", want, out.String())
	}
}
