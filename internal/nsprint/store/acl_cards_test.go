//go:build functional

package store_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/redis/go-redis/v9"
)

// withoutCardVerbs is an actor's rule without its card verb grants
// (+fcall|ns_cm_*): the seat as it was before #3998, the control's red side.
func withoutCardVerbs(rule string) []string {
	var out []string
	for _, tok := range strings.Split(rule, " ") {
		if !strings.HasPrefix(tok, "+fcall|ns_cm_") {
			out = append(out, tok)
		}
	}
	return out
}

// TestACLSeatsRunTheCardVerbs is #3998's seat control: the bench seat
// (ns-bench: bench beat's copy session, nova-card copy) and the friend seat
// (ns-friend: friend serve) run consumer copies through the table moves under
// their own ACL user, every card verb they call: card work --fill
// (ns_cm_work), card beat (ns_cm_beat), card end (ns_cm_end) and the
// session's give-back, card cancel (ns_cm_cancel), plus the plain reads the
// harnesses make (consumers, the copy's record, cfg:card, the consumer's
// slots and sets). The same seat without the ns_cm_* grants is refused card
// work, so the control can fail.
func TestACLSeatsRunTheCardVerbs(t *testing.T) {
	t.Parallel()
	rules := map[string]string{}
	for _, rule := range store.ACLRules {
		name, body, _ := strings.Cut(rule, " ")
		rules[name] = body
	}
	users := map[string][]string{
		"ns-deploy":      strings.Split(rules["ns-deploy"], " "),
		"ns-bench":       strings.Split(rules["ns-bench"], " "),
		"ns-friend":      strings.Split(rules["ns-friend"], " "),
		"ns-bench-bare":  withoutCardVerbs(rules["ns-bench"]),
		"ns-friend-bare": withoutCardVerbs(rules["ns-friend"]),
	}
	var extra []string
	for name, toks := range users {
		extra = append(extra, "--user", name, "on", ">"+name+"-pass")
		extra = append(extra, toks...)
	}
	addr := testutil.Start(t, extra...)
	ctx := context.Background()
	as := func(name string) *redis.Client {
		c := redis.NewClient(&redis.Options{Addr: addr, Username: name, Password: name + "-pass"})
		t.Cleanup(func() { _ = c.Close() })
		return c
	}
	if err := fn.Load(ctx, as("ns-deploy")); err != nil {
		t.Fatalf("ns-deploy loads the library: %v", err)
	}

	// The coordinator's side, as the default user: enroll, push, deal.
	admin := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = admin.Close() })
	bench := taskcard.Consumer{Kind: "bench", Name: "b"}
	friend := taskcard.Consumer{Kind: "friend", Name: "f"}
	admin.SAdd(ctx, "benches", "b")
	admin.SAdd(ctx, "friends", "f")
	admin.HSet(ctx, bench.DesiredKey(), "slots", "3")
	admin.HSet(ctx, friend.DesiredKey(), "slots", "2")
	admin.HSet(ctx, card.CardConfigKey, "wall_max_min", "30")
	prHead := strings.Repeat("a", 40)
	admin.HSet(ctx, "pr:nova-tools:77", "head", prHead, "base", "dev")
	for _, k := range []taskcard.Consumer{bench, friend} {
		if err := taskcard.Enroll(ctx, admin, k, true); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 5; i++ {
		id := fmt.Sprintf("p%02d", i)
		if _, err := taskcard.Push(ctx, admin, taskcard.PushRequest{ID: id, Where: "waiting", Stream: "swarm: cards",
			Sprint: "acl", Kind: "build", Ref: fmt.Sprintf("nova-tools#%d", 5000+i), Title: "primary " + id,
			Repo: "mas-bandwidth/nova-tools", By: "rowan", Fields: []string{"base", "dev", "base_sha",
				strings.Repeat("0", 40), "paths", "internal/x.go", "done_when", "go test ./internal/x passes"}}); err != nil {
			t.Fatalf("push %s: %v", id, err)
		}
	}
	for _, d := range []struct {
		to taskcard.Consumer
		n  int
	}{{bench, 3}, {friend, 2}} {
		if got, err := taskcard.Deal(ctx, admin, taskcard.DealRequest{To: d.to, N: d.n, By: "rowan"}); err != nil || len(got) != d.n {
			t.Fatalf("deal %s: %v %v", d.to, got, err)
		}
	}

	// A seat without the card verb grants is refused card work.
	for name, k := range map[string]taskcard.Consumer{"ns-bench-bare": bench, "ns-friend-bare": friend} {
		if _, err := taskcard.Work(ctx, as(name), k, name, 0, true); err == nil || !strings.Contains(err.Error(), "NOPERM") {
			t.Fatalf("%s card work --as %s: %v, want NOPERM", name, k, err)
		}
	}
	noperm := func(who, what string, err error) {
		t.Helper()
		if err != nil && !errors.Is(err, redis.Nil) {
			t.Fatalf("%s %s: %v", who, what, err)
		}
	}

	// The bench seat: the session start (one card work --fill, the third
	// launch fails and is given back with card cancel), then each copy's
	// ledger: claim, start ack, beat, end (one the card ended itself).
	bc := as("ns-bench")
	var launches []card.CopyLaunch
	s, err := card.OpenCopySession(ctx, bc, "b", "nova-card@b", func(l card.CopyLaunch) error {
		if len(launches) == 2 {
			return errors.New("no wrapper")
		}
		launches = append(launches, l)
		return nil
	})
	if err != nil || !s.Enrolled || len(s.Launched) != 2 || len(s.GivenBack) != 1 {
		t.Fatalf("ns-bench session %+v %v", s, err)
	}
	_, err = bc.HGet(ctx, card.CardConfigKey, "wall_max_min").Result()
	noperm("ns-bench", "HGET cfg:card", err)
	for i, l := range launches {
		led := &card.CopyLedger{Client: bc, Copy: l.Copy, Bench: "b", Token: l.Token}
		_, err := bc.HGetAll(ctx, taskcard.Key(l.Copy)).Result()
		noperm("ns-bench", "HGETALL "+taskcard.Key(l.Copy), err)
		for step, f := range map[string]func() (int, error){
			"claim":    func() (int, error) { return led.Claim(ctx, "nonce") },
			"launched": func() (int, error) { return led.Launched(ctx, "", "", 0) },
			"beat":     func() (int, error) { return led.Beat(ctx) },
		} {
			if code, err := f(); err != nil || code != 0 {
				t.Fatalf("ns-bench copy %s %s code=%d %v", l.Copy, step, code, err)
			}
		}
		end := card.WrapperEnd{Outcome: "FAILED", Reason: "crash", Exit: 3}
		if i == 0 {
			if _, err := taskcard.End(ctx, bc, taskcard.EndRequest{IDs: []string{l.Copy}, OK: true,
				DoneAlready: "c0ffee12", Token: l.Token, By: "model"}); err != nil {
				t.Fatalf("ns-bench card end --id %s --done-already: %v", l.Copy, err)
			}
			end = card.WrapperEnd{Outcome: "DONE", Reason: "done"}
		}
		if code, err := led.End(ctx, end); err != nil || code != 0 {
			t.Fatalf("ns-bench copy %s end code=%d %v", l.Copy, code, err)
		}
	}

	// The friend seat: serve's slot read, card work --fill, the copies'
	// records, one card beat, and card end from each child's line.
	fc := as("ns-friend")
	pipe := fc.Pipeline()
	pipe.HGet(ctx, friend.DesiredKey(), "slots")
	pipe.ZCard(ctx, friend.KeyAt(0, "working"))
	pipe.ZCard(ctx, friend.KeyAt(0, "ready"))
	if _, err := pipe.Exec(ctx); err != nil {
		t.Fatalf("ns-friend slot read: %v", err)
	}
	w, err := taskcard.Work(ctx, fc, friend, "f", 0, true)
	if err != nil || len(w.IDs) != 2 {
		t.Fatalf("ns-friend card work --fill: %+v %v", w, err)
	}
	for _, id := range w.IDs {
		_, err := fc.HGetAll(ctx, taskcard.Key(id)).Result()
		noperm("ns-friend", "HGETALL "+taskcard.Key(id), err)
	}
	if _, err := taskcard.BeatCopies(ctx, fc, friend, w.IDs...); err != nil {
		t.Fatalf("ns-friend card beat: %v", err)
	}
	for i, id := range w.IDs {
		_, err := fc.HGet(ctx, taskcard.Key(id), "where").Result()
		noperm("ns-friend", "HGET where", err)
		// DONE pr=<repo>#<n> head=<sha>: the primary moves to reading and
		// its read copy is cut in the same call
		req := taskcard.EndRequest{IDs: []string{id}, Token: w.Tokens[i], By: "f", OK: true,
			Repo: "mas-bandwidth/nova-tools", PR: "77", Head: prHead}
		if i == 1 {
			req = taskcard.EndRequest{IDs: []string{id}, Token: w.Tokens[i], By: "f", Why: "exit 1; no typed line"}
		}
		if _, err := taskcard.End(ctx, fc, req); err != nil {
			t.Fatalf("ns-friend card end --id %s: %v", id, err)
		}
	}

	if where := admin.HGet(ctx, taskcard.Key(taskcard.PrimaryOf(w.IDs[0])), "where").Val(); where != "review" {
		t.Errorf("primary of %s after its ok with a PR: where=%q, want review", w.IDs[0], where)
	}

	// The table reads the sets (the given-back copy counts as a fail): nothing left ready or working on either seat.
	rows, err := taskcard.ReadCells(ctx, admin, []taskcard.Consumer{bench, friend})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"bench:b ready=0 working=0 done=3 ok=1 fail=2 ok%=33", "friend:f ready=0 working=0 done=2 ok=1 fail=1 ok%=50"}
	for i, r := range rows {
		if r.Line() != want[i] {
			t.Errorf("table row %q, want %q", r.Line(), want[i])
		}
	}
	if f, err := taskcard.FsckMoves(ctx, admin, false); err != nil || f.Drift != 0 {
		t.Errorf("card fsck after the seats' moves: %+v %v", f, err)
	}
}
