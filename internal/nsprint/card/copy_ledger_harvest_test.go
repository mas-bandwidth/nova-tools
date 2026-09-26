//go:build functional

package card_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card/harvestcopy"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws/wstest"
	"github.com/redis/go-redis/v9"
)

const harvestBaseSHA = "0123456789abcdef0123456789abcdef01234567"

// workCopyOnBench pushes one primary, deals a work copy of it to bench:b and
// opens the bench's copy session, so the copy is working under a token as
// nova-card copy finds it; it returns the client, the primary and the launch.
func workCopyOnBench(t *testing.T) (*redis.Client, string, card.CopyLaunch) {
	t.Helper()
	_, c := wstest.Start(t)
	ctx := context.Background()
	bench, _ := taskcard.ParseConsumer("bench:b")
	c.SAdd(ctx, "benches", "b")
	c.HSet(ctx, bench.DesiredKey(), "slots", "1")
	if err := taskcard.Enroll(ctx, c, bench, true); err != nil {
		t.Fatal(err)
	}
	if _, err := taskcard.Push(ctx, c, taskcard.PushRequest{ID: "quack-1", Where: "waiting", Stream: "swarm: cards", Kind: "build",
		Title: "copy model: push and open the PR at the boundary", Repo: "nova-tools", Origin: "issue:nova-tools#4227",
		By: "rowan", Fields: []string{"base", "dev", "base_sha", harvestBaseSHA, "paths", "internal/nsprint/card/copy_ledger.go",
			"done_when", "a work copy reaches ok with a PR open"}}); err != nil {
		t.Fatal(err)
	}
	if d, err := taskcard.Deal(ctx, c, taskcard.DealRequest{To: bench, N: 1, By: "rowan"}); err != nil || len(d) != 1 {
		t.Fatalf("deal %v %v", d, err)
	}
	var launched []card.CopyLaunch
	s, err := card.OpenCopySession(ctx, c, "b", "bench:b", func(l card.CopyLaunch) error {
		launched = append(launched, l)
		return nil
	})
	if err != nil || len(launched) != 1 {
		t.Fatalf("session %+v %v", s, err)
	}
	led := &card.CopyLedger{Client: c, Copy: launched[0].Copy, Bench: "b", Token: launched[0].Token}
	if code, err := led.Launched(ctx, "", "", time.Minute); err != nil || code != 0 {
		t.Fatalf("launched code=%d %v", code, err)
	}
	return c, "quack-1", launched[0]
}

// doneEnd is a DONE wrapper end with a commit in a checkout under
// t.TempDir() and a RESULT.md beside it.
func doneEnd(t *testing.T, sha string) card.WrapperEnd {
	t.Helper()
	job := t.TempDir()
	if err := os.MkdirAll(filepath.Join(job, "out", "repo"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(job, "out", "RESULT.md"), []byte("RESULT: quack-1.c1 sha=01234567\nDONE\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return card.WrapperEnd{Outcome: "DONE", Reason: "done", Exit: 0, PushedSHA: sha, Commit: "COMMITTED",
		ResultsDir: filepath.Join(job, "out"), RepoDir: filepath.Join(job, "out", "repo")}
}

// TestCopyEndHarvestsAndEndsOkWithThePR is #4227's DONE-WHEN on the ledger:
// a work copy's DONE with a commit calls Harvest with the commit, the copy's
// branch, the primary's repo, BASE, title, stream, origin and DONE-WHEN and
// the push credential; then the PR record is at the head, the copy is ok
// with the PR and the head, and the primary is in review with them.
func TestCopyEndHarvestsAndEndsOkWithThePR(t *testing.T) {
	t.Parallel()
	c, primary, l := workCopyOnBench(t)
	ctx := context.Background()
	sha := strings.Repeat("ab", 20)
	var got harvestcopy.Request
	calls := 0
	led := &card.CopyLedger{Client: c, Copy: l.Copy, Bench: "b", Token: l.Token, PushToken: "ghp-bench",
		Now: func() time.Time { return time.UnixMilli(1700000000000) },
		Harvest: func(_ context.Context, r harvestcopy.Request) (harvestcopy.Result, error) {
			calls++
			got = r
			return harvestcopy.Result{Repo: "mas-bandwidth/nova-tools", Branch: r.Branch, Head: r.SHA, PR: 4321,
				URL: "http://127.0.0.1/pr/4321", Push: "pushed", Open: "opened",
				Title: "the opened title", Body: "STREAM: swarm: cards\nDONE-WHEN: a work copy reaches ok with a PR open\n"}, nil
		}}
	end := doneEnd(t, sha)
	if code, err := led.End(ctx, end); err != nil || code != 0 {
		t.Fatalf("end code=%d %v", code, err)
	}
	branch := card.WrapperBranch(card.CopySprint, card.CopyCardLabel(l.Copy), 1)
	want := harvestcopy.Request{RepoDir: end.RepoDir, SHA: sha, Branch: branch, Repo: "nova-tools", Base: "dev",
		Title: "copy model: push and open the PR at the boundary", Stream: "swarm: cards", Origin: "issue:nova-tools#4227",
		DoneWhen: "a work copy reaches ok with a PR open", Token: "ghp-bench", Redis: c}
	// #4371: every production client names its store; the harvest counts its
	// REST calls in the Redis the copy end runs on.
	if calls != 1 || got != want {
		t.Fatalf("harvest called %d times with\n%+v\nwant\n%+v", calls, got, want)
	}
	rec := c.HGetAll(ctx, taskcard.Key(l.Copy)).Val()
	if rec["where"] != "ok" || rec["pr"] != "4321" || rec["head"] != sha || rec["commit"] != sha || rec["branch"] != branch ||
		rec["line1"] != "RESULT: quack-1.c1 sha=01234567" || rec["line2"] != "DONE" {
		t.Fatalf("copy record %v", rec)
	}
	if strings.Contains(fmt.Sprint(rec), "ghp-bench") {
		t.Fatalf("the push credential reached the copy record: %v", rec)
	}
	p := c.HGetAll(ctx, taskcard.Key(primary)).Val()
	if p["where"] != "review" || p["pr"] != "4321" || p["head"] != sha || p["copy"] != "" {
		t.Fatalf("primary %v, want review with the PR and the head", p)
	}
	pr := c.HGetAll(ctx, "pr:nova-tools:4321").Val()
	if pr["head"] != sha || pr["base"] != "dev" || pr["base_sha"] != harvestBaseSHA || pr["stream"] != "swarm: cards" ||
		pr["branch"] != branch || pr["state"] != "open" || pr["ci"] != "pending" || pr["repo"] != "mas-bandwidth/nova-tools" ||
		pr["n"] != "4321" || pr["task"] != primary || pr["created_at"] != "1700000000000" ||
		pr["pr_title"] != "the opened title" || pr["pr_body"] != "STREAM: swarm: cards\nDONE-WHEN: a work copy reaches ok with a PR open\n" {
		t.Fatalf("pr record %v (#4335: pr_title and pr_body are what the PR was opened with)", pr)
	}
	// a second end of the ended copy writes nothing and harvests nothing
	if code, err := led.End(ctx, end); err != nil || code != 0 || calls != 1 {
		t.Fatalf("second end code=%d %v calls=%d", code, err, calls)
	}
}

// TestCopyEndWithoutAPushTokenFailsNoToken: no GH_PUSH_TOKEN in the
// wrapper's environment is the typed fail no-token, Harvest is never
// called, and the commit and the branch stay on the record for the review.
func TestCopyEndWithoutAPushTokenFailsNoToken(t *testing.T) {
	t.Parallel()
	c, primary, l := workCopyOnBench(t)
	ctx := context.Background()
	sha := strings.Repeat("cd", 20)
	led := &card.CopyLedger{Client: c, Copy: l.Copy, Bench: "b", Token: l.Token,
		Harvest: func(context.Context, harvestcopy.Request) (harvestcopy.Result, error) {
			t.Error("harvest called without a token")
			return harvestcopy.Result{}, nil
		}}
	if code, err := led.End(ctx, doneEnd(t, sha)); err != nil || code != 0 {
		t.Fatalf("end code=%d %v", code, err)
	}
	rec := c.HGetAll(ctx, taskcard.Key(l.Copy)).Val()
	if rec["where"] != "fail" || !strings.HasPrefix(rec["why"], "no-token: "+harvestcopy.TokenEnv+" is empty") ||
		rec["commit"] != sha || rec["branch"] != card.WrapperBranch(card.CopySprint, card.CopyCardLabel(l.Copy), 1) || rec["pr"] != "" {
		t.Fatalf("copy record %v", rec)
	}
	if p := c.HGetAll(ctx, taskcard.Key(primary)).Val(); p["where"] != "review" || p["pr"] != "" {
		t.Fatalf("primary %v, want review (a fail) with no PR", p)
	}
}

// TestCopyEndHarvestRefusalsAreTypedFails: a moved branch and a push git
// refused are push-refused, a refused PR is pr-refused, each with the
// harvest's text as the why and the commit kept on the record.
func TestCopyEndHarvestRefusalsAreTypedFails(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, reason string
		err          error
	}{
		{"moved", "push-refused", fmt.Errorf("%w: %w: nova/copies/x at 1111, not 2222", harvestcopy.ErrPushRefused, harvestcopy.ErrBranchMoved)},
		{"pr", "pr-refused", fmt.Errorf("%w: POST /repos/x/pulls: HTTP 403: forbidden", harvestcopy.ErrPRRefused)},
		{"push", "push-refused", errors.New("git push: exit status 128: could not read from remote")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c, _, l := workCopyOnBench(t)
			ctx := context.Background()
			sha := strings.Repeat("ef", 20)
			led := &card.CopyLedger{Client: c, Copy: l.Copy, Bench: "b", Token: l.Token, PushToken: "ghp-bench",
				Harvest: func(context.Context, harvestcopy.Request) (harvestcopy.Result, error) {
					return harvestcopy.Result{}, tc.err
				}}
			if code, err := led.End(ctx, doneEnd(t, sha)); err != nil || code != 0 {
				t.Fatalf("end code=%d %v", code, err)
			}
			rec := c.HGetAll(ctx, taskcard.Key(l.Copy)).Val()
			if rec["where"] != "fail" || rec["why"] != tc.reason+": "+tc.err.Error() || rec["commit"] != sha || rec["pr"] != "" {
				t.Fatalf("copy record %v, want fail %s", rec, tc.reason)
			}
		})
	}
}

// TestCopyCrashEndCarriesTheHarnessExit is #4234's evidence rule: a copy
// whose harness ended non-zero is fail with the exit in its why (the bare
// "crash" the darwin copies recorded said nothing the wrapper line did not),
// a refusal keeps its line, and a harness that never ran stays "crash: <why>".
func TestCopyCrashEndCarriesTheHarnessExit(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		end  card.WrapperEnd
		why  string
	}{
		{"exit 2", card.WrapperEnd{Outcome: "FAILED", Reason: "crash", Exit: 2, Commit: card.NoCommit}, "crash: harness exit 2"},
		{"refused", card.WrapperEnd{Outcome: "FAILED", Reason: "refused", Exit: 2, Commit: card.NoCommit,
			Why: "REFUSED no payload_sha on s:copies:card:quack-001-c1 for copies/quack-001-c1/1"},
			"refused: REFUSED no payload_sha on s:copies:card:quack-001-c1 for copies/quack-001-c1/1"},
		{"never ran", card.WrapperEnd{Outcome: "FAILED", Reason: "crash", Exit: -1, Commit: card.NoCommit, Why: "harness start: no such file"},
			"crash: harness start: no such file"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c, _, l := workCopyOnBench(t)
			ctx := context.Background()
			led := &card.CopyLedger{Client: c, Copy: l.Copy, Bench: "b", Token: l.Token}
			if code, err := led.End(ctx, tc.end); err != nil || code != 0 {
				t.Fatalf("end code=%d %v", code, err)
			}
			rec := c.HGetAll(ctx, taskcard.Key(l.Copy)).Val()
			if rec["where"] != "fail" || rec["why"] != tc.why {
				t.Fatalf("copy record where=%q why=%q, want fail %q", rec["where"], rec["why"], tc.why)
			}
		})
	}
}
