package disposition_test

import (
	"context"
	"strings"
	"testing"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/disposition"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/land"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/life"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
)

// TestControl39 (#3092 rev 7) on the Functions harness: prose, a bold
// "**stella HOLD 4:** fixed", a lowercase, fenced, quoted or plural typed
// line make no record (NORECORD prose); a HOLD whose reason's only sentence
// names another pull makes a note kind=order and no hold.
func TestControl39(t *testing.T) {
	ctx := context.Background()
	c := redis.NewClient(&redis.Options{Addr: testutil.Start(t)})
	t.Cleanup(func() { _ = c.Close() })
	if err := fn.Load(ctx, c); err != nil {
		t.Fatal(err)
	}
	const S = "s39"
	head := strings.Repeat("a", 40)
	c.HSet(ctx, "machine:ctl:ceiling", "slots", 64)
	for _, f := range []string{"stella", "johnny", "rowan"} {
		if _, err := life.Hello(ctx, store.New(c), life.HelloRequest{As: f, Actor: f, Slots: -1, Host: "ctl", Session: "ctl-" + f}); err != nil {
			t.Fatal(err)
		}
	}
	c.HSet(ctx, "s:"+S+":policy", "fix_to", "rowan", "release_reader", "stella")
	if _, err := land.CallUnitHead(ctx, c, land.UnitHeadParams{Sprint: S, Unit: "u-39", Repo: "nova-tools", Base: "dev",
		Branch: "b-39", Head: head, BaseSHA: head, PR: "39", Author: "johnny"}); err != nil {
		t.Fatal(err)
	}
	ingest := func(body string) disposition.IngestResult {
		t.Helper()
		r, err := disposition.Ingest(ctx, c, disposition.IngestRequest{Sprint: S, Repo: "mas-bandwidth/nova-tools", PR: 39,
			URL: "mas-bandwidth/nova-tools/pull/39#issuecomment-1", Body: body, Actor: "rowan"})
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	line := "DISPOSITION who=stella head=" + head + " verdict=HOLD score=4: the check is missing at cmd/x/y.go:4\n"
	for name, b := range map[string]string{
		"prose":     "I would HOLD this: the check is missing at cmd/x/y.go:4\n",
		"bold":      "**stella HOLD 4:** fixed\n",
		"lowercase": strings.ToLower(line),
		"fenced":    "```\n" + line + "```\n",
		"quoted":    "> " + line,
		"plural":    strings.Replace(line, "DISPOSITION ", "DISPOSITIONS ", 1),
	} {
		if r := ingest(b); r.String() != "NORECORD prose" || r.Exit() != 0 {
			t.Fatalf("%s: %q exit %d, want NORECORD prose", name, r.String(), r.Exit())
		}
	}
	if n := c.Exists(ctx, "s:"+S+":hold:u-39:stella", disposition.EventsKey(S)).Val(); n != 0 {
		t.Fatalf("prose made %d records", n)
	}
	r := ingest("DISPOSITION who=stella head=" + head + " verdict=HOLD score=5: this waits on #3091 landing first\n")
	if r.Outcome != disposition.Record || len(r.Reply) < 5 || r.Reply[0] != "note" || r.Reply[4] != "order" {
		t.Fatalf("order HOLD: %q", r.String())
	}
	if c.Exists(ctx, "s:"+S+":hold:u-39:stella").Val() != 0 || c.HGet(ctx, "s:"+S+":u:u-39", "holds_open").Val() != "" {
		t.Fatalf("an order HOLD made a hold")
	}
}
