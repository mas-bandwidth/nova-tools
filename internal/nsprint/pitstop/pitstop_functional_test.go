//go:build functional

package pitstop_test

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/pitstop"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/redis/go-redis/v9"
)

func fnRedis(t *testing.T) *redis.Client {
	t.Helper()
	c := redis.NewClient(&redis.Options{Addr: testutil.Start(t)})
	t.Cleanup(func() { _ = c.Close() })
	if err := fn.Load(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	return c
}

func receipts(t *testing.T, c *redis.Client, sprint string) []map[string]any {
	t.Helper()
	msgs, err := c.XRange(context.Background(), pitstop.LogKey(sprint), "-", "+").Result()
	if err != nil {
		t.Fatal(err)
	}
	var out []map[string]any
	for _, m := range msgs {
		out = append(out, m.Values)
	}
	return out
}

// TestSetClearEveryBranch runs ns_pitstop_set and ns_pitstop_clear on a
// throwaway redis-server (miniredis has no FCALL): unknown sprint, first set,
// set refused over an existing stop, forced replace, clear, clear with none,
// and a missing by. Each write leaves exactly one receipt; each refusal none.
func TestSetClearEveryBranch(t *testing.T) {
	t.Parallel()

	const S = "control-00003371"
	ctx := context.Background()
	c := fnRedis(t)

	r, err := pitstop.Set(ctx, c, S, "rowan", "why", false, "")
	if err != nil || r.Outcome != pitstop.Unknown {
		t.Fatalf("unknown sprint: %+v %v", r, err)
	}
	if c.Exists(ctx, pitstop.Key(S)).Val() != 0 || len(receipts(t, c, S)) != 0 {
		t.Fatal("unknown sprint wrote")
	}
	c.HSet(ctx, "s:"+S, "status", "open")

	r, err = pitstop.Set(ctx, c, S, "rowan", "Glenn 8:00 PM: stop", false, "i1")
	if err != nil || r.Outcome != pitstop.Done || r.At == 0 || r.Prior.Set {
		t.Fatalf("first set: %+v %v", r, err)
	}
	stop, _ := pitstop.Read(ctx, c, S)
	if !stop.Set || stop.By != "rowan" || stop.Why != "Glenn 8:00 PM: stop" || stop.At != r.At {
		t.Fatalf("stored stop %+v, want by rowan at %d", stop, r.At)
	}
	rs := receipts(t, c, S)
	if len(rs) != 1 || rs[0]["kind"] != "pitstop set" || rs[0]["actor"] != "rowan" || rs[0]["from"] != "none" ||
		rs[0]["to"] != "set" || rs[0]["idem"] != "i1" || rs[0]["reason"] != "Glenn 8:00 PM: stop" {
		t.Fatalf("set receipts %v", rs)
	}

	r, err = pitstop.Set(ctx, c, S, "stella", "other", false, "")
	if err != nil || r.Outcome != pitstop.Exists || r.Prior.By != "rowan" || r.Prior.At != stop.At {
		t.Fatalf("set over a stop: %+v %v", r, err)
	}
	if again, _ := pitstop.Read(ctx, c, S); !reflect.DeepEqual(again, stop) || len(receipts(t, c, S)) != 1 {
		t.Fatalf("refused set wrote: %+v", again)
	}

	r, err = pitstop.Set(ctx, c, S, "stella", "other", true, "")
	if err != nil || r.Outcome != pitstop.Done || r.Prior.By != "rowan" || r.Prior.Why != "Glenn 8:00 PM: stop" {
		t.Fatalf("forced set: %+v %v", r, err)
	}
	if now, _ := pitstop.Read(ctx, c, S); now.By != "stella" || now.Why != "other" {
		t.Fatalf("forced set stored %+v", now)
	}
	rs = receipts(t, c, S)
	if len(rs) != 2 || rs[1]["from"] != "set" || !strings.Contains(rs[1]["evidence"].(string), "replaced by=rowan") {
		t.Fatalf("forced receipts %v", rs)
	}

	r, err = pitstop.Clear(ctx, c, S, "rowan", "")
	if err != nil || r.Outcome != pitstop.Done || r.Prior.By != "stella" || r.Prior.Why != "other" || r.At == 0 {
		t.Fatalf("clear: %+v %v", r, err)
	}
	if c.Exists(ctx, pitstop.Key(S)).Val() != 0 {
		t.Fatal("clear left the key")
	}
	rs = receipts(t, c, S)
	if len(rs) != 3 || rs[2]["kind"] != "pitstop clear" || rs[2]["to"] != "none" || rs[2]["actor"] != "rowan" {
		t.Fatalf("clear receipts %v", rs)
	}

	r, err = pitstop.Clear(ctx, c, S, "rowan", "")
	if err != nil || r.Outcome != pitstop.None || len(receipts(t, c, S)) != 3 {
		t.Fatalf("clear with none: %+v %v", r, err)
	}

	if _, err := pitstop.Set(ctx, c, S, "", "x", false, ""); err == nil {
		t.Fatal("set with no by: want an error")
	}
	if _, err := pitstop.Clear(ctx, c, S, "", ""); err == nil {
		t.Fatal("clear with no by: want an error")
	}
}
