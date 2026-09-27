//go:build functional

package pitstop_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/pitstop"
)

// TestQueueHeldOpenMatchesHeldOpen (Stella's read of nova-tools #4449,
// 2026-09-27): ns_pitstop_open, the one-call read the reconciler pass
// queues beside its lease renewal, answers exactly what HeldOpen's
// pipeline answers, over every shape of stop: none, a scope=all stop, a
// scope=streams stop, the 09-23 string at s:<S>:pitstop (a whole stop by
// wrongtype), the legacy sprint:<S>:pitstop (pitstop.LegacyKey; the port
// read s:<S>:pitstop:legacy and lost a legacy-only hold), and a closed
// sprint's stop, which holds nothing.
func TestQueueHeldOpenMatchesHeldOpen(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	c := fnRedis(t)
	for _, s := range []string{"all", "streams", "str", "legacy", "closed", "none"} {
		c.SAdd(ctx, "sprints", s)
		c.HSet(ctx, "s:"+s, "status", "open")
	}
	if _, err := pitstop.Set(ctx, c, "all", "glenn", "rest", false, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := pitstop.Set(ctx, c, "streams", "glenn", "redis only", false, "", "redis: store + bus"); err != nil {
		t.Fatal(err)
	}
	if _, err := pitstop.Set(ctx, c, "closed", "glenn", "old", false, ""); err != nil {
		t.Fatal(err)
	}
	c.HSet(ctx, "s:closed", "status", "closed")
	c.Set(ctx, pitstop.Key("str"), "stopped 09-23", 0)
	c.Set(ctx, pitstop.LegacyKey("legacy"), "1", 0)

	want, err := pitstop.HeldOpen(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	pipe := c.Pipeline()
	q := pitstop.QueueHeldOpen(ctx, pipe)
	if _, err := pipe.Exec(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := q.Result()
	if err != nil {
		t.Fatal(err)
	}
	shape := func(hs pitstop.Holds) string {
		out := ""
		for _, h := range hs {
			out += fmt.Sprintf("%s set=%v by=%s whole=%v scope=%s why=%s\n", h.Sprint, h.Stop.Set, h.Stop.By, h.Whole(), h.Scope(), h.Why())
		}
		return out
	}
	if shape(got) != shape(want) {
		t.Fatalf("ns_pitstop_open differs from HeldOpen\nwant:\n%s\ngot:\n%s", shape(want), shape(got))
	}
	// The legacy-only sprint is held, by legacy, as a whole stop.
	h, ok := got.Whole()
	if !ok || h.Sprint != "all" {
		t.Fatalf("whole = %+v %v", h, ok)
	}
	found := false
	for _, h := range got {
		if h.Sprint == "legacy" {
			found = h.Stop.Set && h.Stop.By == "legacy" && h.Whole()
		}
	}
	if !found {
		t.Fatalf("the legacy-only hold is missing or not whole:\n%s", shape(got))
	}
}
