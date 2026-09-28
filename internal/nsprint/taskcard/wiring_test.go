//go:build functional

package taskcard_test

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
)

// TestConsumerWiringEndToEnd is the #3998 DONE-WHEN: the production harnesses
// run COPIES through the table moves and nothing else. A fake bench session
// (card.OpenCopySession, what `bench beat` runs each tick and `card session`
// runs once) takes its copies with one `card work --fill` call, and each
// copy's ledger (card.CopyLedger, what nova-card copy runs) beats it and
// ends it: one the card ended itself, one a crashed harness, one a DONE
// with no card end. The friend takes its copies with the same `card work
// --fill` and ends them under their tokens. The host and friend tables then print ready/working/done/ok/
// fail from the consumer sets alone, no old lease ledger key exists, both
// fsck walks are clean, and the Lua library names the old ledgers only in
// the move file.
func TestConsumerWiringEndToEnd(t *testing.T) {
	t.Parallel()
	c := start(t)
	ctx := context.Background()
	bench, friend := mustConsumer(t, "bench:b"), mustConsumer(t, "friend:f")
	c.SAdd(ctx, "benches", "b")
	c.SAdd(ctx, "friends", "f")
	c.HSet(ctx, bench.DesiredKey(), "slots", "3")
	c.HSet(ctx, friend.DesiredKey(), "slots", "2")
	for _, k := range []taskcard.Consumer{bench, friend} {
		if err := taskcard.Enroll(ctx, c, k, true); err != nil {
			t.Fatal(err)
		}
	}
	ids := pushPrimaries(t, c, 5)
	if d, err := taskcard.Deal(ctx, c, taskcard.DealRequest{To: bench, N: 3, By: "rowan"}); err != nil || len(d) != 3 {
		t.Fatalf("deal bench %v %v", d, err)
	}
	if d, err := taskcard.Deal(ctx, c, taskcard.DealRequest{To: friend, N: 2, By: "rowan"}); err != nil || len(d) != 2 {
		t.Fatalf("deal friend %v %v", d, err)
	}
	wantCells(t, cellsOf(t, c, bench), "dealt", 3, 0, 0, 0)
	wantCells(t, cellsOf(t, c, friend), "dealt", 2, 0, 0, 0)
	cleanMoves(t, c, "dealt")

	// The bench session start: one card work --fill, one launch per copy.
	var launched []card.CopyLaunch
	calls := countCalls(c)
	s, err := card.OpenCopySession(ctx, c, "b", "bench:b", func(l card.CopyLaunch) error {
		launched = append(launched, l)
		return nil
	})
	if err != nil || !s.Enrolled || len(s.Launched) != 3 || len(launched) != 3 || s.Free != 0 {
		t.Fatalf("session %+v %v", s, err)
	}
	if n := countCalls(c) - calls; n != 1 {
		t.Fatalf("session start took %d calls, want one card work --fill", n)
	}
	wantCells(t, cellsOf(t, c, bench), "session", 0, 3, 0, 0)
	cleanMoves(t, c, "session")

	// Each copy's wrapper ledger: the card is dealt to this bench under its
	// token, the start ack and a beat renew its lease, and it ends.
	for i, l := range launched {
		led := &card.CopyLedger{Client: c, Copy: l.Copy, Bench: "b", Token: l.Token}
		wc, err := led.Card(ctx)
		if err != nil || wc.State != "dealt" || wc.Bench != "b" || wc.Attempt != 1 {
			t.Fatalf("copy %s card %+v %v", l.Copy, wc, err)
		}
		if id, err := card.ParseIdentity(wc.Identity); err != nil || id.Sprint != card.CopySprint || id.Label != card.CopyCardLabel(l.Copy) {
			t.Fatalf("copy %s identity %q %v", l.Copy, wc.Identity, err)
		}
		for step, f := range map[string]func() (int, error){
			"claim":    func() (int, error) { return led.Claim(ctx, "nonce") },
			"launched": func() (int, error) { return led.Launched(ctx, "", "", time.Minute) },
			"beat":     func() (int, error) { return led.Beat(ctx) },
		} {
			if code, err := f(); err != nil || code != 0 {
				t.Fatalf("copy %s %s code=%d %v", l.Copy, step, code, err)
			}
		}
		if lease := c.HGet(ctx, taskcard.Key(l.Copy), "lease_until").Val(); lease == "" {
			t.Fatalf("copy %s has no lease after its beat", l.Copy)
		}
		var end card.WrapperEnd
		switch i {
		case 0:
			// the card ends itself (its card says card end --id), then the
			// wrapper's end writes nothing
			if _, err := taskcard.End(ctx, c, taskcard.EndRequest{IDs: []string{l.Copy}, OK: true,
				DoneAlready: "c0ffee12", Token: l.Token, By: "model"}); err != nil {
				t.Fatalf("the card's own end: %v", err)
			}
			end = card.WrapperEnd{Outcome: "DONE", Reason: "done"}
		case 1:
			end = card.WrapperEnd{Outcome: "FAILED", Reason: "crash", Exit: 3, Why: "exit 3"}
		case 2:
			end = card.WrapperEnd{Outcome: "DONE", Reason: "done", PushedSHA: "89abcdef"}
		}
		if code, err := led.End(ctx, end); err != nil || code != 0 {
			t.Fatalf("copy %s end code=%d %v", l.Copy, code, err)
		}
	}
	wantCells(t, cellsOf(t, c, bench), "bench ended", 0, 0, 1, 2)
	// a work copy's DONE with a commit is the wrapper's push and PR
	// (#4227); this ledger has no push credential, so it ends fail no-token
	// with the commit and the branch kept on the record
	if rec := c.HGetAll(ctx, taskcard.Key(launched[2].Copy)).Val(); !strings.HasPrefix(rec["why"], "no-token: GH_PUSH_TOKEN") ||
		rec["commit"] != "89abcdef" || !strings.HasPrefix(rec["branch"], "nova/copies/") {
		t.Fatalf("a DONE work copy with a commit and no push credential: why %q commit %q branch %q", rec["why"], rec["commit"], rec["branch"])
	}
	// a fenced beat: the ended copy is no longer working here
	if code, _ := (&card.CopyLedger{Client: c, Copy: launched[1].Copy, Bench: "b", Token: launched[1].Token}).Beat(ctx); code != card.WrapperExitFenced {
		t.Fatalf("beat of an ended copy code=%d, want fenced", code)
	}
	cleanMoves(t, c, "bench ended")

	// The friend: the same card work --fill a seat runs at spawn, then card
	// end under each copy's token from the child's typed line.
	w, err := taskcard.Work(ctx, c, friend, "f", 2, true)
	if err != nil || len(w.IDs) != 2 {
		t.Fatalf("friend card work --fill %+v %v", w, err)
	}
	for i, id := range w.IDs {
		if _, err := taskcard.End(ctx, c, taskcard.EndRequest{IDs: []string{id}, OK: true,
			DoneAlready: "c0ffee12", Token: w.Tokens[i], By: "f", Fields: []string{"evidence", "DONE built " + id}}); err != nil {
			t.Fatalf("friend copy %s end: %v", id, err)
		}
	}
	wantCells(t, cellsOf(t, c, friend), "friend ended", 0, 0, 2, 0)
	cleanMoves(t, c, "friend ended")
	landed := 0
	for _, id := range ids {
		if c.HGet(ctx, taskcard.Key(id), "where").Val() == "landed" {
			landed++
		}
	}
	if landed != 3 {
		t.Fatalf("landed primaries %d, want 3 (one bench copy, two friend copies)", landed)
	}

	// The tables are the sets: ZCARDs, done = ok + fail derived.
	rows, err := taskcard.ReadCells(ctx, c, []taskcard.Consumer{bench, friend})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"bench:b ready=0 working=0 done=3 ok=1 fail=2 ok%=33", "friend:f ready=0 working=0 done=2 ok=2 fail=0 ok%=100"}
	for i, r := range rows {
		if r.Line() != want[i] {
			t.Fatalf("table row %q, want %q", r.Line(), want[i])
		}
	}
	var old []string
	for _, pattern := range []string{"*:living", "*:starting"} {
		keys, err := c.Keys(ctx, pattern).Result()
		if err != nil {
			t.Fatal(err)
		}
		old = append(old, keys...)
	}
	if len(old) != 0 {
		t.Fatalf("old lease ledger keys left: %v", old)
	}

	// The Lua library: only the move file names the old ledgers.
	files, err := filepath.Glob(filepath.Join("..", "fn", "lua", "*.lua"))
	if err != nil || len(files) < 10 {
		t.Fatalf("lua files %d %v", len(files), err)
	}
	re := regexp.MustCompile(`living|starting`)
	var named []string
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if re.Match(b) {
			named = append(named, filepath.Base(f))
		}
	}
	sort.Strings(named)
	if strings.Join(named, " ") != "02_card_move.lua" {
		t.Fatalf("files naming living|starting: %v, want only 02_card_move.lua", named)
	}
}
