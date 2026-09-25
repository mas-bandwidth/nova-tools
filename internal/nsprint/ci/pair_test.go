package ci_test

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/civerdict"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ci"
)

// TestCIAttemptsImmutableTwoBasesTwoAttempts is nova-tools #3148's pair
// clause on the #3139 receipts: every (head, tested base) is its own ci
// unit. One head H is cut at two base tips B1 and B2: each is CREATED with
// its own label ci-<pr>-<head8>-<base8> (the head-only label answered EXISTS
// to the second and no verdict was ever written for the new tip); a re-cut
// of the same pair is EXISTS and an 8-hex prefix clash is CONFLICT (exit 4),
// neither writing anything, and no cut writes a ci: key. B2's
// FAIL, rerun, FLAKY, disposition and verdict never touch B1's card or its
// write-once receipt, and the disposition finds B2's card from the item.
func TestCIAttemptsImmutableTwoBasesTwoAttempts(t *testing.T) {
	t.Parallel()

	const (
		b1 = base
		b2 = "4444444444444444444444444444444444444444"
		b3 = "4444444455555555555555555555555555555555" // b2's first 8 hex
	)
	f := newFixture(t, "ctl-a", "ctl-b")
	cut := func(b string) ci.Result {
		t.Helper()
		r, err := ci.Cut(f.ctx, f.st, ci.CutRequest{Sprint: f.sprint, Repo: repo, PR: pr, Head: head, Base: b, BaseRef: "dev", Actor: "ctl"})
		if err != nil {
			t.Fatalf("cut at %s: %v", b[:8], err)
		}
		return r
	}
	dbsize := func() int64 {
		t.Helper()
		n, err := f.client.DBSize(f.ctx).Result()
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	label1 := fmt.Sprintf("ci-%d-%s-%s", pr, head[:8], b1[:8])
	label2 := fmt.Sprintf("ci-%d-%s-%s", pr, head[:8], b2[:8])
	if got := ci.Label(pr, head, b1); got != label1 {
		t.Fatalf("Label = %q; want %q", got, label1)
	}

	if r := cut(b1); r.Status != "CREATED" {
		t.Fatalf("cut at B1 = %v; want CREATED", r)
	}
	if r := cut(b2); r.Status != "CREATED" {
		t.Fatalf("cut at B2 = %v; want CREATED with its own label (the head-only label answers EXISTS)", r)
	}
	for l, b := range map[string]string{label1: b1, label2: b2} {
		c := f.card(l)
		if c["ci_head"] != head || c["base_sha"] != b || c["attempt"] != "1" || c["verdict"] != ci.Pending {
			t.Fatalf("card %s = head %q base %q attempt %q verdict %q; want its own pair at attempt 1 PENDING",
				l, c["ci_head"], c["base_sha"], c["attempt"], c["verdict"])
		}
	}
	card2 := f.card(label2)
	if keys, _ := f.client.Keys(f.ctx, "ci:*").Result(); len(keys) != 0 {
		t.Fatalf("the cuts wrote ci: keys %v; want none until a card ends", keys)
	}

	n := dbsize()
	if r := cut(b1); r.Status != "EXISTS" || r.ExitCode() != ci.ExitOK {
		t.Fatalf("re-cut at B1 = %v; want EXISTS exit 0", r)
	}
	if r := cut(b3); r.Status != "CONFLICT" || r.ExitCode() != ci.ExitConflict {
		t.Fatalf("cut at B3 (B2's 8-hex prefix) = %v; want CONFLICT exit 4", r)
	}
	if got := dbsize(); got != n {
		t.Fatalf("EXISTS and CONFLICT changed DBSIZE %d -> %d; want no write", n, got)
	}
	if got := f.card(label2); !reflect.DeepEqual(got, card2) {
		t.Fatalf("CONFLICT at B2's label rewrote its card: %v", got)
	}

	// B1 ends OK: its write-once receipt at the B1 gid.
	token, identity := f.deal(label1, "ctl-a")
	if r := f.end(label1, token, identity, "DONE", "done", ci.OK, "", ""); r.Status != "ENDED" || r.Detail != ci.OK {
		t.Fatalf("B1 end = %v; want ENDED OK", r)
	}
	gid := func(b string) string {
		g, err := civerdict.Expected(f.ctx, f.client, repo, "dev", b)
		if err != nil {
			t.Fatal(err)
		}
		return g
	}
	rec1Key := civerdict.Key(repo, head, gid(b1))
	rec1, _ := f.client.HGetAll(f.ctx, rec1Key).Result()
	if rec1["verdict"] != ci.OK || rec1["card"] != f.sprint+"/"+label1 {
		t.Fatalf("B1 receipt = %v; want OK from %s", rec1, label1)
	}
	card1 := f.card(label1)
	b1Unchanged := func(step string) {
		t.Helper()
		if got, _ := f.client.HGetAll(f.ctx, rec1Key).Result(); !reflect.DeepEqual(got, rec1) {
			t.Fatalf("%s rewrote the B1 receipt: %v", step, got)
		}
		if got := f.card(label1); !reflect.DeepEqual(got, card1) {
			t.Fatalf("%s rewrote the B1 card: %v", step, got)
		}
		if ok, why := f.landReady(); !ok {
			t.Fatalf("after %s the tip-B1 head is not land-ready: %s", step, why)
		}
	}
	b1Unchanged("B1 end")

	// B2: FAIL, rerun on the other bench, OK there is FLAKY; the typed
	// disposition finds B2's card and its rerun writes the B2 receipt.
	token, identity = f.deal(label2, "ctl-a")
	if r := f.end(label2, token, identity, "DONE", "done", ci.Fail, "internal/x", "TestX"); r.Detail != ci.Fail {
		t.Fatalf("B2 end = %v; want FAIL", r)
	}
	b1Unchanged("B2 FAIL")
	if r, err := ci.Rerun(f.ctx, f.st, f.sprint, label2, "ctl", "pair control"); err != nil || r.Status != "RERUN" || r.Attempt != 2 {
		t.Fatalf("B2 rerun = %v, %v; want RERUN attempt 2", r, err)
	}
	b1Unchanged("B2 rerun")
	token, identity = f.deal(label2, "ctl-b")
	if r := f.end(label2, token, identity, "DONE", "done", ci.OK, "", ""); r.Detail != ci.Flaky {
		t.Fatalf("B2 OK after FAIL = %v; want FLAKY", r)
	}
	b1Unchanged("B2 FLAKY")
	if r, err := ci.Dispose(f.ctx, f.st, f.sprint, repo, head, "APPROVE", "stella", "https://example.test/disp"); err != nil || r.Status != "APPROVE" {
		t.Fatalf("dispose = %v, %v; want APPROVE on B2's card", r, err)
	}
	if c := f.card(label2); c["attempt"] != "3" || c["verdict"] != ci.Pending || c["disp"] != "1" {
		t.Fatalf("B2 card after APPROVE = attempt %q verdict %q disp %q; want 3 PENDING 1", c["attempt"], c["verdict"], c["disp"])
	}
	b1Unchanged("B2 dispose")
	token, identity = f.deal(label2, "ctl-a")
	if r := f.end(label2, token, identity, "DONE", "done", ci.OK, "", ""); r.Detail != ci.OK {
		t.Fatalf("B2 disposition rerun end = %v; want OK", r)
	}
	b1Unchanged("B2 end")
	rec2, _ := f.client.HGetAll(f.ctx, civerdict.Key(repo, head, gid(b2))).Result()
	if rec2["verdict"] != ci.OK || rec2["card"] != f.sprint+"/"+label2 || rec2["attempt"] != "3" || rec2["base_sha"] != b2 {
		t.Fatalf("B2 receipt = %v; want OK from %s attempt 3 at B2", rec2, label2)
	}
	if n := f.logCount("ci end", ""); n != 4 {
		t.Fatalf("ci end receipts = %d; want 4 (one B1, three B2 attempts)", n)
	}
}
