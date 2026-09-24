package land_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/land"
)

// fakeMerge is the merge-tree pre-check stub: conflict[base+" "+head] is true for a conflict.
type fakeMerge struct {
	conflict map[string]bool
	calls    int
}

func (m *fakeMerge) Conflicts(_ context.Context, base, head string) (bool, error) {
	m.calls++
	return m.conflict[base+" "+head], nil
}

const b3Tip = "1111111111111111111111111111111111111111"

// addUnit writes a unit at head and makes it landable (tier 0).
func addUnit(t *testing.T, f *landTestFixture, n int, head, files, parent, author string) string {
	t.Helper()
	unit := fmt.Sprintf("gh/mas-bandwidth/nova-tools/%d", n)
	if _, err := land.CallUnitHead(f.ctx, f.client, land.UnitHeadParams{
		Sprint: f.sprint, Unit: unit, Repo: f.repo, Base: f.base,
		Branch: fmt.Sprintf("card-%d", n), Head: head, BaseSHA: b3Tip,
		Files: files, StackParent: parent, Author: author,
	}); err != nil {
		t.Fatalf("unit head %d: %v", n, err)
	}
	if _, err := land.CallUnitEval(f.ctx, f.client, f.sprint, unit, f.repo, f.base, 0); err != nil {
		t.Fatalf("unit eval %d: %v", n, err)
	}
	return unit
}

func sha(n int) string { return fmt.Sprintf("%040d", n) }

func newBatcher(f *landTestFixture, m land.MergeChecker) *land.Batcher {
	return &land.Batcher{
		Client: f.client, Sprint: f.sprint, Repo: f.repo, Base: f.base, Lease: f.lease,
		BatchMax: 16, ChainMax: 4, Merge: m,
	}
}

func batchHash(t *testing.T, f *landTestFixture, id string) map[string]string {
	t.Helper()
	h, err := f.client.HGetAll(f.ctx, land.BatchKey(f.repo, f.base, id)).Result()
	if err != nil {
		t.Fatalf("batch %s: %v", id, err)
	}
	return h
}

func unitField(t *testing.T, f *landTestFixture, unit, field string) string {
	t.Helper()
	v, err := f.client.HGet(f.ctx, land.UnitKey(f.sprint, unit), field).Result()
	if err != nil && err.Error() != "redis: nil" {
		t.Fatalf("unit %s %s: %v", unit, field, err)
	}
	return v
}

func gatesLen(t *testing.T, f *landTestFixture) int64 {
	t.Helper()
	n, err := f.client.XLen(f.ctx, land.GatesStream(f.repo)).Result()
	if err != nil {
		t.Fatalf("xlen gates: %v", err)
	}
	return n
}

func wantRefused(t *testing.T, err error, reason string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), "REFUSED") || !strings.Contains(err.Error(), reason) {
		t.Fatalf("plan: got err %v, want REFUSED %s", err, reason)
	}
}

// gate claims and receipts a planned batch with the given verdict and train head.
func gate(t *testing.T, f *landTestFixture, id, verdict, trainHead string) {
	t.Helper()
	b := batchHash(t, f, id)
	if r, err := land.CallGateClaim(f.ctx, f.client, f.repo, f.base, id, 1, b["token"], "bench-1", "slot-1"); err != nil || r != "OK" {
		t.Fatalf("claim %s: %s %v", id, r, err)
	}
	if r, err := land.CallGateReceipt(f.ctx, f.client, f.repo, f.base, id, 1, b["token"], verdict, "bench-1", "w", trainHead, "tree-"+id, b["input_id"], "", "", "", "", "1"); err != nil || r != "OK" {
		t.Fatalf("receipt %s: %s %v", id, r, err)
	}
}

func publish(t *testing.T, f *landTestFixture, id, trainHead string) {
	t.Helper()
	if _, err := land.CallLandIntent(f.ctx, f.client, f.sprint, f.repo, f.base, id, f.lease); err != nil {
		t.Fatalf("intent %s: %v", id, err)
	}
	if r, err := land.CallLand(f.ctx, f.client, f.sprint, f.repo, f.base, id, f.lease, trainHead, "", "1"); err != nil || r != "OK" {
		t.Fatalf("land %s: %s %v", id, r, err)
	}
}

// TestL3 verifies control L3 (Issue #3139 rev 7 §11):
// a batch whose members share a file is refused before any gate.
func TestL3(t *testing.T) {
	f := newLandFixture(t, "nova-tools", "dev")
	a := addUnit(t, f, 301, sha(301), "internal/x/a.go,internal/x/shared.go", "", "emma")
	b := addUnit(t, f, 302, sha(302), "internal/x/shared.go,internal/y/b.go", "", "emma")

	_, _, err := land.CallBatchPlan(f.ctx, f.client, f.sprint, f.repo, f.base, "b-l3", f.lease,
		a+"@"+sha(301)+","+b+"@"+sha(302), "", "go", b3Tip, "in-l3")
	wantRefused(t, err, "overlap=internal/x/shared.go")
	if n := gatesLen(t, f); n != 0 {
		t.Fatalf("refused plan queued %d gates, want 0", n)
	}
	if h := batchHash(t, f, "b-l3"); len(h) != 0 {
		t.Fatalf("refused plan wrote batch %v", h)
	}
	for _, u := range []string{a, b} {
		if st := unitField(t, f, u, "state"); st != "landable" {
			t.Fatalf("%s state %q after refused plan, want landable", u, st)
		}
	}

	// The batcher never shapes the pair into one batch.
	rep, err := newBatcher(f, nil).Plan(f.ctx)
	if err != nil {
		t.Fatalf("batcher plan: %v", err)
	}
	if len(rep.Planned) != 2 {
		t.Fatalf("planned %d batches, want 2 (overlap splits): %+v", len(rep.Planned), rep)
	}
	for _, p := range rep.Planned {
		if len(p.Members) != 1 {
			t.Fatalf("batch %s has %d members, want 1: %v", p.ID, len(p.Members), p.Members)
		}
	}
}

// TestL3b verifies control L3b (Issue #3139 rev 7 §11): an ordinary batch touching
// only docs/roadmaps/sprint-x.sexp is planned; one touching any of the five
// storage-split paths is refused roadmap-path.
func TestL3b(t *testing.T) {
	f := newLandFixture(t, "nova-tools", "dev")
	sprintSet := addUnit(t, f, 310, sha(310), "docs/roadmaps/sprint-x.sexp", "", "emma")
	if _, _, err := land.CallBatchPlan(f.ctx, f.client, f.sprint, f.repo, f.base, "b-l3b-ok", f.lease,
		sprintSet+"@"+sha(310), "", "go", b3Tip, "in"); err != nil {
		t.Fatalf("ordinary batch with a sprint set: %v, want planned", err)
	}

	split := []string{
		"docs/roadmaps/nova-work.sexp",
		"docs/roadmaps/work/nova-tools.sexp",
		"docs/roadmaps/work/nova-tools.closed.sexp",
		"docs/roadmaps/blobs/ab/cdef.blob",
		"docs/roadmaps/ingest-map.sexp",
	}
	for i, p := range split {
		n := 320 + i
		u := addUnit(t, f, n, sha(n), p, "", "emma")
		_, _, err := land.CallBatchPlan(f.ctx, f.client, f.sprint, f.repo, f.base, fmt.Sprintf("b-l3b-%d", i), f.lease,
			u+"@"+sha(n), "", "go", b3Tip, "in")
		wantRefused(t, err, "roadmap-path="+p)
	}
	// A roadmap batch holds only docs/roadmaps/ paths.
	mixed := addUnit(t, f, 330, sha(330), "docs/roadmaps/nova-work.sexp,cmd/x/main.go", "", "emma")
	_, _, err := land.CallBatchPlan(f.ctx, f.client, f.sprint, f.repo, f.base, "b-l3b-mixed", f.lease,
		mixed+"@"+sha(330), "", "roadmap", b3Tip, "in")
	wantRefused(t, err, "roadmap-class=cmd/x/main.go")

	// The batcher puts the storage-split units in roadmap batches, never with go units.
	goUnit := addUnit(t, f, 340, sha(340), "internal/z/z.go", "", "emma")
	rep, err := newBatcher(f, nil).Plan(f.ctx)
	if err != nil {
		t.Fatalf("batcher plan: %v", err)
	}
	for _, p := range rep.Planned {
		for _, m := range p.Members {
			unit := strings.SplitN(m, "@", 2)[0]
			if unit == goUnit && p.Class != "go" {
				t.Fatalf("go unit planned in class %s", p.Class)
			}
			if unit != goUnit && p.Class != "roadmap" {
				t.Fatalf("storage-split unit %s planned in class %s", unit, p.Class)
			}
		}
	}
	if st := unitField(t, f, mixed, "state"); st != "dropped" || unitField(t, f, mixed, "drop_reason") != "roadmap-mixed" {
		t.Fatalf("mixed unit state %q reason %q, want dropped roadmap-mixed", st, unitField(t, f, mixed, "drop_reason"))
	}
}

// TestL16 verifies control L16 (Issue #3139 rev 7 §11): batch 2 of 4 turns red:
// 3 and 4 void and re-plan on the new base; all end landed or dropped.
func TestL16(t *testing.T) {
	f := newLandFixture(t, "nova-tools", "dev")
	units := map[string]string{}
	for i := 0; i < 8; i++ {
		n := 400 + i
		units[addUnit(t, f, n, sha(n), fmt.Sprintf("internal/p%d/f.go", i), "", "emma")] = sha(n)
	}
	b := newBatcher(f, nil)
	b.BatchMax = 2
	rep, err := b.Plan(f.ctx)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if len(rep.Planned) != 4 {
		t.Fatalf("planned %d batches, want 4", len(rep.Planned))
	}
	ids := []string{rep.Planned[0].ID, rep.Planned[1].ID, rep.Planned[2].ID, rep.Planned[3].ID}
	if h := batchHash(t, f, ids[0]); h["from_tip"] != b3Tip || h["parent"] != "" {
		t.Fatalf("front batch from_tip %q parent %q, want tip and no parent", h["from_tip"], h["parent"])
	}
	for k := 1; k < 4; k++ {
		if h := batchHash(t, f, ids[k]); h["parent"] != ids[k-1] {
			t.Fatalf("batch %d parent %q, want %s", k+1, h["parent"], ids[k-1])
		}
	}

	t1 := sha(9001)
	gate(t, f, ids[0], "GREEN", t1)
	gate(t, f, ids[1], "RED", sha(9002))
	voided, err := b.VoidRed(f.ctx, ids[1])
	if err != nil {
		t.Fatalf("void red: %v", err)
	}
	if strings.Join(voided, ",") != ids[2]+","+ids[3] {
		t.Fatalf("voided %v, want [%s %s]", voided, ids[2], ids[3])
	}
	for _, id := range ids[2:] {
		if h := batchHash(t, f, id); h["state"] != "void" {
			t.Fatalf("batch %s state %q, want void", id, h["state"])
		}
	}
	// B8 attributes the red batch; here its members drop (the stand-in for attribution).
	for _, m := range strings.Split(batchHash(t, f, ids[1])["members"], ",") {
		p := strings.SplitN(m, "@", 2)
		if ok, err := b.Drop(f.ctx, p[0], p[1], "red", ""); err != nil || !ok {
			t.Fatalf("drop %s: %v %v", m, ok, err)
		}
	}

	// Re-plan: the voided members go behind batch 1 on its train head.
	rep2, err := b.Plan(f.ctx)
	if err != nil {
		t.Fatalf("re-plan: %v", err)
	}
	if len(rep2.Planned) != 2 {
		t.Fatalf("re-planned %d batches, want 2", len(rep2.Planned))
	}
	if h := batchHash(t, f, rep2.Planned[0].ID); h["from_tip"] != t1 || h["parent"] != ids[0] {
		t.Fatalf("re-planned front from_tip %q parent %q, want %s on %s", h["from_tip"], h["parent"], t1, ids[0])
	}

	publish(t, f, ids[0], t1)
	head := t1
	for i, p := range rep2.Planned {
		if _, err := b.Plan(f.ctx); err != nil { // the tick binds from_tip once the parent has a train head
			t.Fatalf("tick: %v", err)
		}
		if h := batchHash(t, f, p.ID); h["from_tip"] != head {
			t.Fatalf("batch %s from_tip %q, want %s", p.ID, h["from_tip"], head)
		}
		th := sha(9100 + i)
		gate(t, f, p.ID, "GREEN", th)
		publish(t, f, p.ID, th)
		head = th
	}

	for u, h := range units {
		st := unitField(t, f, u, "state")
		if st != "landed" && st != "dropped" {
			t.Fatalf("unit %s@%s ended %q, want landed or dropped", u, h[:8], st)
		}
	}
}

// TestL24 verifies control L24 (Issue #3139 rev 7 §11): a child unit is never
// planned before its sexp parent.
func TestL24(t *testing.T) {
	f := newLandFixture(t, "nova-tools", "dev")
	parent := "gh/mas-bandwidth/nova-tools/3011"
	// The child is older (evaluated first) but its parent is still reading.
	child := addUnit(t, f, 3053, sha(3053), "cmd/c/c.go", parent, "emma")
	if _, err := land.CallUnitHead(f.ctx, f.client, land.UnitHeadParams{
		Sprint: f.sprint, Unit: parent, Repo: f.repo, Base: f.base, Branch: "card-3011",
		Head: sha(3011), BaseSHA: b3Tip, Files: "cmd/p/p.go", Author: "emma",
	}); err != nil {
		t.Fatalf("parent head: %v", err)
	}

	_, _, err := land.CallBatchPlan(f.ctx, f.client, f.sprint, f.repo, f.base, "b-l24-child", f.lease,
		child+"@"+sha(3053), "", "go", b3Tip, "in")
	wantRefused(t, err, "stack-parent="+parent)

	b := newBatcher(f, nil)
	rep, err := b.Plan(f.ctx)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if len(rep.Planned) != 0 {
		t.Fatalf("child planned before its parent was landable: %+v", rep.Planned)
	}

	if _, err := land.CallUnitEval(f.ctx, f.client, f.sprint, parent, f.repo, f.base, 0); err != nil {
		t.Fatalf("parent eval: %v", err)
	}
	_, _, err = land.CallBatchPlan(f.ctx, f.client, f.sprint, f.repo, f.base, "b-l24-order", f.lease,
		child+"@"+sha(3053)+","+parent+"@"+sha(3011), "", "go", b3Tip, "in")
	wantRefused(t, err, "stack-parent="+parent)

	rep, err = b.Plan(f.ctx)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	pos := map[string][2]int{}
	for bi, p := range rep.Planned {
		for mi, m := range p.Members {
			pos[strings.SplitN(m, "@", 2)[0]] = [2]int{bi, mi}
		}
	}
	pp, okp := pos[parent]
	cp, okc := pos[child]
	if !okp || !okc {
		t.Fatalf("want both planned, got %+v", rep.Planned)
	}
	if cp[0] < pp[0] || (cp[0] == pp[0] && cp[1] < pp[1]) {
		t.Fatalf("child at %v planned before parent at %v", cp, pp)
	}
}

// TestL26 verifies control L26 (Issue #3139 rev 7 §11): a base conflict is dropped
// with one rebase task and re-offered only on a new head; a conflict ahead waits
// without a drop.
func TestL26(t *testing.T) {
	f := newLandFixture(t, "nova-tools", "dev")
	m := &fakeMerge{conflict: map[string]bool{}}
	b := newBatcher(f, m)

	front := addUnit(t, f, 500, sha(500), "internal/front/f.go", "", "emma")
	rep, err := b.Plan(f.ctx)
	if err != nil || len(rep.Planned) != 1 {
		t.Fatalf("front plan: %+v %v", rep, err)
	}
	t1 := sha(9500)
	gate(t, f, rep.Planned[0].ID, "GREEN", t1)
	_ = front

	base := addUnit(t, f, 501, sha(501), "internal/a/a.go", "", "stella")
	ahead := addUnit(t, f, 502, sha(502), "internal/front/f.go,internal/b/b.go", "", "emma")
	m.conflict[b3Tip+" "+sha(501)] = true // conflicts with the base
	m.conflict[t1+" "+sha(502)] = true    // clean on the base, conflicts with the batch ahead

	for tick := 0; tick < 5; tick++ {
		rep, err := b.Plan(f.ctx)
		if err != nil {
			t.Fatalf("tick %d: %v", tick, err)
		}
		if len(rep.Planned) != 0 {
			t.Fatalf("tick %d planned %+v, want nothing", tick, rep.Planned)
		}
	}
	if st := unitField(t, f, base, "state"); st != "dropped" || unitField(t, f, base, "drop_reason") != "conflict" {
		t.Fatalf("base-conflict unit state %q reason %q, want dropped conflict", st, unitField(t, f, base, "drop_reason"))
	}
	tasks, err := f.client.XRange(f.ctx, "q:stella", "-", "+").Result()
	if err != nil {
		t.Fatalf("q:stella: %v", err)
	}
	if len(tasks) != 1 || fmt.Sprint(tasks[0].Values["kind"]) != "rebase" {
		t.Fatalf("rebase tasks %v, want exactly one", tasks)
	}
	if st := unitField(t, f, ahead, "state"); st != "landable" {
		t.Fatalf("ahead-conflict unit state %q, want landable (waits, no drop)", st)
	}
	if n, _ := f.client.XLen(f.ctx, "q:emma").Result(); n != 0 {
		t.Fatalf("ahead conflict queued %d tasks, want 0", n)
	}

	// Same head: never re-offered.
	if _, err := land.CallUnitEval(f.ctx, f.client, f.sprint, base, f.repo, f.base, 0); err == nil {
		t.Fatal("dropped unit re-offered at the same head")
	}
	// New head: re-offered and planned.
	if _, err := land.CallUnitHead(f.ctx, f.client, land.UnitHeadParams{
		Sprint: f.sprint, Unit: base, Repo: f.repo, Base: f.base, Branch: "card-501",
		Head: sha(5011), BaseSHA: b3Tip, Files: "internal/a/a.go", Author: "stella",
	}); err != nil {
		t.Fatalf("new head: %v", err)
	}
	if _, err := land.CallUnitEval(f.ctx, f.client, f.sprint, base, f.repo, f.base, 0); err != nil {
		t.Fatalf("new head eval: %v", err)
	}
	rep, err = b.Plan(f.ctx)
	if err != nil {
		t.Fatalf("plan after new head: %v", err)
	}
	if len(rep.Planned) != 1 || rep.Planned[0].Members[0] != base+"@"+sha(5011) {
		t.Fatalf("planned %+v, want %s at its new head", rep.Planned, base)
	}
}

// TestL27 verifies control L27 (Issue #3139 rev 7 §11): a member without a unit id
// is refused at plan.
func TestL27(t *testing.T) {
	f := newLandFixture(t, "nova-tools", "dev")
	addUnit(t, f, 3053, sha(3053), "cmd/c/c.go", "", "emma")
	for _, m := range []string{"3053@" + sha(3053), "nova-tools#3053@" + sha(3053), "@" + sha(3053)} {
		_, _, err := land.CallBatchPlan(f.ctx, f.client, f.sprint, f.repo, f.base, "b-l27", f.lease, m, "", "go", b3Tip, "in")
		wantRefused(t, err, "no unit")
	}
	if n := gatesLen(t, f); n != 0 {
		t.Fatalf("refused plans queued %d gates", n)
	}
}

// TestGitMergeTree runs the real merge-tree pre-check on a throwaway repository.
func TestGitMergeTree(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	dir := t.TempDir()
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t", "GIT_CONFIG_GLOBAL=/dev/null")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run("init", "-q", "-b", "dev")
	write("f.txt", "a\n")
	run("add", ".")
	run("commit", "-qm", "base")
	root := run("rev-parse", "HEAD")
	write("f.txt", "tip\n")
	run("commit", "-qam", "tip")
	tip := run("rev-parse", "HEAD")
	run("checkout", "-qb", "c", root)
	write("f.txt", "child\n")
	run("commit", "-qam", "conflicting")
	conflicting := run("rev-parse", "HEAD")
	run("checkout", "-qb", "d", root)
	write("g.txt", "g\n")
	run("add", ".")
	run("commit", "-qm", "clean")
	clean := run("rev-parse", "HEAD")

	g := land.GitMergeTree{Dir: dir}
	if c, err := g.Conflicts(context.Background(), tip, conflicting); err != nil || !c {
		t.Fatalf("conflicting head: conflict=%v err=%v, want true", c, err)
	}
	if c, err := g.Conflicts(context.Background(), tip, clean); err != nil || c {
		t.Fatalf("clean head: conflict=%v err=%v, want false", c, err)
	}
}

// TestBatcherLeaseLost verifies HOLD 5 item 1 on #3533: ns_batch_bind, ns_unit_drop and
// ns_chain_void check the writer gen and the publisher lease like ns_batch_plan. A batcher that
// lost the lease (a new token, or a writer gen bump) cannot bind, drop or void; the chain, the
// units and the author queues are unchanged, and the new lease holder does all three.
func TestBatcherLeaseLost(t *testing.T) {
	for _, tc := range []struct {
		name, reason string
		lose         func(t *testing.T, f *landTestFixture) string // returns the new holder's lease
	}{
		{"token", "lease mismatch", func(t *testing.T, f *landTestFixture) string {
			v := fmt.Sprintf("%d:lease-token-2", f.gen)
			if err := f.client.Set(f.ctx, land.LeaseKey(f.repo, f.base), v, 0).Err(); err != nil {
				t.Fatalf("new lease: %v", err)
			}
			return v
		}},
		{"gen", "lease gen mismatch", func(t *testing.T, f *landTestFixture) string {
			// The writer gen moves on while the lease key still holds the stale batcher's value;
			// the new holder's lease is set after the stale checks.
			gen, err := land.CallWriter(f.ctx, f.client, f.repo, f.base, "nova-sprint", "takeover")
			if err != nil {
				t.Fatalf("writer: %v", err)
			}
			return fmt.Sprintf("%d:lease-token-1", gen)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newLandFixture(t, "nova-tools", "dev")
			for i := 0; i < 4; i++ {
				n := 600 + i
				addUnit(t, f, n, sha(n), fmt.Sprintf("internal/q%d/f.go", i), "", "emma")
			}
			b := newBatcher(f, nil)
			b.BatchMax = 2
			rep, err := b.Plan(f.ctx)
			if err != nil || len(rep.Planned) != 2 {
				t.Fatalf("plan: %+v %v", rep, err)
			}
			ids := []string{rep.Planned[0].ID, rep.Planned[1].ID}
			t1 := sha(9600)
			gate(t, f, ids[0], "GREEN", t1)
			spare := addUnit(t, f, 610, sha(610), "internal/spare/f.go", "", "stella")

			newLease := tc.lose(t, f)

			// Bind: batch 2's parent has a train head, but the stale batcher may not bind it.
			rep, err = b.Plan(f.ctx)
			if err != nil {
				t.Fatalf("stale plan: %v", err)
			}
			if len(rep.Bound) != 0 || len(rep.Planned) != 0 {
				t.Fatalf("stale batcher bound %v planned %+v, want nothing", rep.Bound, rep.Planned)
			}
			if h := batchHash(t, f, ids[1]); h["from_tip"] != "" {
				t.Fatalf("stale bind wrote from_tip %q", h["from_tip"])
			}
			// Drop: refused, the unit stays landable and no task is queued.
			if ok, err := b.Drop(f.ctx, spare, sha(610), "conflict", "rebase"); err == nil || ok || !strings.Contains(err.Error(), tc.reason) {
				t.Fatalf("stale drop: ok=%v err=%v, want REFUSED %s", ok, err, tc.reason)
			}
			if st := unitField(t, f, spare, "state"); st != "landable" {
				t.Fatalf("stale drop left %s %q, want landable", spare, st)
			}
			if n, _ := f.client.XLen(f.ctx, "q:stella").Result(); n != 0 {
				t.Fatalf("stale drop queued %d tasks", n)
			}
			// Void: refused, the chain keeps both batches.
			if voided, err := b.VoidRed(f.ctx, ids[0]); err == nil || !strings.Contains(err.Error(), tc.reason) {
				t.Fatalf("stale void: %v %v, want REFUSED %s", voided, err, tc.reason)
			}
			if chain, _ := f.client.ZRange(f.ctx, land.ChainKey(f.repo, f.base), 0, -1).Result(); strings.Join(chain, ",") != ids[0]+","+ids[1] {
				t.Fatalf("chain after stale void %v, want %v", chain, ids)
			}
			if h := batchHash(t, f, ids[1]); h["state"] != "queued" {
				t.Fatalf("batch %s state %q after stale void, want queued", ids[1], h["state"])
			}

			if tc.name == "gen" {
				if err := f.client.Set(f.ctx, land.LeaseKey(f.repo, f.base), newLease, 0).Err(); err != nil {
					t.Fatalf("lease: %v", err)
				}
			}
			// The new holder drops, binds and voids.
			nb := newBatcher(f, nil)
			nb.BatchMax, nb.Lease = 2, newLease
			if ok, err := nb.Drop(f.ctx, spare, sha(610), "conflict", "rebase"); err != nil || !ok {
				t.Fatalf("holder drop: %v %v", ok, err)
			}
			if n, _ := f.client.XLen(f.ctx, "q:stella").Result(); n != 1 {
				t.Fatalf("holder drop queued %d tasks, want 1", n)
			}
			rep, err = nb.Plan(f.ctx)
			if err != nil || strings.Join(rep.Bound, ",") != ids[1] || len(rep.Planned) != 0 {
				t.Fatalf("holder plan: bound %v planned %+v err %v, want bound [%s]", rep.Bound, rep.Planned, err, ids[1])
			}
			if voided, err := nb.VoidRed(f.ctx, ids[0]); err != nil || len(voided) != 1 || voided[0] != ids[1] {
				t.Fatalf("holder void: %v %v, want [%s]", voided, err, ids[1])
			}
		})
	}
}

// TestBatcherNoTip verifies HOLD 5 item 2 on #3533: with an empty chain and no tip record the
// batcher plans nothing (a front batch with no from_tip and no parent could never be bound) and
// reports the landable units waiting on "no tip"; once the tip is recorded it plans.
func TestBatcherNoTip(t *testing.T) {
	f := newLandFixture(t, "nova-tools", "dev")
	if err := f.client.Del(f.ctx, land.TipKey(f.repo, f.base)).Err(); err != nil {
		t.Fatalf("del tip: %v", err)
	}
	u := addUnit(t, f, 700, sha(700), "internal/t/f.go", "", "emma")
	b := newBatcher(f, nil)
	rep, err := b.Plan(f.ctx)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if len(rep.Planned) != 0 || gatesLen(t, f) != 0 {
		t.Fatalf("planned %+v with no tip, want nothing", rep.Planned)
	}
	if strings.Join(rep.Waiting, ";") != u+" no tip" {
		t.Fatalf("waiting %v, want [%s no tip]", rep.Waiting, u)
	}
	if st := unitField(t, f, u, "state"); st != "landable" {
		t.Fatalf("unit state %q, want landable", st)
	}
	if err := f.client.HSet(f.ctx, land.TipKey(f.repo, f.base), "sha", b3Tip).Err(); err != nil {
		t.Fatalf("set tip: %v", err)
	}
	rep, err = b.Plan(f.ctx)
	if err != nil || len(rep.Planned) != 1 || rep.Planned[0].FromTip != b3Tip {
		t.Fatalf("plan with tip: %+v %v", rep, err)
	}
}
