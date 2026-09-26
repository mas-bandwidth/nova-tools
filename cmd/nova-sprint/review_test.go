//go:build functional

package main

import (
	"context"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/reconcile"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/redis/go-redis/v9"
)

// closedIssues is the stub forge the review tests swap in: no host is
// touched (tests never touch real remotes).
type closedIssues struct {
	mu   sync.Mutex
	seen []string
}

func (c *closedIssues) CloseIssue(_ context.Context, repo string, n int, comment string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.seen = append(c.seen, repo+"#"+strconv.Itoa(n)+" "+comment)
	return nil
}

// reviewStore is a throwaway redis-server with the function library, one
// bench and one primary whose copy failed with exit 1 (in review).
func reviewStore(t *testing.T, id string) (string, *redis.Client) {
	t.Helper()
	addr := testutil.Start(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = c.Close() })
	ctx := context.Background()
	if err := fn.Load(ctx, c); err != nil {
		t.Fatal(err)
	}
	c.HSet(ctx, "bench:b:desired", "slots", "2")
	if _, err := taskcard.Push(ctx, c, taskcard.PushRequest{ID: id, Where: "waiting", Stream: "swarm: cards", Kind: "build",
		Ref: "nova-tools#4000", Origin: "issue:mas-bandwidth/nova-tools#4000", Title: "t",
		Repo: "mas-bandwidth/nova-tools", By: "rowan"}); err != nil {
		t.Fatal(err)
	}
	failOnce(t, c, id)
	return addr, c
}

// failOnce deals id to bench:b, starts it and ends it --fail exit 1.
func failOnce(t *testing.T, c *redis.Client, id string) {
	t.Helper()
	ctx := context.Background()
	b, _ := taskcard.ParseConsumer("bench:b")
	d, err := taskcard.Deal(ctx, c, taskcard.DealRequest{To: b, IDs: []string{id}, By: "rowan"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := taskcard.Work(ctx, c, b, "rowan", 0, false, d[0].Copy); err != nil {
		t.Fatal(err)
	}
	e, err := taskcard.End(ctx, c, taskcard.EndRequest{IDs: []string{d[0].Copy}, Why: "child exit 1", By: "b",
		Fields: []string{"exit", "1"}})
	if err != nil || e[0].To != "review" {
		t.Fatalf("end --fail: %v %v", e, err)
	}
}

// seat is the --actor the seat check accepts in this environment.
func seat() string {
	if s := os.Getenv(seatEnv); s != "" {
		return s
	}
	return "rowan"
}

// TestReviewPostCLI (#4072): `nova-sprint review post --id <primary>
// --verdict recut|redeal|reassign:<consumer>|drop --why <text>` is typed
// (a missing or unknown verdict, or no why, is a usage refusal, exit 2), a
// primary not in review is REFUSED (exit 1, nothing written), redeal prints
// one receipt line and moves it to ready, and drop moves it to landed and
// closes its origin issue with the why.
func TestReviewPostCLI(t *testing.T) {
	t.Parallel()
	addr, c := reviewStore(t, "rv1")
	ctx := context.Background()
	post := func(args ...string) (int, string, string) {
		return runSprint(append([]string{"review", "post", "--redis", addr}, args...)...)
	}
	for _, tc := range [][]string{
		{"--ids", "rv1", "--why", "x"},
		{"--ids", "rv1", "--verdict", "maybe", "--why", "x"},
		{"--ids", "rv1", "--verdict", "redeal"},
		{"--verdict", "redeal", "--why", "x"},
		{"--ids", "rv1", "--verdict", "reassign", "--why", "x"},
	} {
		if code, out, errOut := post(tc...); code != 2 || out != "" || !strings.Contains(errOut, "nova-sprint review post:") {
			t.Fatalf("review post %v = %d %q %q, want a usage refusal", tc, code, out, errOut)
		}
	}
	if w := c.HGet(ctx, taskcard.Key("rv1"), "where").Val(); w != "review" {
		t.Fatalf("a refused verdict moved the card to %q", w)
	}
	code, out, errOut := post("--ids", "rv1", "--verdict", "redeal", "--why", "transient")
	if code != 0 || !regexp.MustCompile(`^REVIEW POST id=rv1 verdict=redeal to=ready copy=- ms=\d+\n$`).MatchString(out) {
		t.Fatalf("redeal = %d %q %q", code, out, errOut)
	}
	code, out, _ = post("--ids", "rv1", "--verdict", "redeal", "--why", "again")
	if code != 1 || !strings.HasPrefix(out, `REVIEW POST REFUSED id=rv1 why="NOTREVIEW task:rv1 is ready, not review"`) {
		t.Fatalf("a verdict on a ready card = %d %q", code, out)
	}

	failOnce(t, c, "rv1")
	forge := &closedIssues{}
	reviewForge = func() reconcile.IssueCloser { return forge }
	code, out, errOut = post("--ids", "rv1", "--verdict", "drop", "--why", "superseded by #4100")
	if code != 0 || !regexp.MustCompile(`^REVIEW POST id=rv1 verdict=drop to=landed copy=- issue=mas-bandwidth/nova-tools#4000 closed=yes ms=\d+\n$`).MatchString(out) {
		t.Fatalf("drop = %d %q %q", code, out, errOut)
	}
	if len(forge.seen) != 1 || !strings.HasPrefix(forge.seen[0], "mas-bandwidth/nova-tools#4000 REVIEW verdict=drop by="+seat()+": superseded by #4100") {
		t.Fatalf("closed issues %v", forge.seen)
	}
	if h := c.HGetAll(ctx, taskcard.Key("rv1")).Val(); h["where"] != "landed" || h["outcome"] != "dropped" {
		t.Fatalf("dropped record %v", h)
	}
}
