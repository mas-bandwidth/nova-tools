package reconcile_test

import (
	"context"
	"testing"

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

// TestDealRetryCapDefault: a missing, zero or non-numeric cfg:deal
// max_attempts is the default 3, never unbounded; a card at attempt 3 is
// capped, a card at attempt 2 is dealt.
func TestDealRetryCapDefault(t *testing.T) {
	t.Parallel()

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
