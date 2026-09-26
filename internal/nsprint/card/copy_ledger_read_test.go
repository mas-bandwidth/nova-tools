package card_test

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card/harvestcopy"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws/wstest"
	"github.com/redis/go-redis/v9"
)

const (
	readHead   = "cdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcd"
	readBranch = "nova/copies/quack-1-c1-a1"
)

// readCopyOnBench pushes one primary, has its author (bench:a) end a work
// copy ok with PR nova-tools#3950 at readHead on readBranch (the move into
// review), which cuts one read copy onto the live reader bench:b (one
// slot), and opens b's copy session so the read copy is working under a
// token as nova-card copy finds it. It returns the client, the primary and
// the launch.
func readCopyOnBench(t *testing.T) (*redis.Client, string, card.CopyLaunch) {
	t.Helper()
	_, c := wstest.Start(t)
	ctx := context.Background()
	at := strconv.FormatInt(time.Now().UnixMilli(), 10)
	author, _ := taskcard.ParseConsumer("bench:a")
	reader, _ := taskcard.ParseConsumer("bench:b")
	for _, k := range []taskcard.Consumer{author, reader} {
		c.SAdd(ctx, "benches", k.Name)
		c.HSet(ctx, k.DesiredKey(), "slots", "1")
		c.HSet(ctx, k.BeatKey(), "at", at)
		if err := taskcard.Enroll(ctx, c, k, true); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := taskcard.Push(ctx, c, taskcard.PushRequest{ID: "quack-1", Where: "waiting", Stream: "swarm: cards", Kind: "build",
		Title: "read copies: the wrapper ends the copy from the SCORE line", Repo: "nova-tools", Origin: "issue:nova-tools#4270",
		By: "rowan", Fields: []string{"base", "dev", "base_sha", harvestBaseSHA, "paths", "internal/nsprint/card/copy_ledger.go",
			"done_when", "a read copy ends ok with a SCORE line and no nova-sprint call by the model"}}); err != nil {
		t.Fatal(err)
	}
	d, err := taskcard.Deal(ctx, c, taskcard.DealRequest{To: author, N: 1, By: "rowan"})
	if err != nil || len(d) != 1 {
		t.Fatalf("deal %v %v", d, err)
	}
	if _, err := taskcard.Work(ctx, c, author, "a", 0, false, d[0].Copy); err != nil {
		t.Fatal(err)
	}
	c.HSet(ctx, "pr:nova-tools:3950", "repo", "nova-tools", "n", "3950", "head", readHead, "base", "dev", "state", "open",
		"branch", readBranch)
	e, err := taskcard.End(ctx, c, taskcard.EndRequest{IDs: []string{d[0].Copy}, OK: true, Repo: "nova-tools", PR: "3950",
		Head: readHead, By: "a", Fields: []string{"branch", readBranch}})
	if err != nil || len(e) != 1 || e[0].To != "review" || strings.Contains(e[0].Next, ",") || e[0].Next == "" {
		t.Fatalf("the move into review %v %v, want one read copy cut on bench:b", e, err)
	}
	var launched []card.CopyLaunch
	s, err := card.OpenCopySession(ctx, c, "b", "bench:b", func(l card.CopyLaunch) error {
		launched = append(launched, l)
		return nil
	})
	if err != nil || len(launched) != 1 || launched[0].Copy != e[0].Next {
		t.Fatalf("session %+v %v launched %v", s, err, launched)
	}
	if r := c.HGetAll(ctx, taskcard.Key(launched[0].Copy)).Val(); r["leg"] != "read" || r["head"] != readHead || r["branch"] != readBranch {
		t.Fatalf("read copy %v, want leg read at the head carrying the PR's branch", r)
	}
	return c, "quack-1", launched[0]
}

// readEnd is a DONE wrapper end of a read copy whose RESULT.md line 2 is
// line2 (no RESULT.md when line2 is "-"); a read commits nothing.
func readEnd(t *testing.T, line2 string) card.WrapperEnd {
	t.Helper()
	out := filepath.Join(t.TempDir(), "out")
	if err := os.MkdirAll(filepath.Join(out, "repo"), 0o755); err != nil {
		t.Fatal(err)
	}
	if line2 != "-" {
		if err := os.WriteFile(filepath.Join(out, "RESULT.md"), []byte("RESULT: quack-1.c2 sha=cdcdcdcd\n"+line2+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return card.WrapperEnd{Outcome: "DONE", Reason: "done", PushedSHA: card.NoCommit, Commit: "NO-COMMIT", ResultsDir: out,
		RepoDir: filepath.Join(out, "repo")}
}

func ledgerFor(c *redis.Client, l card.CopyLaunch) *card.CopyLedger {
	return &card.CopyLedger{Client: c, Copy: l.Copy, Bench: "b", Token: l.Token, PushToken: "ghp-bench",
		Harvest: func(context.Context, harvestcopy.Request) (harvestcopy.Result, error) {
			panic("a read copy harvests nothing")
		}}
}

// TestReadCopyEndsOkFromTheScoreLine is #4270's DONE-WHEN on the ledger: a
// read copy's DONE with a SCORE line in RESULT.md ends ok with the score,
// the gates and the finding, the SCORE line is on pr:<name>:<n> at the
// head, and the 8+ moves the primary review -> merging; the model ran no
// nova-sprint and nothing was harvested.
func TestReadCopyEndsOkFromTheScoreLine(t *testing.T) {
	t.Parallel()
	c, primary, l := readCopyOnBench(t)
	ctx := context.Background()
	c.HSet(ctx, "ci:nova-tools:"+readHead, "final", "OK", "ci", "green")
	led := ledgerFor(c, l)
	if code, err := led.End(ctx, readEnd(t, "SCORE 9/10 gates=ci:green,base:ok,scope:ok finding=one comment could name the issue")); err != nil || code != 0 {
		t.Fatalf("end code=%d %v", code, err)
	}
	rec := c.HGetAll(ctx, taskcard.Key(l.Copy)).Val()
	if rec["where"] != "ok" || rec["score"] != "9" || rec["gates"] != "ci:green,base:ok,scope:ok" ||
		rec["finding"] != "one comment could name the issue" || rec["line2"] != "SCORE 9/10 gates=ci:green,base:ok,scope:ok finding=one comment could name the issue" ||
		rec["commit"] != "" {
		t.Fatalf("copy record %v", rec)
	}
	if p := c.HGetAll(ctx, taskcard.Key(primary)).Val(); p["where"] != "merging" || p["score"] != "9" || p["reads"] != "" {
		t.Fatalf("primary %v, want merging on the 9", p)
	}
	reads := c.HGet(ctx, "pr:nova-tools:3950", "reads").Val()
	if !strings.Contains(reads, "SCORE who=b head="+readHead+" score=9/10 gates=ci:green,base:ok,scope:ok: one comment could name the issue") {
		t.Fatalf("pr record reads %q, want the SCORE line at the head", reads)
	}
	// the same end again writes nothing
	if code, err := led.End(ctx, readEnd(t, "SCORE 9/10")); err != nil || code != 0 {
		t.Fatalf("second end code=%d %v", code, err)
	}
}

// TestReadCopyWithoutAScoreLineFailsTyped: no RESULT.md, a line 2 that is
// not a SCORE line, and a malformed SCORE line each end the copy fail with
// the typed reason no-score and the text as the why; `ABSTAIN <why>` ends
// it fail with the reason abstain. The primary goes to review for a
// verdict as any read fail does.
func TestReadCopyWithoutAScoreLineFailsTyped(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, line2, why string }{
		{"no RESULT.md", "-", "no-score: RESULT.md has no line 2"},
		{"DONE", "DONE", "no-score: line 2 is not a SCORE line: DONE"},
		{"score out of range", "SCORE 11/10 gates=ci:green,base:ok,scope:ok", "no-score: line 2 is not a SCORE line: SCORE 11/10 gates=ci:green,base:ok,scope:ok"},
		{"gates of another shape", "SCORE 9/10 gates=ci:ok finding=x", "no-score: line 2 is not a SCORE line: SCORE 9/10 gates=ci:ok finding=x"},
		{"abstain", "ABSTAIN the PR could not be fetched", "abstain: the PR could not be fetched"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c, primary, l := readCopyOnBench(t)
			ctx := context.Background()
			end := readEnd(t, tc.line2)
			if tc.name == "abstain" {
				// what ModelEnd makes of an ABSTAIN line 2
				end.Outcome, end.Reason, end.Why = "ABSTAIN", "other", tc.line2
			}
			if code, err := ledgerFor(c, l).End(ctx, end); err != nil || code != 0 {
				t.Fatalf("end code=%d %v", code, err)
			}
			rec := c.HGetAll(ctx, taskcard.Key(l.Copy)).Val()
			if rec["where"] != "fail" || rec["why"] != tc.why || rec["score"] != "" {
				t.Fatalf("copy record %v, want fail %q", rec, tc.why)
			}
			if p := c.HGetAll(ctx, taskcard.Key(primary)).Val(); p["where"] != "review" || p["review_at"] == "" {
				t.Fatalf("primary %v, want review with a verdict pending", p)
			}
		})
	}
}

// TestReadCopyScoreWaitsForCI: a passing score at a head with no CI verdict
// yet (CIPENDING) gives the copy back (the primary stays in review, its
// reads empty so the deal pass cuts a fresh read when CI is in); at a head
// CI called red (CIRED) the read ends under 8 with the CI failure as its
// finding, as the refusal says, and the primary stays in review with a fix
// copy cut.
func TestReadCopyScoreWaitsForCI(t *testing.T) {
	t.Parallel()
	t.Run("pending", func(t *testing.T) {
		t.Parallel()
		c, primary, l := readCopyOnBench(t)
		ctx := context.Background()
		if code, err := ledgerFor(c, l).End(ctx, readEnd(t, "SCORE 9/10 gates=ci:green,base:ok,scope:ok finding=none")); err != nil || code != 0 {
			t.Fatalf("end code=%d %v", code, err)
		}
		rec := c.HGetAll(ctx, taskcard.Key(l.Copy)).Val()
		if rec["where"] != "fail" || !strings.HasPrefix(rec["why"], "cancel: read 9/10 held: CIPENDING") {
			t.Fatalf("copy record %v, want given back on CIPENDING", rec)
		}
		if p := c.HGetAll(ctx, taskcard.Key(primary)).Val(); p["where"] != "review" || p["review_at"] != "" || p["reads"] != "" {
			t.Fatalf("primary %v, want review, no verdict pending, no live read", p)
		}
	})
	t.Run("red", func(t *testing.T) {
		t.Parallel()
		c, primary, l := readCopyOnBench(t)
		ctx := context.Background()
		c.HSet(ctx, "ci:nova-tools:"+readHead, "final", "FAIL", "ci", "red", "why", "--- FAIL: TestX")
		if code, err := ledgerFor(c, l).End(ctx, readEnd(t, "SCORE 9/10 gates=ci:red,base:ok,scope:ok finding=looks right")); err != nil || code != 0 {
			t.Fatalf("end code=%d %v", code, err)
		}
		rec := c.HGetAll(ctx, taskcard.Key(l.Copy)).Val()
		if rec["where"] != "ok" || rec["score"] != "7" ||
			rec["finding"] != "CI red at cdcdcdcdcdcd: CIRED cdcdcdcdcdcd CI is FAIL (--- FAIL: TestX); read 9/10: looks right" {
			t.Fatalf("copy record %v, want ok at 7 with the CI failure as the finding", rec)
		}
		p := c.HGetAll(ctx, taskcard.Key(primary)).Val()
		if p["where"] != "review" || p["copy"] == "" || c.HGet(ctx, taskcard.Key(p["copy"]), "leg").Val() != "fix" {
			t.Fatalf("primary %v, want review with a fix copy", p)
		}
	})
}

// TestFixCopyEndPushesOntoThePRBranch (#4270, the fix leg): a read under 8
// cuts a fix copy on the swarm; its DONE with a commit on top of the PR's
// head hands Harvest the commit, the PR's own branch and the head it built
// on (Onto), then the PR record moves to the new head, the copy ends ok
// with the PR and the new head, and the primary stays in review at that
// head with fresh reads cut. A fix copy with nothing committed fails "done
// without a commit".
func TestFixCopyEndPushesOntoThePRBranch(t *testing.T) {
	t.Parallel()
	c, primary, l := readCopyOnBench(t)
	ctx := context.Background()
	c.HSet(ctx, "ci:nova-tools:"+readHead, "final", "OK", "ci", "green")
	if code, err := ledgerFor(c, l).End(ctx, readEnd(t, "SCORE 6/10 gates=ci:green,base:ok,scope:ok finding=the test does not fail without the fix")); err != nil || code != 0 {
		t.Fatalf("read end code=%d %v", code, err)
	}
	p := c.HGetAll(ctx, taskcard.Key(primary)).Val()
	fixCopy := p["copy"]
	if p["where"] != "review" || fixCopy == "" {
		t.Fatalf("primary %v, want review with a fix copy", p)
	}
	fr := c.HGetAll(ctx, taskcard.Key(fixCopy)).Val()
	if fr["leg"] != "fix" || fr["consumer"] != "bench:a" || fr["branch"] != readBranch || fr["head"] != readHead {
		t.Fatalf("fix copy %v, want on the author's bench carrying the PR's branch and head", fr)
	}
	body, err := card.RenderCopy(card.CopyCardFrom(fixCopy, fr))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"\nKIND: fix\n", "\nbase-sha: " + readHead + "\n", "\nBRANCH: " + readBranch + "\n",
		"repo/ is checked out at the PR's head " + readHead + ", which is " + readBranch, "the wrapper pushes your commit to " + readBranch,
		"the read found: SCORE who=b head=" + readHead + " score=6/10 gates=ci:green,base:ok,scope:ok: the test does not fail without the fix"} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("fix card lacks %q:\n%s", want, body)
		}
	}
	if strings.Contains(string(body), "card end") {
		t.Fatalf("fix card tells the model to run card end:\n%s", body)
	}
	var launched []card.CopyLaunch
	if s, err := card.OpenCopySession(ctx, c, "a", "bench:a", func(l card.CopyLaunch) error {
		launched = append(launched, l)
		return nil
	}); err != nil || len(launched) != 1 || launched[0].Copy != fixCopy {
		t.Fatalf("session %+v %v launched %v", s, err, launched)
	}
	sha := strings.Repeat("ef", 20)
	var got harvestcopy.Request
	led := &card.CopyLedger{Client: c, Copy: fixCopy, Bench: "a", Token: launched[0].Token, PushToken: "ghp-bench",
		Now: func() time.Time { return time.UnixMilli(1700000000000) },
		Harvest: func(_ context.Context, r harvestcopy.Request) (harvestcopy.Result, error) {
			got = r
			return harvestcopy.Result{Repo: "mas-bandwidth/nova-tools", Branch: r.Branch, Head: r.SHA, PR: 3950,
				URL: "http://127.0.0.1/pr/3950", Push: "pushed", Open: "already"}, nil
		}}
	end := doneEnd(t, sha)
	if code, err := led.End(ctx, end); err != nil || code != 0 {
		t.Fatalf("fix end code=%d %v", code, err)
	}
	want := harvestcopy.Request{RepoDir: end.RepoDir, SHA: sha, Branch: readBranch, Onto: readHead, Repo: "nova-tools", Base: "dev",
		Title: "read copies: the wrapper ends the copy from the SCORE line", Stream: "swarm: cards", Origin: "issue:nova-tools#4270",
		DoneWhen: "a read copy ends ok with a SCORE line and no nova-sprint call by the model", Token: "ghp-bench"}
	if got != want {
		t.Fatalf("harvest called with\n%+v\nwant\n%+v", got, want)
	}
	rec := c.HGetAll(ctx, taskcard.Key(fixCopy)).Val()
	if rec["where"] != "ok" || rec["pr"] != "3950" || rec["head"] != sha || rec["commit"] != sha || rec["branch"] != readBranch {
		t.Fatalf("fix copy record %v", rec)
	}
	pr := c.HGetAll(ctx, "pr:nova-tools:3950").Val()
	if pr["head"] != sha || pr["branch"] != readBranch || pr["ci"] != "pending" {
		t.Fatalf("pr record %v, want at the new head, CI pending", pr)
	}
	p = c.HGetAll(ctx, taskcard.Key(primary)).Val()
	if p["where"] != "review" || p["head"] != sha || p["copy"] != "" || p["reads"] == "" {
		t.Fatalf("primary %v, want review at the new head with fresh reads", p)
	}
	if r := c.HGetAll(ctx, taskcard.Key(strings.Fields(p["reads"])[0])).Val(); r["head"] != sha || r["consumer"] != "bench:b" {
		t.Fatalf("fresh read %v, want at the new head on bench:b", r)
	}
}

// TestFixCopyWithoutACommitFails: a fix copy's DONE with nothing committed
// harvests nothing and fails "done without a commit".
func TestFixCopyWithoutACommitFails(t *testing.T) {
	t.Parallel()
	c, primary, l := readCopyOnBench(t)
	ctx := context.Background()
	c.HSet(ctx, "ci:nova-tools:"+readHead, "final", "OK", "ci", "green")
	if code, err := ledgerFor(c, l).End(ctx, readEnd(t, "SCORE 5/10 gates=ci:green,base:ok,scope:ok finding=wrong file")); err != nil || code != 0 {
		t.Fatalf("read end code=%d %v", code, err)
	}
	fixCopy := c.HGet(ctx, taskcard.Key(primary), "copy").Val()
	var launched []card.CopyLaunch
	if _, err := card.OpenCopySession(ctx, c, "a", "bench:a", func(l card.CopyLaunch) error {
		launched = append(launched, l)
		return nil
	}); err != nil || len(launched) != 1 {
		t.Fatalf("session %v %v", launched, err)
	}
	led := &card.CopyLedger{Client: c, Copy: fixCopy, Bench: "a", Token: launched[0].Token, PushToken: "ghp-bench",
		Harvest: func(context.Context, harvestcopy.Request) (harvestcopy.Result, error) {
			t.Error("harvest called with no commit")
			return harvestcopy.Result{}, nil
		}}
	end := doneEnd(t, card.NoCommit)
	end.Commit = "NO-COMMIT"
	if code, err := led.End(ctx, end); err != nil || code != 0 {
		t.Fatalf("end code=%d %v", code, err)
	}
	if rec := c.HGetAll(ctx, taskcard.Key(fixCopy)).Val(); rec["where"] != "fail" || rec["why"] != "done without a commit" {
		t.Fatalf("fix copy record %v", rec)
	}
}
