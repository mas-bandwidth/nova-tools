package land_test

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/land"
)

// Controls L4, L4b, L4c, L4d and L8 (Issue #3139 rev 7 §6, §11; B8): red batches settle by
// parallel attribution (the tip, singles and prefixes queued at once), flaky tests rerun once,
// and nothing touches GitHub.

// redGate decides one gate: verdict and the receipt's failing field.
type redGate func(batch string, attempt int, units []string) (verdict, failing string)

// comboGate is red when the gated units contain every unit of any bad set.
func comboGate(bad ...[]string) redGate {
	return func(_ string, _ int, units []string) (string, string) {
		have := map[string]bool{}
		for _, u := range units {
			have[u] = true
		}
		for _, set := range bad {
			all := true
			for _, u := range set {
				all = all && have[u]
			}
			if all {
				return "RED", "internal/q TestCombo"
			}
		}
		return "GREEN", ""
	}
}

// noGitHub fails the test on any HTTP request through the default transport (L8, L18).
type noGitHub struct{ t *testing.T }

func noHTTP(t *testing.T) {
	old := http.DefaultTransport
	http.DefaultTransport = noGitHub{t}
	t.Cleanup(func() { http.DefaultTransport = old })
}

func (n noGitHub) RoundTrip(r *http.Request) (*http.Response, error) {
	n.t.Errorf("red batches made an HTTP call: %s %s", r.Method, r.URL)
	return nil, fmt.Errorf("no HTTP in red batches")
}

func trainSHA(fromTip string, members string) string {
	s := sha1.Sum([]byte(fromTip + "|" + members))
	return hex.EncodeToString(s[:])
}

func unitsOf(members string) []string {
	var out []string
	for _, m := range strings.Split(members, ",") {
		if m != "" {
			out = append(out, strings.SplitN(m, "@", 2)[0])
		}
	}
	return out
}

// trees is the fake gate's model of content: the units in each gated train head (a from_tip
// never gated, the fixture tip, holds none). A gate sees its from_tip's units plus its members,
// so a batch of one on base-plus-train is red with what the train carries.
var trees = map[string][]string{}

// gateOne claims and receipts one queued batch at its current attempt.
func gateOne(t *testing.T, f *landTestFixture, id string, g redGate) {
	t.Helper()
	h := batchHash(t, f, id)
	if h["state"] != "queued" || h["from_tip"] == "" {
		return
	}
	var attempt int
	fmt.Sscan(h["attempt"], &attempt)
	if r, err := land.CallGateClaim(f.ctx, f.client, f.repo, f.base, id, attempt, h["token"], "bench-1", "slot-1"); err != nil || r != "OK" {
		t.Fatalf("claim %s: %s %v", id, r, err)
	}
	units := append(append([]string{}, trees[h["from_tip"]]...), unitsOf(h["members"])...)
	verdict, failing := g(id, attempt, units)
	th := trainSHA(h["from_tip"], h["members"])
	trees[th] = units
	if r, err := land.CallGateReceipt(f.ctx, f.client, f.repo, f.base, id, attempt, h["token"], verdict, "bench-1", "w", th, "tree-"+th[:8], "", "", "", failing, "", "1"); err != nil || r != "OK" {
		t.Fatalf("receipt %s: %s %v", id, r, err)
	}
}

func chainIDs(t *testing.T, f *landTestFixture) []string {
	t.Helper()
	ids, err := f.client.ZRange(f.ctx, land.ChainKey(f.repo, f.base), 0, -1).Result()
	if err != nil {
		t.Fatalf("chain: %v", err)
	}
	return ids
}

// redRun drives batcher, gates, red ticks and the publisher until nothing is left to settle.
type redRun struct {
	splits   int      // SPLIT lines (attribution rounds)
	walls    []int    // gates queued by each SPLIT, counted before any is gated
	lines    []string // every batcher and red line
	maxRound int
}

func driveRed(t *testing.T, f *landTestFixture, b *land.Batcher, g redGate) redRun {
	t.Helper()
	var run redRun
	for tick := 0; tick < 60; tick++ {
		rep, err := b.Plan(f.ctx)
		if err != nil {
			t.Fatalf("plan: %v", err)
		}
		run.lines = append(run.lines, rep.Lines...)
		for _, id := range chainIDs(t, f) {
			gateOne(t, f, id, g)
		}
		for pass := 0; pass < 2; pass++ {
			red, err := b.RedTick(f.ctx)
			if err != nil {
				t.Fatalf("red tick: %v", err)
			}
			run.lines = append(run.lines, red.Lines...)
			for _, l := range red.Lines {
				if strings.HasPrefix(l, "SPLIT ") {
					run.splits++
					gates, err := land.SplitGates(f.ctx, f.client, f.repo, f.base)
					if err != nil {
						t.Fatalf("split gates: %v", err)
					}
					queued := 0
					for _, id := range gates {
						if batchHash(t, f, id)["state"] == "queued" {
							queued++
						}
					}
					run.walls = append(run.walls, queued)
				}
				if strings.Contains(l, "REFUSED") {
					t.Fatalf("red tick refused: %s", l)
				}
			}
			for _, k := range red.Kept {
				var r int
				fmt.Sscan(strings.Fields(k)[1], &r)
				if r > run.maxRound {
					run.maxRound = r
				}
			}
			gates, err := land.SplitGates(f.ctx, f.client, f.repo, f.base)
			if err != nil {
				t.Fatalf("split gates: %v", err)
			}
			for _, id := range gates {
				gateOne(t, f, id, g)
			}
			for _, id := range chainIDs(t, f) { // a rerun re-queues a chain batch
				gateOne(t, f, id, g)
			}
		}
		for {
			ids := chainIDs(t, f)
			if len(ids) == 0 {
				break
			}
			h := batchHash(t, f, ids[0])
			if h["state"] != "green" {
				break
			}
			publish(t, f, ids[0], h["train_head"])
			run.lines = append(run.lines, "LANDED "+ids[0]+" "+h["members"])
		}
		landable, _ := f.client.ZCard(f.ctx, land.LandableKey(f.sprint, f.repo, f.base)).Result()
		splits, _ := f.client.ZCard(f.ctx, land.SplitsKey(f.repo, f.base)).Result()
		if landable == 0 && splits == 0 && len(chainIDs(t, f)) == 0 {
			return run
		}
	}
	t.Fatalf("red run did not settle in 60 ticks:\n%s", strings.Join(run.lines, "\n"))
	return run
}

// redUnits adds n units with disjoint files, oldest first, authored by emma.
func redUnits(t *testing.T, f *landTestFixture, base, n int) []string {
	t.Helper()
	var out []string
	for i := 0; i < n; i++ {
		out = append(out, addUnit(t, f, base+i, sha(base+i), fmt.Sprintf("internal/r%d/f.go", base+i), "", "emma"))
	}
	return out
}

// settled checks every unit ends landed or dropped with the named reason prefix, and each
// dropped unit has exactly its one fix task on its author's queue.
func settled(t *testing.T, f *landTestFixture, units []string, dropped map[string]string, run redRun) {
	t.Helper()
	for _, u := range units {
		st := unitField(t, f, u, "state")
		want, drop := dropped[u]
		switch {
		case drop && (st != "dropped" || !strings.HasPrefix(unitField(t, f, u, "drop_reason"), want)):
			t.Fatalf("%s: state %q reason %q, want dropped %s\n%s", u, st, unitField(t, f, u, "drop_reason"), want, strings.Join(run.lines, "\n"))
		case !drop && st != "landed":
			t.Fatalf("%s: state %q reason %q, want landed\n%s", u, st, unitField(t, f, u, "drop_reason"), strings.Join(run.lines, "\n"))
		}
		task := "fix-" + u + "-" + unitField(t, f, u, "head")[:8]
		owner, _ := f.client.HGet(f.ctx, "task:"+task, "owner").Result()
		if drop && owner != "emma" {
			t.Fatalf("%s dropped with no fix task %s on emma (owner %q)", u, task, owner)
		}
		if !drop && owner != "" {
			t.Fatalf("%s landed but has fix task %s", u, task)
		}
	}
	// Every wall is the tip, each member alone and each inner prefix, all queued at once.
	for i, w := range run.walls {
		if w < 3 {
			t.Fatalf("split %d queued %d gates at once, want the whole wall (tip + singles + prefixes)", i+1, w)
		}
	}
}

// TestL4 verifies control L4 (§6.1): one red member in a batch of four is dropped alone, with one
// fix task, from one wall of 2N-1 gates; the other three land.
func TestL4(t *testing.T) {
	f := newLandFixture(t, "nova-tools", "dev")
	noHTTP(t)
	u := redUnits(t, f, 800, 4)
	run := driveRed(t, f, newBatcher(f, nil), comboGate([]string{u[1]}))
	settled(t, f, u, map[string]string{u[1]: "alone=red@" + b3Tip[:8] + ":internal/q.TestCombo"}, run)
	if run.splits != 1 || len(run.walls) != 1 || run.walls[0] != 7 {
		t.Fatalf("splits %d walls %v, want one wall of 7 (tip + 4 singles + 2 prefixes)\n%s", run.splits, run.walls, strings.Join(run.lines, "\n"))
	}

	// The tip alone red is the base's fault: the base freezes and no member drops.
	f2 := newLandFixture(t, "nova-tools", "dev")
	u2 := redUnits(t, f2, 820, 3)
	b2 := newBatcher(f2, nil)
	if _, err := b2.Plan(f2.ctx); err != nil {
		t.Fatal(err)
	}
	baseRed := func(_ string, _ int, units []string) (string, string) { return "RED", "internal/base TestTip" }
	for _, id := range chainIDs(t, f2) {
		gateOne(t, f2, id, baseRed)
	}
	if _, err := b2.RedTick(f2.ctx); err != nil {
		t.Fatal(err)
	}
	gates, _ := land.SplitGates(f2.ctx, f2.client, f2.repo, f2.base)
	for _, id := range gates {
		gateOne(t, f2, id, baseRed)
	}
	rep, err := b2.RedTick(f2.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if fz, _ := f2.client.HGet(f2.ctx, land.FreezeKey(f2.repo, f2.base), "reason").Result(); !strings.HasPrefix(fz, "base-red internal/base.TestTip") {
		t.Fatalf("freeze reason %q, want base-red (lines %v)", fz, rep.Lines)
	}
	// source red: ns_land_intent refuses publishing under it and land thaw lifts it (B11).
	if src, _ := f2.client.HGet(f2.ctx, land.FreezeKey(f2.repo, f2.base), "source").Result(); src != "red" {
		t.Fatalf("freeze source %q, want red", src)
	}
	for _, x := range u2 {
		if st := unitField(t, f2, x, "state"); st != "landable" {
			t.Fatalf("base red: %s state %q, want landable (not the member's fault)", x, st)
		}
	}
}

// TestL4b verifies control L4b (§6.1): a straddling pair A+C in A,B,C,D, with the halves AB and CD
// green, is found by the first red prefix ABC: C drops combo-red-with=A,B; A, B, D land.
func TestL4b(t *testing.T) {
	f := newLandFixture(t, "nova-tools", "dev")
	noHTTP(t)
	u := redUnits(t, f, 840, 4)
	g := comboGate([]string{u[0], u[2]})
	for _, half := range [][]string{{u[0], u[1]}, {u[2], u[3]}} {
		if v, _ := g("", 1, half); v != "GREEN" {
			t.Fatalf("fixture: half %v is %s, want GREEN (halves must miss the pair)", half, v)
		}
	}
	run := driveRed(t, f, newBatcher(f, nil), g)
	settled(t, f, u, map[string]string{u[2]: "combo-red-with=" + u[0] + "," + u[1]}, run)
	if cw := unitField(t, f, u[2], "combo_with"); cw != u[0]+"@"+sha(840)+","+u[1]+"@"+sha(841) {
		t.Fatalf("combo_with %q, want the heads of the green prefix", cw)
	}
	if run.splits != 1 {
		t.Fatalf("splits %d, want 1", run.splits)
	}
}

// TestL4c verifies control L4c (§6.1): a three-member interaction A+B+C in A,B,C,D drops C (the
// member completing the first red prefix) and lands A, B and D.
func TestL4c(t *testing.T) {
	f := newLandFixture(t, "nova-tools", "dev")
	noHTTP(t)
	u := redUnits(t, f, 860, 4)
	run := driveRed(t, f, newBatcher(f, nil), comboGate([]string{u[0], u[1], u[2]}))
	settled(t, f, u, map[string]string{u[2]: "combo-red-with=" + u[0] + "," + u[1]}, run)
}

// TestL4d verifies control L4d (§6.1): two disjoint pairs A+C and B+D in A,B,C,D settle within 3
// rounds: one member of each pair drops, the rest land, and with rounds_max 1 the survivors of
// the first round plan alone.
func TestL4d(t *testing.T) {
	f := newLandFixture(t, "nova-tools", "dev")
	noHTTP(t)
	u := redUnits(t, f, 880, 4)
	g := comboGate([]string{u[0], u[2]}, []string{u[1], u[3]})
	run := driveRed(t, f, newBatcher(f, nil), g)
	settled(t, f, u, map[string]string{
		u[2]: "combo-red-with=" + u[0] + "," + u[1],
		u[3]: "combo-red-with=" + u[0] + "," + u[1],
	}, run)
	if run.splits > 3 || run.maxRound > 3 {
		t.Fatalf("splits %d max round %d, want within 3 rounds\n%s", run.splits, run.maxRound, strings.Join(run.lines, "\n"))
	}

	// The rounds bound: at rounds_max the survivors re-enter as batches of one.
	f2 := newLandFixture(t, "nova-tools", "dev")
	u2 := redUnits(t, f2, 900, 4)
	b2 := newBatcher(f2, nil)
	b2.RoundsMax = 1
	run2 := driveRed(t, f2, b2, comboGate([]string{u2[0], u2[2]}, []string{u2[1], u2[3]}))
	settled(t, f2, u2, map[string]string{u2[2]: "combo-red-with=", u2[3]: "alone=red@"}, run2)
	afterSplit := false
	for _, l := range run2.lines {
		afterSplit = afterSplit || strings.HasPrefix(l, "SPLIT ")
		if afterSplit && strings.HasPrefix(l, "PLAN ") && !strings.Contains(l, "members=1") {
			t.Fatalf("a survivor past rounds_max was planned with others: %s\n%s", l, strings.Join(run2.lines, "\n"))
		}
	}
	for _, x := range []string{u2[0], u2[1]} {
		if unitField(t, f2, x, "red_alone") != "1" {
			t.Fatalf("%s red_alone %q after rounds_max 1, want 1", x, unitField(t, f2, x, "red_alone"))
		}
	}
}

// outside marks one package outside every selection closure.
type outside string

func (o outside) InClosure(_ context.Context, _ string, _ map[string]string, pkg string) (bool, error) {
	return pkg != string(o), nil
}

// TestL8 verifies control L8 (§6.2): a test red in two unrelated batches becomes flaky with one
// task on the unit and no GitHub call; a third red batch reruns and lands. A rerun red again on
// the same tree freezes the base instead of dropping a member.
func TestL8(t *testing.T) {
	f := newLandFixture(t, "nova-tools", "dev")
	noHTTP(t)
	b := newBatcher(f, nil)
	b.Closure = outside("internal/flaky")
	b.Owner = func(_ context.Context, pkg, test string) string { return "johnny" }
	// The flake: attempt 1 of every batch is red on it, the rerun green.
	flake := func(_ string, attempt int, _ []string) (string, string) {
		if attempt == 1 {
			return "RED", "internal/flaky TestWobble"
		}
		return "GREEN", ""
	}
	const name = "internal/flaky.TestWobble"
	var units []string
	for i := 0; i < 3; i++ {
		u := redUnits(t, f, 920+i, 1)[0]
		units = append(units, u)
		run := driveRed(t, f, b, flake)
		if st := unitField(t, f, u, "state"); st != "landed" {
			t.Fatalf("batch %d: %s state %q, want landed after the rerun\n%s", i+1, u, st, strings.Join(run.lines, "\n"))
		}
		if run.splits != 0 {
			t.Fatalf("batch %d: a flaky test split the batch (%d)", i+1, run.splits)
		}
		fh, _ := f.client.HGetAll(f.ctx, land.FlakyTestKey(f.repo, name)).Result()
		want := map[int]string{0: "seen", 1: "flaky", 2: "flaky"}[i]
		if fh["state"] != want || fh["hits"] != fmt.Sprint(i+1) {
			t.Fatalf("batch %d: flaky %v, want state %s hits %d", i+1, fh, want, i+1)
		}
		// The rerun's receipt (attempt 2) names the test it reran.
		reruns := 0
		for _, l := range run.lines {
			if f0 := strings.Fields(l); len(f0) > 1 && f0[0] == "RERUN" {
				reruns++
				k := land.ReceiptKey(f.repo, f0[1], 2)
				if v, _ := f.client.HGet(f.ctx, k, "flaky_rerun").Result(); v != name {
					t.Fatalf("receipt %s flaky_rerun %q, want %s", k, v, name)
				}
			}
		}
		if reruns != 1 {
			t.Fatalf("batch %d: %d reruns, want exactly one\n%s", i+1, reruns, strings.Join(run.lines, "\n"))
		}
	}
	fh, _ := f.client.HGetAll(f.ctx, land.FlakyTestKey(f.repo, name)).Result()
	task := "flaky-" + f.repo + "-" + name
	if fh["task"] != task || fh["owner"] != "johnny" {
		t.Fatalf("flaky record %v, want task %s owner johnny", fh, task)
	}
	if n, _ := f.client.XLen(f.ctx, "q:johnny").Result(); n != 1 {
		t.Fatalf("q:johnny has %d entries, want exactly one flaky task", n)
	}
	if ref, _ := f.client.HGet(f.ctx, "task:"+task, "ref").Result(); ref != units[0] && ref != units[1] {
		t.Fatalf("flaky task ref %q, want the owning unit", ref)
	}

	// Red twice on one tree with no member touching the test: the base is red, nothing drops.
	u := redUnits(t, f, 940, 1)[0]
	if _, err := b.Plan(f.ctx); err != nil {
		t.Fatal(err)
	}
	always := func(string, int, []string) (string, string) { return "RED", "internal/flaky TestWobble" }
	for pass := 0; pass < 2; pass++ {
		for _, id := range chainIDs(t, f) {
			gateOne(t, f, id, always)
		}
		if _, err := b.RedTick(f.ctx); err != nil {
			t.Fatal(err)
		}
	}
	if fz, _ := f.client.HGet(f.ctx, land.FreezeKey(f.repo, f.base), "reason").Result(); !strings.HasPrefix(fz, "base-red "+name) {
		t.Fatalf("freeze %q, want base-red %s", fz, name)
	}
	if st := unitField(t, f, u, "state"); st != "landable" {
		t.Fatalf("%s state %q after a base red, want landable", u, st)
	}
}
