//go:build functional

package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws/wstest"
	"github.com/redis/go-redis/v9"
)

const oneCountSHA = "0123456789abcdef0123456789abcdef01234567"

// oneCountPlace moves a pushed card to where through the library's one task
// move, the way the verbs do: ready -> working -> review | merging -> landed
// at a sha, parked, or done/fail (cancel).
func oneCountPlace(t *testing.T, c *redis.Client, id, where string) {
	t.Helper()
	ctx := context.Background()
	move := func(to string, o taskcard.Opts) {
		t.Helper()
		o.By = "one-count"
		if o.Why == "" {
			o.Why = "fixture"
		}
		if _, err := taskcard.Move(ctx, c, id, to, o); err != nil {
			t.Fatalf("%s -> %s: %v", id, to, err)
		}
	}
	switch where {
	case ws.Waiting, ws.Ready:
	case ws.Parked:
		move(ws.Parked, taskcard.Opts{})
	case ws.Done:
		if _, err := taskcard.Cancel(ctx, c, id, "one-count", "not needed"); err != nil {
			t.Fatalf("cancel %s: %v", id, err)
		}
	case ws.Review:
		// review is a copy's end: deal a copy to bench:b, start it, end it
		// --fail exit 1 (the typed-verdict wait, #4072)
		c.HSet(ctx, "bench:b:desired", "slots", "2")
		b, _ := taskcard.ParseConsumer("bench:b")
		d, err := taskcard.Deal(ctx, c, taskcard.DealRequest{To: b, IDs: []string{id}, By: "one-count"})
		if err != nil {
			t.Fatalf("deal %s: %v", id, err)
		}
		if _, err := taskcard.Work(ctx, c, b, "one-count", 0, false, d[0].Copy); err != nil {
			t.Fatalf("work %s: %v", d[0].Copy, err)
		}
		if e, err := taskcard.End(ctx, c, taskcard.EndRequest{IDs: []string{d[0].Copy}, Why: "child exit 1", By: "b",
			Fields: []string{"exit", "1"}}); err != nil || e[0].To != ws.Review {
			t.Fatalf("end --fail %s: %v %v", id, e, err)
		}
	default:
		move(ws.Working, taskcard.Opts{As: "f1", Friend: "f1", SetFriend: true})
		switch where {
		case ws.Merging:
			move(where, taskcard.Opts{})
		case ws.Landed:
			move(ws.Merging, taskcard.Opts{})
			if _, err := taskcard.Land(ctx, c, id, "one-count", oneCountSHA, ""); err != nil {
				t.Fatalf("land %s: %v", id, err)
			}
		}
	}
}

// TestOneCountFunctional (#one-count): the one-count fixture pushed through
// the real library on a throwaway redis-server (ns_sprint_begin and
// ns_sprint_open, ns_tcard_push creating each stream's sentinel, ns_tcard_move
// for every state, a land at a sha), then sprint status, the table headline,
// the live loop's headline, the total row and ws counts print the same
// numbers; landing one more card moves them all together.
func TestOneCountFunctional(t *testing.T) {
	t.Parallel()
	_, c := wstest.Start(t)
	ctx := context.Background()
	st := store.New(c)
	opened := time.Now()
	if line, err := sprint.Begin(ctx, st, oneCountSprint, "one-count.lisp", "sha-one-count", opened); err != nil || line != "" {
		t.Fatalf("begin: %q %v", line, err)
	}
	if line, err := sprint.Finish(ctx, st, oneCountSprint, "one-count.lisp", "sha-one-count", opened, nil); err != nil || line != "" {
		t.Fatalf("open: %q %v", line, err)
	}
	for _, card := range oneCountCards {
		where := ws.Ready
		if card.where == ws.Waiting || card.where == ws.Parked {
			where = ws.Waiting
		}
		if _, err := taskcard.Push(ctx, c, taskcard.PushRequest{ID: card.id, Where: where, Stream: card.stream, Sprint: oneCountSprint,
			Kind: "build", Ref: "nova-tools#4000", Origin: "issue:mas-bandwidth/nova-tools#4000", Repo: "mas-bandwidth/nova-tools",
			Title: "STREAM: " + card.stream + " | " + card.id, By: "one-count"}); err != nil {
			t.Fatalf("push %s: %v", card.id, err)
		}
		oneCountPlace(t, c, card.id, card.where)
	}
	for _, s := range oneCountStreams {
		if n, _ := c.ZScore(ctx, ws.Key(s, ws.Waiting), ws.SentinelID(s)).Result(); n == 0 {
			t.Fatalf("stream %s has no sentinel waiting", s)
		}
	}

	now := time.Now() // after every move: the three lands are in its hour
	got, cells := oneCountPrintouts(t, c, now)
	want := got["ws counts"]
	if want.done != 3 || want.total != 14 || want.left != 11 || want.pct != 21 || !strings.HasSuffix(want.eta, " ET") {
		t.Fatalf("ws counts %+v; want 3/14 21%%, left 11, an eta in ET", want)
	}
	assertOneCount(t, got, want)
	if cells != [6]int{5, 2, 2, 1, 1, 3} {
		t.Fatalf("total row %v; want waiting 5 (2 cards, 3 sentinels) ready 2 working 2 review 1 merging 1 landed 3", cells)
	}

	// Mutation: g2 lands through the library. Every printout moves together.
	oneCountPlace(t, c, "g2", ws.Merging)
	if _, err := taskcard.Land(ctx, c, "g2", "one-count", oneCountSHA, ""); err != nil {
		t.Fatalf("land g2: %v", err)
	}
	now = time.Now()
	got, cells = oneCountPrintouts(t, c, now)
	want = got["ws counts"]
	if want.done != 4 || want.total != 14 || want.left != 10 || want.pct != 28 {
		t.Fatalf("after g2 lands ws counts %+v; want 4/14 28%%, left 10", want)
	}
	assertOneCount(t, got, want)
	if cells != [6]int{5, 2, 1, 1, 1, 4} {
		t.Fatalf("total row after the land %v", cells)
	}
}
