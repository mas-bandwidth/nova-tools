//go:build functional

package consume

import (
	"strings"
	"testing"

	"github.com/redis/go-redis/v9"
)

// An ack or an idempotence mark Redis refused is counted and named on the
// pass receipt and in the pass's error, and the pass goes on: the event
// stays pending and comes back next pass. Before this both writes were
// dropped and a refused ack was a silent redelivery. The faults are the
// store's own: s:<S>:idem made a string so HSET is WRONGTYPE, and XACK taken
// away from the user so every ack is NOPERM.
func TestPRToReadCountsTheAcksItCouldNotMake(t *testing.T) {
	t.Parallel()

	f := newFirstReadFixture(t, "ack-fail")
	f.up("emma", "1", 0, 0)
	// One head event of no card's PR (acked as a NOOP with an idem mark) and
	// one event that is not a head at all (acked outright).
	head, err := f.client.XAdd(f.ctx, &redis.XAddArgs{Stream: "s:ack-fail:log", Values: []any{
		"kind", "pr head", "repo", "nova-tools", "pr", "9", "head", strings.Repeat("9", 40),
		"prev", "", "source", "ls-remote", "at", "1"}}).Result()
	if err != nil {
		t.Fatal(err)
	}
	other, err := f.client.XAdd(f.ctx, &redis.XAddArgs{Stream: "s:ack-fail:log", Values: []any{
		"kind", "card queued", "label", "c-1"}}).Result()
	if err != nil {
		t.Fatal(err)
	}
	if err := f.client.Set(f.ctx, "s:ack-fail:idem", "not-a-hash", 0).Err(); err != nil {
		t.Fatal(err)
	}
	if err := f.client.Do(f.ctx, "ACL", "SETUSER", "default", "-xack").Err(); err != nil {
		t.Fatal(err)
	}

	err = f.pr.Once(f.ctx)
	if err == nil {
		t.Fatal("a pass with refused acks returned no error")
	}
	for _, want := range []string{
		"pr-to-read: 3 acks failed, each event comes back next pass: ",
		"idem " + head + " on s:ack-fail:idem: WRONGTYPE",
		"ack " + head + " on s:ack-fail:log: NOPERM",
		"ack " + other + " on s:ack-fail:log: NOPERM",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the pass error %q does not carry %q", err.Error(), want)
		}
	}
	receipt, rerr := f.client.HGet(f.ctx, "proc:pr-to-read", "err").Result()
	if rerr != nil || !strings.Contains(receipt, "3 acks failed") || !strings.Contains(receipt, "ack "+other+" on s:ack-fail:log") {
		t.Errorf("proc:pr-to-read err = %q (%v), want the count and the names", receipt, rerr)
	}
	for _, want := range []string{
		"PR-TO-READ ACK-FAILED what=idem id=" + head + " key=s:ack-fail:idem: ",
		"PR-TO-READ ACK-FAILED what=ack id=" + head + " key=s:ack-fail:log: ",
		"PR-TO-READ ACK-FAILED what=ack id=" + other + " key=s:ack-fail:log: ",
	} {
		if !strings.Contains(f.out.String(), want) {
			t.Errorf("out lacks %q:\n%s", want, f.out.String())
		}
	}
	pending, perr := f.client.XPending(f.ctx, "s:ack-fail:log", RulePRToRead).Result()
	if perr != nil || pending.Count != 2 {
		t.Errorf("pr-to-read pending = %+v (%v), want both events back next pass", pending, perr)
	}
}
