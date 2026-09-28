//go:build functional

package taskcard_test

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
	"github.com/redis/go-redis/v9"
)

// ghLeg writes one workflow_run event onto ci:<repo>:<sha>:gh through
// ns_ci_github, the leg's one writer (ci_github.lua): status completed with
// a conclusion, or in_progress for a pending fold.
func ghLeg(t *testing.T, c *redis.Client, key, status, conclusion string) {
	t.Helper()
	ctx := context.Background()
	v, err := c.FCall(ctx, "ns_ci_github", []string{key, "ev:github"}, "g", "0-1", "workflow_run", "ci", "1",
		status, conclusion, "2026-09-27T00:00:00Z", "").Result()
	if err != nil {
		t.Fatalf("ns_ci_github %s: %v", key, err)
	}
	if got := fmt.Sprint(v.([]any)[0]); got != "APPLIED" {
		t.Fatalf("ns_ci_github %s: %v", key, v)
	}
}

// ciCard records one ci card verdict at a head the way ci.lua keys it:
// ci:<repo>:<head>:gids names the gid, ci:<repo>:<head>:<gid> carries the
// verdict ("" for a card with no verdict yet).
func ciCard(c *redis.Client, repo, head, gid, verdict string) {
	ctx := context.Background()
	c.SAdd(ctx, "ci:"+repo+":"+head+":gids", gid)
	c.HSet(ctx, "ci:"+repo+":"+head+":"+gid, "gid", gid, "verdict", verdict)
}

// TestReadGateFoldsEveryCISource (ci-final-gh@dev): a read copy's passing
// score ends at a head whose CI is OK, where CI is one fold over every
// source that speaks at the head, FAIL anywhere winning, then pending
// anywhere, then OK: the store's own request record (final, or pending
// while it has none), the ci cards' verdicts, the GitHub leg
// ci:<repo>:<head>:gh (gh green|red|pending), each under the bare and the
// owner/name spelling, and the PR record's ci word at its ci_sha. A head no
// source names stays pending.
func TestReadGateFoldsEveryCISource(t *testing.T) {
	t.Parallel()

	c := start(t)
	ctx := context.Background()
	k, rd := mustConsumer(t, "bench:b"), mustConsumer(t, "friend:reader")
	c.SAdd(ctx, "benches", "b")
	c.SAdd(ctx, "friends", "reader")
	c.HSet(ctx, "friend:reader:roles", "roles", "reader")
	for _, x := range []taskcard.Consumer{k, rd} {
		c.HSet(ctx, x.DesiredKey(), "slots", "16")
		if err := taskcard.Enroll(ctx, c, x, true); err != nil {
			t.Fatal(err)
		}
		c.HSet(ctx, x.BeatKey(), "at", strconv.FormatInt(time.Now().UnixMilli(), 10))
	}
	c.HSet(ctx, "cfg:ci", "max_attempts", "1")
	const n = 12
	ids := pushPrimaries(t, c, n)
	if _, err := taskcard.Deal(ctx, c, taskcard.DealRequest{To: k, N: n, By: "rowan"}); err != nil {
		t.Fatal(err)
	}
	w, err := taskcard.Work(ctx, c, k, "rowan", 0, true)
	if err != nil || len(w.IDs) != n {
		t.Fatalf("work %+v %v", w, err)
	}
	// each build copy's ok with its PR (the owner/name spelling on the
	// primary, the PR record under the bare name) cuts one read copy for
	// the friend reader; no CI has run at any head
	reads := make([]string, n)
	for i := 0; i < n; i++ {
		recordPR(c, 7000+i, head(i))
		e, err := taskcard.End(ctx, c, taskcard.EndRequest{IDs: []string{w.IDs[i]}, OK: true, Repo: "mas-bandwidth/nova-tools",
			PR: fmt.Sprint(7000 + i), Head: head(i), By: "rowan"})
		if err != nil || len(e) != 1 || e[0].To != "review" || e[0].Next == "" {
			t.Fatalf("end ok %d: %v %v", i, e, err)
		}
		reads[i] = strings.Split(e[0].Next, ",")[0]
		if p := c.HGetAll(ctx, taskcard.Key(ids[i])).Val(); p["repo"] != "mas-bandwidth/nova-tools" || p["pr"] != fmt.Sprint(7000+i) {
			t.Fatalf("primary %d names %s#%s, want the owner/name spelling", i, p["repo"], p["pr"])
		}
	}
	if _, err := taskcard.Work(ctx, c, rd, "rowan", 0, true); err != nil {
		t.Fatal(err)
	}
	score := func(i int, head string) error {
		_, err := taskcard.End(ctx, c, taskcard.EndRequest{IDs: []string{reads[i]}, OK: true, Score: 9, Head: head, By: "rowan"})
		return err
	}
	passes := func(when string, i int) {
		t.Helper()
		if err := score(i, ""); err != nil {
			t.Fatalf("%s: a read at %s did not pass: %v", when, head(i)[:12], err)
		}
		if got := c.HGet(ctx, taskcard.Key(ids[i]), "where").Val(); got != "merging" {
			t.Fatalf("%s: primary is %s after a passing read, want merging", when, got)
		}
	}
	refuses := func(when string, i int, head, word, why string) {
		t.Helper()
		err := score(i, head)
		if !isRefusedWith(err, word) {
			t.Fatalf("%s: want a %s refusal, got %v", when, word, err)
		}
		if got, _ := taskcard.IsRefused(err); why != "" && !strings.Contains(got, why) {
			t.Fatalf("%s: refusal %q does not name %q", when, got, why)
		}
		if got := c.HGet(ctx, taskcard.Key(ids[i]), "where").Val(); got != "review" {
			t.Fatalf("%s: primary is %s after a refused read, want review", when, got)
		}
	}
	const bare, full = "nova-tools", "mas-bandwidth/nova-tools"
	gh := func(repo string, i int) string { return "ci:" + repo + ":" + head(i) + ":gh" }
	pending := "has no CI verdict yet"

	// own CI runs first for (1) and (6): the unfinished request (2) leaves
	// in ci:pool would be what a later claim takes
	ciVerdict(t, c, 7000, head(0), "0")
	ciVerdict(t, c, 7007, head(7), "0")

	// (1) same head, own final OK + gh red: FAIL wins over the own OK
	ghLeg(t, c, gh(bare, 0), "completed", "failure")
	refuses("own OK + gh red", 0, "", "CIRED", "github red wf:ci")

	// (2) own CI unfinished (a request record with no final) + gh green:
	// pending, not the gh OK
	if v, err := c.FCall(ctx, "ns_ci_request", nil, bare, head(1), "7001", "", "", "unit", "go test ./...").Result(); err != nil {
		t.Fatalf("ci request %v %v", v, err)
	}
	ghLeg(t, c, gh(full, 1), "completed", "success")
	refuses("own unfinished + gh green", 1, "", "CIPENDING", pending)

	// (3) ci cards: one OK + one FAIL is FAIL; one OK + one with no
	// verdict yet is pending; one OK alone is OK
	ciCard(c, full, head(2), "g1", "OK")
	ciCard(c, full, head(2), "g2", "FAIL")
	refuses("ci cards OK + FAIL", 2, "", "CIRED", "ci card verdict FAIL")
	ciCard(c, full, head(3), "g1", "OK")
	ciCard(c, full, head(3), "g2", "")
	refuses("ci cards OK + pending", 3, "", "CIPENDING", pending)
	c.HSet(ctx, "ci:"+full+":"+head(3)+":g2", "verdict", "OK")
	passes("ci cards both OK", 3)

	// (4) the two spellings conflicting: FAIL wins (own final under the
	// bare name OK, under owner/name FAIL; gh green bare, red owner/name)
	c.HSet(ctx, "ci:"+bare+":"+head(4), "ci", "green", "final", "OK")
	c.HSet(ctx, "ci:"+full+":"+head(4), "ci", "red", "final", "FAIL", "why", "unit")
	refuses("own OK bare + own FAIL owner/name", 4, "", "CIRED", "unit")
	ghLeg(t, c, gh(bare, 5), "completed", "success")
	ghLeg(t, c, gh(full, 5), "completed", "failure")
	refuses("gh green bare + gh red owner/name", 5, "", "CIRED", "github red wf:ci")

	// (5) a gh green at another head says nothing at this one, and a score
	// naming a head the PR record does not (--head) is FAIL even with gh
	// green there (the copy's own head moving past is the end's fail
	// outcome before the gate, tableMoves)
	ghLeg(t, c, gh(bare, 16), "completed", "success")
	refuses("gh green at another head", 6, "", "CIPENDING", pending)
	refuses("head moved", 6, head(16), "CIRED", "head moved to "+head(6)[:12])

	// (6) no gh leg + own final OK is OK; no own run + gh green is OK
	// (under either spelling); gh pending is pending, also with the PR
	// record green (a run in flight is the newest word)
	passes("own OK, no gh", 7)
	refuses("no source", 8, "", "CIPENDING", pending)
	ghLeg(t, c, gh(full, 8), "completed", "success")
	passes("gh green owner/name, no own", 8)
	ghLeg(t, c, gh(bare, 9), "in_progress", "")
	refuses("gh pending", 9, "", "CIPENDING", pending)
	c.HSet(ctx, "pr:nova-tools:7009", "ci", "green", "ci_sha", head(9))
	refuses("gh pending over pr record green", 9, "", "CIPENDING", pending)
	c.Del(ctx, gh(bare, 9))
	ghLeg(t, c, gh(bare, 9), "completed", "success")
	passes("gh green bare", 9)

	// (7) the PR record's ci word, fourth: nothing at another ci_sha, its
	// creation default (pending, no ci_sha) nothing, pending at this head
	// pending, red at this head FAIL with its why, green with no ci_sha or
	// at this head OK
	c.HSet(ctx, "pr:nova-tools:7010", "ci", "green", "ci_sha", head(17))
	refuses("pr record green at another head", 10, "", "CIPENDING", pending)
	c.HSet(ctx, "pr:nova-tools:7010", "ci", "pending", "ci_sha", "")
	refuses("pr record creation default", 10, "", "CIPENDING", pending)
	c.HSet(ctx, "pr:nova-tools:7010", "ci", "pending", "ci_sha", head(10))
	refuses("pr record pending at this head", 10, "", "CIPENDING", pending)
	c.HSet(ctx, "pr:nova-tools:7010", "ci", "red", "ci_sha", head(10), "ci_why", "lint")
	refuses("pr record red", 10, "", "CIRED", "pr record ci red lint")
	c.HSet(ctx, "pr:nova-tools:7010", "ci", "green", "ci_sha", "", "ci_why", "")
	passes("pr record green, no ci_sha", 10)
	c.HSet(ctx, "pr:nova-tools:7011", "ci", "green", "ci_sha", head(11))
	passes("pr record green at this head", 11)
	cleanMoves(t, c, "gate")
}
