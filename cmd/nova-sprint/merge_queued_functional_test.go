//go:build functional

package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/redis/go-redis/v9"
)

// On a real store, with the machine running: the coordinator's accept queues
// the work table's review -> merging for the next pump and writes the merge
// card at once; a merge in the same tick, before the pump, judges the work
// table through the queued view (as every step but the pump plans), so it
// lands the cards, and check is clean before and after the pump, which
// lands them on the work table and finds the sprint done.
func TestAMergeBeforeThePumpOnTheStore(t *testing.T) {
	t.Parallel()
	addr := testutil.Start(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	defer c.Close()
	if err := fn.Load(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"NOVA_SPRINT_REDIS": addr, "NOVA_SPRINT_ACTOR": "coordinator"}
	a := newApp(func(k string) string { return env[k] })
	defer a.close()
	run := func(line string) string {
		t.Helper()
		var out, errb bytes.Buffer
		if code := a.run(strings.Fields(line), &out, &errb); code != 0 {
			t.Fatalf("%s: exit %d\n%s%s", line, code, out.String(), errb.String())
		}
		return out.String()
	}
	for _, l := range []string{"init --readers reader-a,reader-b --members m1", "add --stream s1 --count 2", "fleet beat m1", "start",
		"tick", "tick", // the fleet update brings m1 up; the next pump deals
		"take --as m1 --limit 5 --epoch 0", "finish --as m1 s1-1.w1@1 s1-2.w1@1 --epoch 0",
		"tick", // the pump moves both to review; the readers are asked
		"read --as reader-a --begin --limit 10 --epoch 0", "read --as reader-a --ok --limit 10 --epoch 0",
		"read --as reader-b --begin --limit 10 --epoch 0", "read --as reader-b --ok --limit 10 --epoch 0"} {
		run(l)
	}
	// before the next pump (which would accept them itself), the coordinator
	// accepts: its work change is queued, its merge cards written
	if out := run("accept s1-1 s1-2"); !strings.Contains(out, "ACCEPT OK moved=2") {
		t.Fatalf("accept on a running machine:\n%s", out)
	}
	if out := run("check"); !strings.Contains(out, "CHECK OK violations=0") {
		t.Fatalf("check after the accept, before the pump:\n%s", out)
	}
	if out := run("merge --stream s1 --batch 10 --epoch 0"); !strings.Contains(out, "MERGE OK") || strings.Contains(out, "REFUSED") {
		t.Fatalf("merge before the pump:\n%s", out)
	}
	if out := run("check"); !strings.Contains(out, "CHECK OK violations=0") {
		t.Fatalf("check after the merge, before the pump:\n%s", out)
	}
	run("tick")
	if out := run("where"); !strings.Contains(out, "DONE") || !strings.Contains(out, "|      2") {
		t.Fatalf("after the pump:\n%s", out)
	}
	if out := run("check"); !strings.Contains(out, "CHECK OK violations=0") {
		t.Fatalf("check after the pump:\n%s", out)
	}
}
