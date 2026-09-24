package publish_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/land"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/land/publish"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/redis/go-redis/v9"
)

// The publisher controls of #3139 rev 7 section 11 (B7), each run against a
// throwaway redis-server and testutil's local bare remote, whose hooks refuse
// any non-fast-forward (the dev-integrity ruleset). Nothing here reaches a host.

var errKilled = errors.New("killed at a kill point")

type fx struct {
	t      *testing.T
	ctx    context.Context
	rdb    *redis.Client
	sprint string
	repo   string
	base   string
	gen    int64
	remote *testutil.LocalRemote
	work   string // the publisher's mirror; member branches are cut here
	tip    string // dev at the start
	n      int
}

func gitEnv() []string {
	return append(os.Environ(),
		"GIT_AUTHOR_NAME=Fixture", "GIT_AUTHOR_EMAIL=fixture@mas-bandwidth.com",
		"GIT_COMMITTER_NAME=Fixture", "GIT_COMMITTER_EMAIL=fixture@mas-bandwidth.com",
		"GIT_CONFIG_NOSYSTEM=1",
	)
}

func (f *fx) git(dir string, args ...string) string {
	f.t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "commit.gpgsign=false"}, args...)...)
	cmd.Env = gitEnv()
	out, err := cmd.CombinedOutput()
	if err != nil {
		f.t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func newFx(t *testing.T) *fx {
	t.Helper()
	addr := testutil.Start(t)
	rdb := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = rdb.Close() })
	ctx := context.Background()
	if err := fn.Load(ctx, rdb); err != nil {
		t.Fatalf("load fn library: %v", err)
	}
	f := &fx{t: t, ctx: ctx, rdb: rdb, sprint: "sprint-b7", repo: "nova-tools", base: "dev"}
	gen, err := land.CallWriter(ctx, rdb, f.repo, f.base, "nova-sprint", "fixture")
	if err != nil {
		t.Fatalf("writer: %v", err)
	}
	f.gen = gen
	f.takeLease("pub-a")
	if err := land.CallPolicySet(ctx, rdb, f.repo, f.base, "pol-1", "req-1", "runner-1"); err != nil {
		t.Fatalf("policy: %v", err)
	}

	f.remote = testutil.NewLocalRemote(t, f.base)
	f.work = filepath.Join(t.TempDir(), "mirror")
	if out, err := exec.Command("git", "clone", "--quiet", f.remote.URL, f.work).CombinedOutput(); err != nil {
		t.Fatalf("clone: %v\n%s", err, out)
	}
	f.git(f.work, "checkout", "-q", "-b", f.base)
	f.write(f.work, "base.txt", "base\n")
	f.git(f.work, "add", "base.txt")
	f.git(f.work, "commit", "-q", "-m", "dev base")
	f.git(f.work, "push", "-q", "origin", f.base)
	f.tip = f.git(f.work, "rev-parse", "HEAD")
	if err := rdb.HSet(ctx, land.TipKey(f.repo, f.base), "sha", f.tip, "at", "1", "by", "fixture").Err(); err != nil {
		t.Fatalf("tip: %v", err)
	}
	return f
}

func (f *fx) write(dir, name, body string) {
	f.t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		f.t.Fatal(err)
	}
}

// lease is the value a publisher holding the lease under token tok carries.
func (f *fx) lease(tok string) string { return fmt.Sprintf("%d:%s", f.gen, tok) }

// takeLease is a lease expiry plus a new holder: the key now names tok.
func (f *fx) takeLease(tok string) string {
	f.t.Helper()
	v := f.lease(tok)
	if err := f.rdb.Set(f.ctx, land.LeaseKey(f.repo, f.base), v, 0).Err(); err != nil {
		f.t.Fatalf("lease: %v", err)
	}
	return v
}

// member cuts one unit on its own branch from the starting tip, pushes it and
// makes it landable.
func (f *fx) member() (unit, head string) {
	f.t.Helper()
	f.n++
	unit = fmt.Sprintf("gh/mas-bandwidth/nova-tools/%d", 7000+f.n)
	branch := fmt.Sprintf("card-%d", f.n)
	f.git(f.work, "checkout", "-q", "-b", branch, f.tip)
	f.write(f.work, fmt.Sprintf("m%d.txt", f.n), fmt.Sprintf("member %d\n", f.n))
	f.git(f.work, "add", ".")
	f.git(f.work, "commit", "-q", "-m", "member "+strconv.Itoa(f.n))
	head = f.git(f.work, "rev-parse", "HEAD")
	f.git(f.work, "push", "-q", "origin", branch)
	f.git(f.work, "checkout", "-q", "--detach", f.tip)
	if _, err := land.CallUnitHead(f.ctx, f.rdb, land.UnitHeadParams{
		Sprint: f.sprint, Unit: unit, Repo: f.repo, Base: f.base, Branch: branch,
		Head: head, BaseSHA: f.tip, Author: "emma",
	}); err != nil {
		f.t.Fatalf("unit head: %v", err)
	}
	if _, err := land.CallUnitEval(f.ctx, f.rdb, f.sprint, unit, f.repo, f.base, 0); err != nil {
		f.t.Fatalf("unit eval: %v", err)
	}
	return unit, head
}

// plan puts one batch on the chain from the starting tip.
func (f *fx) plan(batch string, members ...string) string {
	f.t.Helper()
	token, _, err := land.CallBatchPlan(f.ctx, f.rdb, f.sprint, f.repo, f.base, batch,
		f.lease("pub-a"), strings.Join(members, ","), "", "go", f.tip, "in-"+batch)
	if err != nil {
		f.t.Fatalf("plan %s: %v", batch, err)
	}
	return token
}

// gate plays the worker: the deterministic train from the mirror, a GREEN receipt.
func (f *fx) gate(batch, token string) *publish.TrainResult {
	f.t.Helper()
	b, err := f.rdb.HMGet(f.ctx, land.BatchKey(f.repo, f.base, batch), "from_tip", "members", "created_at").Result()
	if err != nil {
		f.t.Fatal(err)
	}
	var heads []string
	for _, m := range strings.Split(fmt.Sprint(b[1]), ",") {
		heads = append(heads, m[strings.Index(m, "@")+1:])
	}
	tr, err := publish.BuildTrain(f.ctx, publish.TrainParams{
		GitDir: f.work, FromTip: fmt.Sprint(b[0]), Members: heads, BatchID: batch, CreatedAt: fmt.Sprint(b[2]),
	})
	if err != nil {
		f.t.Fatalf("train: %v", err)
	}
	if r, err := land.CallGateClaim(f.ctx, f.rdb, f.repo, f.base, batch, 1, token, "bench-1", "slot-1"); err != nil || r != "OK" {
		f.t.Fatalf("claim: %s %v", r, err)
	}
	if r, err := land.CallGateReceipt(f.ctx, f.rdb, f.repo, f.base, batch, 1, token, "GREEN", "bench-1", "w1",
		tr.TrainHead, tr.TrainTree, "in-"+batch, "", "", "", "", "1"); err != nil || r != "OK" {
		f.t.Fatalf("receipt: %s %v", r, err)
	}
	return tr
}

func (f *fx) publisher(lease string, hook func(publish.Point) error) *publish.Publisher {
	return publish.New(publish.Config{
		Redis: f.rdb, Sprint: f.sprint, Repo: f.repo, Base: f.base, Lease: lease,
		GitDir: f.work, Remote: "origin",
		Sleep: func(context.Context, time.Duration) error { return nil },
		Hook:  hook,
	})
}

func (f *fx) land(p *publish.Publisher) publish.Result {
	f.t.Helper()
	f.fresh()
	r, err := p.LandFront(f.ctx)
	if err != nil {
		f.t.Fatalf("land front: %v", err)
	}
	return r
}

func (f *fx) events(name string) int {
	f.t.Helper()
	es, err := f.rdb.XRange(f.ctx, land.EventsStream(f.repo), "-", "+").Result()
	if err != nil {
		f.t.Fatal(err)
	}
	n := 0
	for _, e := range es {
		if fmt.Sprint(e.Values["event"]) == name {
			n++
		}
	}
	return n
}

func (f *fx) remoteTip() string {
	f.t.Helper()
	out, err := exec.Command("git", "--git-dir", f.remote.Dir, "rev-parse", "refs/heads/"+f.base).CombinedOutput()
	if err != nil {
		f.t.Fatalf("remote tip: %v\n%s", err, out)
	}
	return strings.TrimSpace(string(out))
}

func (f *fx) hget(key, field string) string {
	v, _ := f.rdb.HGet(f.ctx, key, field).Result()
	return v
}

// handPush is a push to the base the lander did not make: a fast-forward from
// a second clone.
func (f *fx) handPush() string {
	f.t.Helper()
	hand := filepath.Join(f.t.TempDir(), "hand")
	if out, err := exec.Command("git", "clone", "--quiet", "--branch", f.base, f.remote.URL, hand).CombinedOutput(); err != nil {
		f.t.Fatalf("clone hand: %v\n%s", err, out)
	}
	f.write(hand, "hand.txt", "by hand\n")
	f.git(hand, "add", "hand.txt")
	f.git(hand, "commit", "-q", "-m", "a push the lander did not make")
	f.git(hand, "push", "-q", "origin", f.base)
	return f.git(hand, "rev-parse", "HEAD")
}

// landedOnce is the one-accepted-publication check: the remote base is the
// train head, the tip record follows it, every member carries its landed key,
// exactly one LANDED, no void, no re-gate, the intent resolved.
func (f *fx) landedOnce(batch string, tr *publish.TrainResult, units, heads []string) {
	f.t.Helper()
	if got := f.remoteTip(); got != tr.TrainHead {
		f.t.Fatalf("remote %s = %s, want train head %s", f.base, got, tr.TrainHead)
	}
	if got := f.hget(land.TipKey(f.repo, f.base), "sha"); got != tr.TrainHead {
		f.t.Fatalf("tip record %s, want %s", got, tr.TrainHead)
	}
	for i, u := range units {
		v, err := f.rdb.Get(f.ctx, land.LandedKey(f.repo, u, heads[i])).Result()
		if err != nil || !strings.HasPrefix(v, tr.Commits[i]+" "+batch) {
			f.t.Fatalf("landed %s: %q %v, want merge %s in %s", u, v, err, tr.Commits[i], batch)
		}
	}
	if n := f.events("LANDED"); n != 1 {
		f.t.Fatalf("LANDED events %d, want 1", n)
	}
	if n := f.events("VOID") + f.events("REGATE"); n != 0 {
		f.t.Fatalf("VOID+REGATE events %d, want 0", n)
	}
	if n := f.events("GATE"); n != 1 {
		f.t.Fatalf("GATE events %d, want 1 (a re-gate happened)", n)
	}
	if got := f.hget(land.BatchKey(f.repo, f.base, batch), "state"); got != "landed" {
		f.t.Fatalf("batch state %s, want landed", got)
	}
	if n, _ := f.rdb.Exists(f.ctx, land.PubBatchKey(f.repo, f.base, batch), land.PubBatchKey(f.repo, f.base, "active")).Result(); n != 0 {
		f.t.Fatalf("intent not resolved: %d pub keys remain", n)
	}
}

// TestL6: ns_land twice for one batch writes nothing the second time, driven
// through the publisher: a second publisher resolves the same verified intent
// first, the first one's ns_land is ALREADY, and a later tick lands nothing.
func TestL6(t *testing.T) {
	t.Parallel()
	f := newFx(t)
	u1, h1 := f.member()
	u2, h2 := f.member()
	tok := f.plan("b6", u1+"@"+h1, u2+"@"+h2)
	tr := f.gate("b6", tok)

	lease := f.lease("pub-a")
	var second publish.Result
	a := f.publisher(lease, func(pt publish.Point) error {
		if pt == publish.AfterVerify {
			second = f.land(f.publisher(lease, nil))
		}
		return nil
	})
	first := f.land(a)
	if second.Outcome != publish.Landed {
		t.Fatalf("second publisher: %+v, want LANDED", second)
	}
	if first.Outcome != publish.Already {
		t.Fatalf("first publisher: %+v, want ALREADY", first)
	}
	before, _ := f.rdb.Get(f.ctx, land.LandedKey(f.repo, u1, h1)).Result()
	f.landedOnce("b6", tr, []string{u1, u2}, []string{h1, h2})

	again := f.land(f.publisher(lease, nil))
	if again.Outcome != publish.Idle || again.Pushes != 0 {
		t.Fatalf("a later tick: %+v, want IDLE with 0 pushes", again)
	}
	after, _ := f.rdb.Get(f.ctx, land.LandedKey(f.repo, u1, h1)).Result()
	if before != after {
		t.Fatalf("landed key rewritten: %q -> %q", before, after)
	}
	f.landedOnce("b6", tr, []string{u1, u2}, []string{h1, h2})
}

// TestL11: a kill at each of K1-K6 ends with one accepted publication, no
// lost work and no re-gate. K1 and K2 are the worker's side of the receipt;
// K3-K6 kill the publisher and a new lease holder takes over.
func TestL11(t *testing.T) {
	t.Parallel()
	for _, k := range []struct {
		name string
		at   publish.Point // "" = no publisher kill
		pre  bool          // K1: the publisher runs before the receipt exists
	}{
		{"K1-before-receipt", "", true},
		{"K2-after-receipt", "", false},
		{"K3-after-intent", publish.AfterIntent, false},
		{"K4-after-push-before-verify", publish.AfterPush, false},
		{"K5-after-verify-before-ns_land", publish.AfterVerify, false},
		{"K6-after-ns_land", publish.AfterLand, false},
	} {
		t.Run(k.name, func(t *testing.T) {
			t.Parallel()
			f := newFx(t)
			u, h := f.member()
			tok := f.plan("b11", u+"@"+h)
			if k.pre {
				r := f.land(f.publisher(f.lease("pub-a"), nil))
				if r.Outcome != publish.Wait || r.Pushes != 0 {
					t.Fatalf("before the receipt: %+v, want WAIT with 0 pushes", r)
				}
			}
			tr := f.gate("b11", tok)
			if k.at != "" {
				a := f.publisher(f.lease("pub-a"), func(pt publish.Point) error {
					if pt == k.at {
						return errKilled
					}
					return nil
				})
				f.fresh()
				if _, err := a.LandFront(f.ctx); !errors.Is(err, errKilled) {
					t.Fatalf("kill at %s: err %v, want the kill", k.at, err)
				}
			}
			b := f.land(f.publisher(f.takeLease("pub-b"), nil))
			want := publish.Landed
			if k.at == publish.AfterLand {
				want = publish.Idle
			}
			if b.Outcome != want {
				t.Fatalf("takeover after %s: %+v, want %s", k.name, b, want)
			}
			f.landedOnce("b11", tr, []string{u}, []string{h})
		})
	}
}

// TestL19: nova-secrets exits 125 (the 9:02 AM outage): the intent stays, the
// push retries on the next tick, nothing drops or re-gates.
func TestL19(t *testing.T) {
	t.Parallel()
	f := newFx(t)
	u, h := f.member()
	tok := f.plan("b19", u+"@"+h)
	tr := f.gate("b19", tok)

	dir := t.TempDir()
	count := filepath.Join(dir, "count")
	fake := filepath.Join(dir, "nova-secrets")
	script := "#!/bin/sh\n" +
		"n=$(cat " + strconv.Quote(count) + " 2>/dev/null || echo 0); n=$((n+1)); echo $n > " + strconv.Quote(count) + "\n" +
		"if [ \"$n\" -le 2 ]; then echo 'nova-secrets: sops decrypt failed' >&2; exit 125; fi\n" +
		"while [ \"$#\" -gt 0 ]; do a=\"$1\"; shift; [ \"$a\" = \"--\" ] && break; done\n" +
		"exec \"$@\"\n"
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	p := publish.New(publish.Config{
		Redis: f.rdb, Sprint: f.sprint, Repo: f.repo, Base: f.base, Lease: f.lease("pub-a"),
		GitDir: f.work, Remote: "origin",
		PushPrefix: []string{fake, "exec", "--as", "nova-lander", "--"},
		Sleep:      func(context.Context, time.Duration) error { return nil },
	})
	for i := 1; i <= 2; i++ {
		r := f.land(p)
		if r.Outcome != publish.Retry || r.Pushes != 1 {
			t.Fatalf("tick %d with nova-secrets at 125: %+v, want RETRY after 1 push", i, r)
		}
		if st := f.hget(land.PubBatchKey(f.repo, f.base, "b19"), "state"); st != "intent" {
			t.Fatalf("tick %d: intent state %q, want intent (kept)", i, st)
		}
		if st := f.hget(land.BatchKey(f.repo, f.base, "b19"), "state"); st != "green" {
			t.Fatalf("tick %d: batch %q, want green (nothing dropped)", i, st)
		}
		if st := f.hget(land.UnitKey(f.sprint, u), "state"); st != "landing" {
			t.Fatalf("tick %d: unit %q, want landing", i, st)
		}
		if got := f.remoteTip(); got != f.tip {
			t.Fatalf("tick %d: remote moved to %s", i, got)
		}
	}
	if r := f.land(p); r.Outcome != publish.Landed {
		t.Fatalf("nova-secrets back: %+v, want LANDED", r)
	}
	if n, _ := os.ReadFile(count); strings.TrimSpace(string(n)) != "3" {
		t.Fatalf("credential calls %q, want 3", n)
	}
	if n := f.events("INTENT"); n != 1 {
		t.Fatalf("INTENT events %d, want 1 (the intent was re-cut)", n)
	}
	f.landedOnce("b19", tr, []string{u}, []string{h})
}

// TestL22: a push to the base the lander did not make. The compare-and-swap
// refuses, the chain voids for re-planning, and there is no retry loop.
func TestL22(t *testing.T) {
	t.Parallel()
	t.Run("remote-moved-under-the-tip-record", func(t *testing.T) {
		t.Parallel()
		f := newFx(t)
		u1, h1 := f.member()
		u2, h2 := f.member()
		tok := f.plan("b22a", u1+"@"+h1)
		f.plan("b22b", u2+"@"+h2)
		f.gate("b22a", tok)
		moved := f.handPush()

		r := f.land(f.publisher(f.lease("pub-a"), nil))
		if r.Outcome != publish.Dead || r.Pushes != 1 {
			t.Fatalf("CAS on a moved base: %+v, want DEAD after exactly 1 push", r)
		}
		if got := f.remoteTip(); got != moved {
			t.Fatalf("remote %s, want the hand push %s untouched", got, moved)
		}
		if got := f.hget(land.TipKey(f.repo, f.base), "sha"); got != moved {
			t.Fatalf("tip record %s, want %s", got, moved)
		}
		if got := f.hget(land.PubBatchKey(f.repo, f.base, "b22a"), "state"); got != "dead" {
			t.Fatalf("intent %q, want dead", got)
		}
		for _, b := range []string{"b22a", "b22b"} {
			if st := f.hget(land.BatchKey(f.repo, f.base, b), "state"); st != "void" {
				t.Fatalf("batch %s %q, want void", b, st)
			}
		}
		for _, u := range []string{u1, u2} {
			if st := f.hget(land.UnitKey(f.sprint, u), "state"); st != "landable" {
				t.Fatalf("unit %s %q, want landable (re-plan)", u, st)
			}
		}
		if n, _ := f.rdb.ZCard(f.ctx, land.ChainKey(f.repo, f.base)).Result(); n != 0 {
			t.Fatalf("chain holds %d, want 0", n)
		}
		if n := f.events("LANDED"); n != 0 {
			t.Fatalf("LANDED %d, want 0", n)
		}
		for i := 0; i < 3; i++ {
			if r := f.land(f.publisher(f.lease("pub-a"), nil)); r.Outcome != publish.Idle || r.Pushes != 0 {
				t.Fatalf("tick %d after DEAD: %+v, want IDLE with 0 pushes (no retry loop)", i, r)
			}
		}
	})
	t.Run("tip-record-already-moved", func(t *testing.T) {
		t.Parallel()
		f := newFx(t)
		u, h := f.member()
		tok := f.plan("b22c", u+"@"+h)
		f.gate("b22c", tok)
		moved := f.handPush()
		if err := f.rdb.HSet(f.ctx, land.TipKey(f.repo, f.base), "sha", moved, "by", "fetch").Err(); err != nil {
			t.Fatal(err)
		}
		r := f.land(f.publisher(f.lease("pub-a"), nil))
		if r.Outcome != publish.Voided || r.Pushes != 0 {
			t.Fatalf("front from_tip behind the tip record: %+v, want VOIDED with 0 pushes", r)
		}
		if n := f.events("INTENT"); n != 0 {
			t.Fatalf("INTENT %d, want 0", n)
		}
		if st := f.hget(land.BatchKey(f.repo, f.base, "b22c"), "state"); st != "void" {
			t.Fatalf("batch %q, want void", st)
		}
		if got := f.remoteTip(); got != moved {
			t.Fatalf("remote %s, want %s", got, moved)
		}
	})
}

// TestL22Fenced: the tip-moved void is a publisher write, so it carries the
// lease fence: a publisher whose lease was taken over, or whose writer
// generation is gone, voids nothing and gets FENCED (HOLD 5 on #3531, item 1).
func TestL22Fenced(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		stale func(f *fx) string
	}{
		{"lease-taken-over", func(f *fx) string {
			old := f.lease("pub-a")
			f.takeLease("pub-b")
			return old
		}},
		{"writer-generation-moved", func(f *fx) string {
			old := f.lease("pub-a")
			if _, err := land.CallWriter(f.ctx, f.rdb, f.repo, f.base, "nova-sprint", "fixture-2"); err != nil {
				f.t.Fatalf("writer: %v", err)
			}
			return old
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newFx(t)
			u, h := f.member()
			f.gate("b22f", f.plan("b22f", u+"@"+h))
			moved := f.handPush()
			if err := f.rdb.HSet(f.ctx, land.TipKey(f.repo, f.base), "sha", moved, "by", "fetch").Err(); err != nil {
				t.Fatal(err)
			}
			r := f.land(f.publisher(tc.stale(f), nil))
			if r.Outcome != publish.Fenced || r.Pushes != 0 {
				t.Fatalf("stale publisher on a moved tip: %+v, want FENCED with 0 pushes", r)
			}
			if st := f.hget(land.BatchKey(f.repo, f.base, "b22f"), "state"); st != "green" {
				t.Fatalf("batch %q, want green (the stale publisher voids nothing)", st)
			}
			if n, _ := f.rdb.ZCard(f.ctx, land.ChainKey(f.repo, f.base)).Result(); n != 1 {
				t.Fatalf("chain holds %d, want 1", n)
			}
			if n := f.events("VOID"); n != 0 {
				t.Fatalf("VOID %d, want 0", n)
			}
		})
	}
}

// TestL28: the paused publisher, cases (i)-(iv) of section 11.
func TestL28(t *testing.T) {
	t.Parallel()
	hold := func(f *fx, unit, head string) bool {
		f.t.Helper()
		_, post, err := land.CallHold(f.ctx, f.rdb, f.sprint, unit, "stella", head, "substantive", "late objection", "", "", "", "verb")
		if err != nil {
			f.t.Fatalf("hold: %v", err)
		}
		return post
	}
	t.Run("i-hold-after-the-intent-lands-post_land", func(t *testing.T) {
		t.Parallel()
		f := newFx(t)
		u, h := f.member()
		tr := f.gate("b28i", f.plan("b28i", u+"@"+h))
		var post bool
		r := f.land(f.publisher(f.lease("pub-a"), func(pt publish.Point) error {
			if pt == publish.AfterIntent {
				post = hold(f, u, h)
			}
			return nil
		}))
		if r.Outcome != publish.Landed {
			t.Fatalf("%+v, want LANDED", r)
		}
		if !post || f.hget(land.HoldKey(f.sprint, u, "stella"), "post_land") != "1" {
			t.Fatalf("hold after the cut: post_land=%v, want 1", post)
		}
		if n, _ := f.rdb.XLen(f.ctx, "q:stella").Result(); n != 1 {
			t.Fatalf("follow-up tasks on q:stella %d, want 1", n)
		}
		f.landedOnce("b28i", tr, []string{u}, []string{h})
	})
	t.Run("ii-hold-before-the-intent-refuses-nothing-pushed", func(t *testing.T) {
		t.Parallel()
		f := newFx(t)
		u, h := f.member()
		f.gate("b28ii", f.plan("b28ii", u+"@"+h))
		r := f.land(f.publisher(f.lease("pub-a"), func(pt publish.Point) error {
			if pt == publish.BeforeIntent {
				if hold(f, u, h) {
					t.Fatalf("hold before the cut marked post_land")
				}
			}
			return nil
		}))
		if r.Outcome != publish.Refused || r.Pushes != 0 || !strings.Contains(r.Line, "hold on "+u) {
			t.Fatalf("%+v, want REFUSED hold on %s with 0 pushes", r, u)
		}
		if got := f.remoteTip(); got != f.tip {
			t.Fatalf("remote moved to %s", got)
		}
		if n := f.events("INTENT") + f.events("LANDED"); n != 0 {
			t.Fatalf("INTENT+LANDED %d, want 0", n)
		}
	})
	t.Run("iii-lease-expires-new-publisher-completes-old-is-stale", func(t *testing.T) {
		t.Parallel()
		f := newFx(t)
		u, h := f.member()
		tr := f.gate("b28iii", f.plan("b28iii", u+"@"+h))
		var b publish.Result
		a := f.publisher(f.lease("pub-a"), func(pt publish.Point) error {
			if pt == publish.AfterIntent {
				b = f.land(f.publisher(f.takeLease("pub-b"), nil))
			}
			return nil
		})
		ra := f.land(a)
		if b.Outcome != publish.Landed {
			t.Fatalf("new publisher: %+v, want LANDED", b)
		}
		if ra.Outcome != publish.Fenced {
			t.Fatalf("resumed old publisher: %+v, want FENCED (STALE)", ra)
		}
		f.landedOnce("b28iii", tr, []string{u}, []string{h})
	})
	t.Run("iv-base-moved-by-hand-intent-dead-chain-replanned", func(t *testing.T) {
		t.Parallel()
		f := newFx(t)
		u, h := f.member()
		f.gate("b28iv", f.plan("b28iv", u+"@"+h))
		var moved string
		var b publish.Result
		a := f.publisher(f.lease("pub-a"), func(pt publish.Point) error {
			if pt == publish.AfterIntent {
				moved = f.handPush()
				b = f.land(f.publisher(f.takeLease("pub-b"), nil))
			}
			return nil
		})
		ra := f.land(a)
		if b.Outcome != publish.Dead {
			t.Fatalf("new publisher on a hand-moved base: %+v, want DEAD", b)
		}
		if ra.Outcome != publish.Fenced || ra.Pushes != 1 {
			t.Fatalf("old publisher: %+v, want its push refused and FENCED", ra)
		}
		if got := f.remoteTip(); got != moved {
			t.Fatalf("remote %s, want the hand push %s", got, moved)
		}
		if got := f.hget(land.PubBatchKey(f.repo, f.base, "b28iv"), "state"); got != "dead" {
			t.Fatalf("intent %q, want dead", got)
		}
		if st := f.hget(land.UnitKey(f.sprint, u), "state"); st != "landable" {
			t.Fatalf("unit %q, want landable (re-planned)", st)
		}
		if got := f.hget(land.TipKey(f.repo, f.base), "sha"); got != moved {
			t.Fatalf("tip record %s, want %s", got, moved)
		}
		if n := f.events("LANDED"); n != 0 {
			t.Fatalf("LANDED %d, want 0", n)
		}
	})
}

// fresh marks the inbound consumer caught up now (3.2): the fixture's git work
// takes longer than the freshness bound, so every pass is preceded by it.
func (f *fx) fresh() {
	f.t.Helper()
	if err := f.rdb.HSet(f.ctx, "ev:github:consumer:land", "pending", "0", "at", strconv.FormatInt(time.Now().Unix(), 10)).Err(); err != nil {
		f.t.Fatalf("consumer: %v", err)
	}
}
