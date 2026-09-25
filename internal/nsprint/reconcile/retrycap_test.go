package reconcile_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/deal"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/reconcile"
)

// refusingBench is a fake `ssh` to a bench whose card wrapper refuses every
// launch, the way sprint quack-0925 ran (2026-09-25 03:23-03:35Z: the bench
// ACL answered NOPERM on ws:* inside the first card move). The session
// reaches `card launch --stdin`, which prints the secrets banner on stderr,
// one REFUSED line per batch line on stdout (and on stderr), and exits 1.
// Every session appends one line to sessions.log. It lives in t.TempDir(), so
// testguard sees a fake.
const refusingBench = `#!/bin/bash
set -u
while [ $# -gt 0 ]; do
  case "$1" in
    -o) shift 2 ;;
    *) break ;;
  esac
done
echo "open $1" >> %q
IFS= read -r benchsh_line # internal/benchsh's exec line; the batch follows
echo "SECRETS EXEC OK as=ctl-cap keys=1 only=1 required=1 cmd=nova-sprint" >&2
n=0
while read -r s label attempt token; do
  n=$((n+1))
  line="REFUSED line=$n wrapper $s/$label/$attempt: REFUSED card launched NOPERM No permissions to access a key (ws:swarm: cards:working)"
  echo "$line"
  echo "$line" >&2
done
echo "LAUNCH started=0 refused=$n ms=3 over=false"
exit 1
`

type capFence string

func (f capFence) Token(context.Context) (string, error) { return string(f), nil }

// TestDealRetryCapRefusedLaunch is nova-tools #3700's DONE-WHEN: with a bench
// that refuses every launch, each deal pass keeps the launcher's REFUSED line
// as the why of the bench's ssh row and of the card record; after
// cfg:deal max_attempts deals the next pass does not deal the card again but
// moves it to done/fail (state refused, reason retries, why the last REFUSED
// line) with one receipt; the pool no longer holds it and card fsck is clean.
func TestDealRetryCapRefusedLaunch(t *testing.T) {
	const (
		sprint = "control-3700"
		bench  = "ctl-cap"
		label  = "cap-a"
		token  = "fence-3700"
		stream = "swarm: cards"
		max    = 3
	)
	ctx := context.Background()
	st, c := controlRedis(t)
	ck := "s:" + sprint + ":card:" + label
	for _, cmd := range [][]any{
		{"SADD", "benches", bench},
		{"HSET", "bench:" + bench + ":desired", "slots", "4"},
		{"HSET", "bench:" + bench + ":beat", "host", bench, "at", "1"},
		{"HSET", "bench:" + bench + ":state", "state", "UP", "at", "1"},
		{"SADD", "sprints", sprint},
		{"ZADD", "sprint:order", "1", sprint},
		{"HSET", "s:" + sprint, "status", "open"},
		{"HSET", "lease:reconciler", "instance", "ctl", "token", token, "host", "ctl-host"},
		{"HSET", "cfg:deal", "max_attempts", fmt.Sprint(max)},
		{"HSET", ck, "state", "queued", "priority", "0", "attempt", "0", "retries", "0",
			"base_sha", "0123456789abcdef", "stream", stream},
		{"ZADD", "s:" + sprint + ":pool", "0", label},
		{"SADD", "s:" + sprint + ":idx:card:queued", label},
	} {
		if err := c.Do(ctx, cmd...).Err(); err != nil {
			t.Fatal(err)
		}
	}
	dir := t.TempDir()
	sessions := filepath.Join(dir, "sessions.log")
	prog := filepath.Join(dir, "ssh")
	if err := os.WriteFile(prog, []byte(fmt.Sprintf(refusingBench, sessions)), 0o755); err != nil {
		t.Fatal(err)
	}
	fns := &reconcile.DealFunctions{Client: c, Actor: "reconciler"}
	pass := &deal.Pass{Source: deal.RedisSource{Client: c}, Fence: capFence(token), Reserver: fns, Row: fns, Gate: fns, Why: fns,
		Dialer: deal.Remote{Program: prog, ConnectTimeout: 5 * time.Second, RunTimeout: 20 * time.Second}}
	refusal := func(attempt int) string {
		return fmt.Sprintf("REFUSED line=1 wrapper %s/%s/%d: REFUSED card launched NOPERM No permissions to access a key (ws:swarm: cards:working)",
			sprint, label, attempt)
	}

	for attempt := 1; attempt <= max; attempt++ {
		res, err := pass.Run(ctx)
		if err != nil {
			t.Fatalf("pass %d: %v", attempt, err)
		}
		if res.Launched() != 0 || len(res.Benches) != 1 || len(res.Benches[0].Dealt) != 1 {
			t.Fatalf("pass %d: %+v, want one card dealt and refused", attempt, res.Benches)
		}
		h := c.HGetAll(ctx, ck).Val()
		if h["state"] != "dealt" || h["attempt"] != fmt.Sprint(attempt) || h["why"] != refusal(attempt) {
			t.Fatalf("pass %d: card %v, want dealt at attempt %d with why %q", attempt, h, attempt, refusal(attempt))
		}
		row := c.HGetAll(ctx, deal.RowKey(bench)).Val()
		if row["state"] != deal.SSHError || row["why"] != refusal(attempt) {
			t.Fatalf("pass %d: ssh row %v, want error with the REFUSED line (not the secrets banner)", attempt, row)
		}
		// The start-ack window passes with no launched ack (a fake clock:
		// the reservation is dated at the epoch), so the sweep requeues.
		if err := c.HSet(ctx, ck, "dealt_at", "0").Err(); err != nil {
			t.Fatal(err)
		}
		got, err := reconcile.Reclaim(ctx, st, reconcile.ReclaimRequest{Sprint: sprint, Label: label, Fence: token, Windows: reconcile.DefaultWindows})
		if err != nil || got.Status != "QUEUED" {
			t.Fatalf("reclaim after attempt %d: %+v, %v", attempt, got, err)
		}
	}

	for extra := 0; extra < 2; extra++ {
		res, err := pass.Run(ctx)
		if err != nil {
			t.Fatalf("capped pass %d: %v", extra, err)
		}
		for _, b := range res.Benches {
			if len(b.Dealt) != 0 {
				t.Fatalf("capped pass %d dealt %d cards: %+v", extra, len(b.Dealt), res.Benches)
			}
		}
	}
	if raw, _ := os.ReadFile(sessions); strings.Count(string(raw), "\n") != max {
		t.Fatalf("bench sessions:\n%s\nwant exactly %d: a capped card is never sent to a bench", raw, max)
	}
	h := c.HGetAll(ctx, ck).Val()
	if h["state"] != "refused" || h["where"] != "done" || h["where_ok"] != "fail" || h["reason"] != "retries" ||
		h["why"] != refusal(max) || h["attempt"] != fmt.Sprint(max) || h["retries"] != fmt.Sprint(max) {
		t.Fatalf("capped card %v, want done/fail refused, reason retries, why the last REFUSED line, attempt %d", h, max)
	}
	if _, err := c.ZScore(ctx, "s:"+sprint+":pool", label).Result(); err == nil {
		t.Fatal("the capped card is still in the pool")
	}
	if n := c.ZCard(ctx, "s:"+sprint+":pool").Val(); n != 0 {
		t.Fatalf("pool holds %d cards, want 0", n)
	}
	// The card stays on the bench it last failed on, so the host row's fail
	// cell counts it.
	for _, k := range []string{card.BenchCardsKey(bench, "done"), card.BenchCardsKey(bench, "fail"), "ws:" + stream + ":done"} {
		if _, err := c.ZScore(ctx, k, ck).Result(); err != nil {
			t.Fatalf("%s does not hold the capped card: %v", k, err)
		}
	}
	msgs := c.XRange(ctx, "s:"+sprint+":log", "-", "+").Val()
	var receipts []string
	for _, m := range msgs {
		if m.Values["kind"] == "card retries" && m.Values["id"] == label {
			receipts = append(receipts, fmt.Sprint(m.Values["reason"], " ", m.Values["attempt"], " ", m.Values["evidence"]))
		}
	}
	if len(receipts) != 1 || receipts[0] != "retries 3 "+refusal(max) {
		t.Fatalf("card retries receipts %q, want exactly one naming the last refusal", receipts)
	}
	rep, err := card.Fsck(ctx, c, sprint, false)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Drift != 0 || rep.Cards != 1 || rep.Done != 1 || rep.Fail != 1 || rep.Ready != 0 {
		t.Fatalf("%s\n%s", rep.Line("FSCK"), strings.Join(rep.Lines, "\n"))
	}
}

// TestDealRetryCapDefault: a missing, zero or non-numeric cfg:deal
// max_attempts is the default 3, never unbounded; a card at attempt 3 is
// capped, a card at attempt 2 is dealt.
func TestDealRetryCapDefault(t *testing.T) {
	const sprint, bench, token = "control-3700d", "ctl-capd", "fence-3700d"
	ctx := context.Background()
	_, c := controlRedis(t)
	for _, v := range []string{"", "0", "lots"} {
		if err := c.FlushAll(ctx).Err(); err != nil {
			t.Fatal(err)
		}
		cmds := [][]any{
			{"SADD", "benches", bench},
			{"HSET", "bench:" + bench + ":desired", "slots", "4"},
			{"HSET", "bench:" + bench + ":state", "state", "UP"},
			{"HSET", "s:" + sprint, "status", "open"},
			{"HSET", "lease:reconciler", "token", token},
		}
		if v != "" {
			cmds = append(cmds, []any{"HSET", "cfg:deal", "max_attempts", v})
		}
		for _, l := range []struct{ label, attempt string }{{"at-two", "2"}, {"at-three", "3"}} {
			cmds = append(cmds,
				[]any{"HSET", "s:" + sprint + ":card:" + l.label, "state", "queued", "priority", "0", "attempt", l.attempt,
					"retries", l.attempt, "reason", "spawn-timeout", "base_sha", "0123456789abcdef"},
				[]any{"ZADD", "s:" + sprint + ":pool", "0", l.label},
				[]any{"SADD", "s:" + sprint + ":idx:card:queued", l.label})
		}
		for _, cmd := range cmds {
			if err := c.Do(ctx, cmd...).Err(); err != nil {
				t.Fatal(err)
			}
		}
		fns := &reconcile.DealFunctions{Client: c, Actor: "reconciler"}
		res, err := fns.Reserve(ctx, token, bench, []deal.Card{{Sprint: sprint, Label: "at-two"}, {Sprint: sprint, Label: "at-three"}})
		if err != nil {
			t.Fatal(err)
		}
		if len(res) != 1 || res[0].Card.Label != "at-two" || res[0].Attempt != 3 {
			t.Fatalf("max_attempts %q: dealt %+v, want at-two at attempt 3 only", v, res)
		}
		h := c.HGetAll(ctx, "s:"+sprint+":card:at-three").Val()
		want := "retries: 3 attempts reached cfg:deal max_attempts 3; last reason spawn-timeout"
		if h["state"] != "refused" || h["where"] != "done" || h["where_ok"] != "fail" || h["why"] != want {
			t.Fatalf("max_attempts %q: at-three %v, want done/fail with why %q", v, h, want)
		}
	}
}
