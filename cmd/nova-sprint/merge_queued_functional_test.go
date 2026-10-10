//go:build functional

package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// recordMerge is the merge step a landing records once its push is on the base, run as land
// runs it (the step, at the epoch given), not through the verb: on a real store the verb
// refuses a bare merge, and these cards have no git to name by --landed.
func recordMerge(t *testing.T, a *app, stream string, batch int, epoch string) string {
	t.Helper()
	fs, c := a.verbSetup("merge")
	_, err := parse(fs, []string{"--epoch", epoch})
	require.NoError(t, err)
	st, err := a.store(*c)
	require.NoError(t, err)
	var out, errb bytes.Buffer
	code := a.runStep("merge", *c, st, store.MergeStep(sprint.MergeReq{Stream: stream, Batch: batch, Who: c.actor}), &out, &errb)
	require.Zero(t, code, "the merge step: exit %d\n%s%s", code, out.String(), errb.String())
	return out.String()
}

// On a real store, with the machine running: the coordinator's accept queues
// the work table's review -> merging for the next pump and writes the merge
// card at once; a merge in the same tick, before the pump, judges the work
// table through the queued view (as every step but the pump plans), so it
// lands the cards, and check is clean before and after the pump, which
// lands them on the work table, archives the landed stream and finds the
// sprint done.
func TestAMergeBeforeThePumpOnTheStore(t *testing.T) {
	t.Parallel()
	addr := testutil.Start(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	defer c.Close()
	err := fn.Load(context.Background(), c)
	require.NoError(t, err)
	env := map[string]string{"NOVA_SPRINT_REDIS": addr, "NOVA_SPRINT_ACTOR": "coordinator"}
	a := newApp(func(k string) string { return env[k] })
	defer a.close()
	run := func(line string) string {
		t.Helper()
		var out, errb bytes.Buffer
		code := a.run(strings.Fields(line), &out, &errb)
		require.Zero(t, code, "%s: exit %d\n%s%s", line, code, out.String(), errb.String())
		return out.String()
	}
	for _, l := range []string{"init --readers reader-a,reader-b --members m1", "add --stream s1 --count 2", "fleet beat m1", "start",
		"tick", "tick", // the fleet update brings m1 up; the next pump deals
		"take --as m1 --limit 5 --epoch 0", "finish --as m1 s1-1.w1@1 s1-2.w1@1 --epoch 0",
		"queue --as reader-a", "queue --as reader-b", // the readers ask for their queues: they are up
		"tick", // the pump moves both to review; the readers are asked
		"read --as reader-a --begin --limit 10 --epoch 0", "read --as reader-a --ok --limit 10 --epoch 0",
		"read --as reader-b --begin --limit 10 --epoch 0", "read --as reader-b --ok --limit 10 --epoch 0"} {
		run(l)
	}
	// before the next pump (which would accept them itself), the coordinator
	// accepts: its work change is queued, its merge cards written
	out := run("accept s1-1 s1-2")
	require.Contains(t, out, "ACCEPT OK moved=2", "accept on a running machine:\n%s", out)
	out = run("check")
	require.Contains(t, out, "CHECK OK violations=0", "check after the accept, before the pump:\n%s", out)
	// a bare merge on the store is refused and writes nothing: check stays clean and the
	// landing below still moves both cards
	var bare, bareErr bytes.Buffer
	require.NotZero(t, a.run(strings.Fields("merge --stream s1 --batch 10 --epoch 0"), &bare, &bareErr), "a bare merge on a real store:\n%s%s", bare.String(), bareErr.String())
	require.Contains(t, bareErr.String(), "a bare merge records the queue's head as landed with no push, and a real store refuses it; nothing was changed")
	out = run("check")
	require.Contains(t, out, "CHECK OK violations=0", "check after the refused bare merge:\n%s", out)
	out = recordMerge(t, a, "s1", 10, "0")
	require.Contains(t, out, "MERGE OK", "merge before the pump:\n%s", out)
	require.NotContains(t, out, "REFUSED", "merge before the pump:\n%s", out)
	out = run("check")
	require.Contains(t, out, "CHECK OK violations=0", "check after the merge, before the pump:\n%s", out)
	run("tick")
	out = run("where")
	require.Contains(t, out, "DONE", "after the pump:\n%s", out)
	// the tick archives a stream when its last card lands, and the headline counts
	// only the streams on the table (stream archive; the owner, 2026-10-06): the two
	// landed cards are on the archived line, not in the work rows
	require.Contains(t, out, "1 archived stream, 2 cards landed", "after the pump:\n%s", out)
	out = run("check")
	require.Contains(t, out, "CHECK OK violations=0", "check after the pump:\n%s", out)
}
