//go:build functional

package main

import (
	"bytes"
	"context"
	"errors"
	"net"
	"regexp"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/reconcile"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/redis/go-redis/v9"
)

// The controls of the no-silent-failure sweep (Glenn 2026-09-26 11:30 AM ET:
// "every verb in nova tools related to current work should not fail
// silently"): each was a place a failure vanished, and each now prints one
// typed line and exits non-zero, or reaches the duty's DUTY line.

// silentRedis is a throwaway redis-server with the function library loaded
// and nothing else: no t.Setenv, so every test here runs in parallel and
// names its store with --redis.
func silentRedis(t *testing.T) (string, *redis.Client) {
	t.Helper()
	addr := testutil.Start(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = c.Close() })
	if err := fn.Load(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	return addr, c
}

// silentClosedAddr is a loopback port with nothing listening: a Redis that
// does not answer.
func silentClosedAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	return addr
}

// TestCardRenderNamesTheStoreNotNOTASK: a store that does not answer used to
// render as `CARD RENDER REFUSED ... why=NOTASK`, a missing card. It is the
// Redis error on stderr, exit 2.
func TestCardRenderNamesTheStoreNotNOTASK(t *testing.T) {
	t.Parallel()
	code, out, errOut := runCLI("card", "render", "--ids", "c0~1", "--redis", silentClosedAddr(t))
	if code != 2 || out != "" || strings.Contains(errOut, "NOTASK") || strings.Contains(errOut, "NOCOPY") ||
		!strings.Contains(errOut, "nova-sprint card render:") || !strings.Contains(errOut, "connect") {
		t.Fatalf("render on a dead store = %d %q %q; want exit 2 with the dial error, never NOTASK", code, out, errOut)
	}
}

// TestCardExpireNamesTheCopyItCouldNotEnd: a lapsed copy the move refused to
// finish (its record is gone: NOCOPY) stayed in working with nothing said,
// every sweep. The sweep now answers the pair `<id> REFUSED <why>`: the verb
// prints EXPIRE <id> REFUSED why=... and exits 1, and the deal duty's pass
// carries the same line into its DUTY err.
func TestCardExpireNamesTheCopyItCouldNotEnd(t *testing.T) {
	t.Parallel()
	addr, c := silentRedis(t)
	ctx := context.Background()
	c.HSet(ctx, "bench:b:desired", "slots", "2")
	if code, out, errOut := runTaskCLI("push", "--redis", addr, "--as", seat(), "--ids", "sx0", "--stream", "swarm: cards", "--waiting",
		"--kind", "build", "--repo", "mas-bandwidth/nova-tools", "--title", "t"); code != 0 {
		t.Fatalf("push %d %q %q", code, out, errOut)
	}
	if code, out, _ := runCLI("card", "deal", "--redis", addr, "--to", "bench:b", "--ids", "sx0"); code != 0 {
		t.Fatalf("deal = %d %q", code, out)
	}
	if code, out, _ := runCLI("card", "work", "--redis", addr, "--as", "bench:b", "--fill"); code != 0 {
		t.Fatalf("work = %d %q", code, out)
	}
	// The copy's record vanishes while it works; its lease lapses.
	if err := c.Del(ctx, "task:sx0~1").Err(); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := runCLI("card", "expire", "--redis", addr, "--as", "bench:b")
	if code != 1 || !strings.Contains(out, "EXPIRE sx0~1 REFUSED why=\"NOCOPY task:sx0~1\"") ||
		!regexp.MustCompile(`CARD EXPIRE n=0 refused=1 ms=\d+\n$`).MatchString(out) {
		t.Fatalf("expire = %d %q %q; want the refused copy named and exit 1", code, out, errOut)
	}
	if n := c.ZCard(ctx, "bench:b:cards:working").Val(); n != 1 {
		t.Fatalf("the refused copy left working (%d in the set); the sweep changed more than it said", n)
	}
	// The same refusal reaches the reconciler's deal duty (over the roster:
	// the consumers SET): its pass line and the DUTY line's err, where the
	// wrapper used to return clean counts.
	c.SAdd(ctx, "consumers", "bench:b")
	st, err := store.Open(ctx, addr)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	var dutyOut, errb, report bytes.Buffer
	d := &cardDealDuty{st: st, out: &dutyOut}
	named := &namedDuties{errOut: &errb}
	duties := named.wrap([]reconcile.Duty{d.Run}, []string{"card-deal"})
	if _, err := duties[0](ctx, nil); err == nil || !strings.Contains(err.Error(), "EXPIRE sx0~1 REFUSED why=NOCOPY task:sx0~1") {
		t.Fatalf("deal duty err = %v; want the refused expire in it", err)
	}
	if !strings.Contains(dutyOut.String(), "DEAL-DUTY EXPIRE sx0~1 REFUSED why=NOCOPY task:sx0~1\n") {
		t.Fatalf("deal duty stdout %q lacks the pass line", dutyOut.String())
	}
	named.report(&report, true)
	if !regexp.MustCompile(`^DUTY card-deal dealt=0 routed=0 expired=0 .* err=deal pass: 1 refused: EXPIRE sx0~1 REFUSED why=NOCOPY task:sx0~1\n$`).MatchString(report.String()) {
		t.Fatalf("DUTY line %q does not carry the refusal", report.String())
	}
	if !strings.Contains(errb.String(), "nova-sprint reconcile: duty card-deal: deal pass: 1 refused") {
		t.Fatalf("stderr %q lacks the duty's error", errb.String())
	}
}

// TestDealDutySaysWhichConsumerItSkipped: a consumer on the roster whose
// slots field is not a number was skipped every pass with no line. It is a
// DEAL <c> SKIP line on stdout and the duty's error.
func TestDealDutySaysWhichConsumerItSkipped(t *testing.T) {
	t.Parallel()
	addr, c := silentRedis(t)
	ctx := context.Background()
	c.SAdd(ctx, "consumers", "bench:odd")
	c.HSet(ctx, "bench:odd:desired", "slots", "two")
	st, err := store.Open(ctx, addr)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	var out bytes.Buffer
	d := &cardDealDuty{st: st, out: &out}
	_, err = d.Run(ctx, nil)
	want := `DEAL bench:odd SKIP why=slots "two" on bench:odd:desired is not a number`
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("Run err = %v; want %q in it", err, want)
	}
	if !strings.Contains(out.String(), "DEAL-DUTY "+want+"\n") {
		t.Fatalf("stdout %q lacks the SKIP line", out.String())
	}
}

// TestCapacityRefusesWhenTheDesiredHashCannotBeRead: with no --machine the
// verb reads the consumer's desired hash; a store that does not answer used
// to read as "machine is required; pass --machine", a usage error. It is the
// dial error.
func TestCapacityRefusesWhenTheDesiredHashCannotBeRead(t *testing.T) {
	t.Parallel()
	code, out, errOut := runCLI("capacity", "bench", "--redis", silentClosedAddr(t), "--as", "b1", "--slots", "2")
	if code != 2 || out != "" || strings.Contains(errOut, "machine is required") || !strings.Contains(errOut, "read bench:b1:desired machine:") {
		t.Fatalf("capacity bench on a dead store = %d %q %q; want the read error, never 'machine is required'", code, out, errOut)
	}
}

// failingCloser is a forge whose close is refused.
type failingCloser struct{}

func (failingCloser) CloseIssue(context.Context, string, int, string) error {
	return errors.New("gh api PATCH: HTTP 403: rate limit")
}

// TestReviewDropSaysWhyTheIssueStayedOpen: a dropped card whose origin issue
// the forge would not close printed `closed=no` and nothing else. The why is
// on the line, exit 1 as before; a card with no issue is `issue=- closed=no`
// and exit 0, as before.
func TestReviewDropSaysWhyTheIssueStayedOpen(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	suffix, code := reviewClose(ctx, failingCloser{}, true, "mas-bandwidth/nova-tools", 4000, "REVIEW verdict=drop")
	if code != 1 || suffix != ` issue=mas-bandwidth/nova-tools#4000 closed=no err="gh api PATCH: HTTP 403: rate limit"` {
		t.Fatalf("a refused close = %d %q; want closed=no with the why", code, suffix)
	}
	if suffix, code := reviewClose(ctx, failingCloser{}, false, "", 0, ""); code != 0 || suffix != " issue=- closed=no" {
		t.Fatalf("no issue = %d %q", code, suffix)
	}
	if suffix, code := reviewClose(ctx, &closedIssues{}, true, "mas-bandwidth/nova-tools", 4000, "x"); code != 0 || suffix != " issue=mas-bandwidth/nova-tools#4000 closed=yes" {
		t.Fatalf("a close = %d %q", code, suffix)
	}
}
