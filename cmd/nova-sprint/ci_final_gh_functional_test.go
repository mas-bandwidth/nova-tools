//go:build functional

package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/webhook"
	"github.com/redis/go-redis/v9"
)

// TestFriendDoneReadPassesOnTheGitHubLegOrThePRRecord (ci-final-gh@dev):
// the whole door through the CLI. A friend reader's `friend done --score
// 10/10` on a read copy of a PR in a repo whose CI is GitHub Actions only
// (no request record of our own ever exists) is refused CIPENDING until
// the head's CI is OK, and CI is one fold over every source that speaks:
// the GitHub leg ci:<repo>:<head>:gh as `ci github --from-runner` writes it
// (webhook.Write), the PR record's ci word at its ci_sha, our own request
// record and the ci cards' verdicts. FAIL anywhere wins, pending anywhere
// stays pending, and the copy ENDS to merging only on OK.
func TestFriendDoneReadPassesOnTheGitHubLegOrThePRRecord(t *testing.T) {
	t.Parallel()

	addr := testutil.Start(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = c.Close() })
	ctx := context.Background()
	if err := fn.Load(ctx, c); err != nil {
		t.Fatal(err)
	}
	me := os.Getenv(seatEnv)
	if me == "" {
		me = "rowan"
	}
	const repo, bare = "mas-bandwidth/quack", "quack"
	head := func(i int) string { return fmt.Sprintf("%040x", 0xc0000+i) }
	author, _ := taskcard.ParseConsumer("bench:b")
	reader, _ := taskcard.ParseConsumer("friend:" + me)
	c.SAdd(ctx, "benches", "b")
	c.SAdd(ctx, "friends", me)
	c.HSet(ctx, "friend:"+me+":roles", "roles", "reader")
	for _, x := range []taskcard.Consumer{author, reader} {
		c.HSet(ctx, x.DesiredKey(), "slots", "12")
		if err := taskcard.Enroll(ctx, c, x, true); err != nil {
			t.Fatal(err)
		}
		c.HSet(ctx, x.BeatKey(), "at", strconv.FormatInt(time.Now().UnixMilli(), 10))
	}
	const n = 9
	for i := 0; i < n; i++ {
		if _, err := taskcard.Push(ctx, c, taskcard.PushRequest{ID: fmt.Sprintf("q%02d", i), Where: "waiting", Stream: "swarm: cards",
			Sprint: "s", Kind: "build", Ref: fmt.Sprintf("quack#%d", 100+i), Origin: fmt.Sprintf("issue:quack#%d", 100+i),
			Title: "quack", Repo: repo, By: "rowan",
			Fields: []string{"base", "main", "base_sha", strings.Repeat("0", 40), "paths", "x.go", "done_when", "go test ./x passes"}}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := taskcard.Deal(ctx, c, taskcard.DealRequest{To: author, N: n, By: "rowan"}); err != nil {
		t.Fatal(err)
	}
	w, err := taskcard.Work(ctx, c, author, "rowan", 0, true)
	if err != nil || len(w.IDs) != n {
		t.Fatalf("work %+v %v", w, err)
	}
	prKey := func(i int) string { return "pr:" + bare + ":" + strconv.Itoa(100+i) }
	reads := make([]string, n)
	for i := 0; i < n; i++ {
		c.HSet(ctx, prKey(i), "repo", bare, "n", 100+i, "head", head(i), "base", "main", "state", "open", "ci", "pending", "ci_sha", "")
		e, err := taskcard.End(ctx, c, taskcard.EndRequest{IDs: []string{w.IDs[i]}, OK: true, Repo: repo, PR: strconv.Itoa(100 + i),
			Head: head(i), By: "rowan"})
		if err != nil || len(e) != 1 || e[0].To != "review" || e[0].Next == "" {
			t.Fatalf("end ok %d: %v %v", i, e, err)
		}
		reads[i] = e[0].Next
	}
	if _, err := taskcard.Work(ctx, c, reader, "rowan", 0, true); err != nil {
		t.Fatal(err)
	}
	done := func(i int) (int, string) {
		var out, errOut bytes.Buffer
		code := runFriend(ctx, []string{"done", "--redis", addr, "--as", "friend:" + me, "--id", reads[i], "--score", "10/10"}, &out, &errOut)
		return code, out.String() + errOut.String()
	}
	ends := func(when string, i int) {
		t.Helper()
		code, out := done(i)
		if code != 0 || !strings.Contains(out, "ENDED "+reads[i]+" primary=q"+fmt.Sprintf("%02d", i)+" from=review to=merging") ||
			!strings.Contains(out, "FRIEND DONE as=friend:"+me+" n=1 ms=") {
			t.Fatalf("%s: friend done --score 10/10 = %d %q, want ENDED to merging", when, code, out)
		}
	}
	refused := func(when string, i int, why string) {
		t.Helper()
		code, out := done(i)
		if code != 1 || !strings.Contains(out, "FRIEND DONE REFUSED id="+reads[i]+" why=\""+why) {
			t.Fatalf("%s: friend done --score 10/10 = %d %q, want a %s refusal", when, code, out, why)
		}
		if got := c.HGet(ctx, "task:q"+fmt.Sprintf("%02d", i), "where").Val(); got != "review" {
			t.Fatalf("%s: primary is %s after a refused read, want review", when, got)
		}
	}
	runner := func(i int, conclusion string) {
		t.Helper()
		if _, err := webhook.Write(ctx, c, webhook.Receipt{Repo: repo, SHA: head(i), RunID: strconv.Itoa(500 + i), Event: "pull_request",
			HeadBranch: "fix", BaseBranch: "main", PR: strconv.Itoa(100 + i), Workflow: "ci", Conclusion: conclusion,
			At: "2026-09-27T00:00:00Z"}); err != nil {
			t.Fatal(err)
		}
	}
	pending := "CIPENDING " + head(0)[:12]

	// (a) no source at the head: CIPENDING with the PR record at its
	// creation default (ci pending, no ci_sha); the runner receipt green
	// makes the leg the one OK, and the read ENDS
	refused("nothing at the head", 0, pending)
	runner(0, "success")
	// the receipt also folds the PR record's ci; put that back to its
	// creation default so the leg is the one source that speaks
	c.HSet(ctx, prKey(0), "ci", "pending", "ci_sha", "")
	ends("gh green from the runner receipt", 0)

	// (b) no leg at all (no receiver, no runner token): the PR record's ci
	// green at this head is the one OK, and the read ENDS
	c.HSet(ctx, prKey(1), "ci", "green", "ci_sha", head(1))
	ends("pr record green, no leg", 1)

	// (c) the PR record's green at another ci_sha says nothing
	c.HSet(ctx, prKey(2), "ci", "green", "ci_sha", head(12))
	refused("pr record green at another head", 2, "CIPENDING "+head(2)[:12])

	// (d) the runner receipt red is CIRED naming the run, over the PR
	// record's green at the head
	c.HSet(ctx, prKey(3), "ci", "green", "ci_sha", head(3))
	runner(3, "failure")
	refused("gh red over pr record green", 3, "CIRED "+head(3)[:12]+" CI is FAIL (github red wf:ci)")

	// (e) our own CI finished OK + gh red: FAIL wins; own unfinished + gh
	// green: pending
	c.HSet(ctx, "ci:"+bare+":"+head(4), "ci", "green", "final", "OK")
	runner(4, "failure")
	refused("own OK + gh red", 4, "CIRED "+head(4)[:12]+" CI is FAIL (github red wf:ci)")
	c.HSet(ctx, "ci:"+repo+":"+head(5), "ci", "pending", "final", "")
	runner(5, "success")
	refused("own unfinished + gh green", 5, "CIPENDING "+head(5)[:12])

	// (f) ci cards: OK + FAIL is FAIL over gh green; OK + no verdict is
	// pending; own OK with no leg is OK
	c.SAdd(ctx, "ci:"+repo+":"+head(6)+":gids", "g1", "g2")
	c.HSet(ctx, "ci:"+repo+":"+head(6)+":g1", "verdict", "OK")
	c.HSet(ctx, "ci:"+repo+":"+head(6)+":g2", "verdict", "FAIL")
	runner(6, "success")
	refused("ci cards OK + FAIL over gh green", 6, "CIRED "+head(6)[:12]+" CI is FAIL (ci card verdict FAIL)")
	c.SAdd(ctx, "ci:"+bare+":"+head(7)+":gids", "g1", "g2")
	c.HSet(ctx, "ci:"+bare+":"+head(7)+":g1", "verdict", "OK")
	c.HSet(ctx, "ci:"+bare+":"+head(7)+":g2", "verdict", "")
	refused("ci cards OK + pending", 7, "CIPENDING "+head(7)[:12])
	c.HSet(ctx, "ci:"+repo+":"+head(8), "ci", "green", "final", "OK")
	ends("own OK, no leg", 8)
}
