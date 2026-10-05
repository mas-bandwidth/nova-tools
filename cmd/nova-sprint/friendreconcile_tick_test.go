package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// The run loop reconciles every friend each tick (docs/SPEC-SPRINT.md section 1,
// friend-reconcile-every-tick-r.w1): on the in-memory store with the sprint's clock, her
// directory under t.TempDir(), no socket and no wall clock. A row working=4 with an empty
// QUEUE.json is returned by the next tick, one history line a card; a reported card is
// collected; a directory the server cannot reach is one record line; a disagreement that
// lasts is one note.

// tickReconcileApp is reconcileApp with the server's HOME at home: run reads her directory
// there.
func tickReconcileApp(t *testing.T, n int) (*testApp, string) {
	t.Helper()
	ta, root := reconcileApp(t, n)
	homeIs(ta, root)
	return ta, root
}

// homeIs sets the app's HOME.
func homeIs(ta *testApp, home string) {
	prev := ta.a.getenv
	ta.a.getenv = func(k string) string {
		if k == "HOME" {
			return home
		}
		return prev(k)
	}
}

// runTicks runs the loop n ticks and returns what it printed; it fails on anything on stderr.
func runTicks(t *testing.T, ta *testApp, n int) string {
	t.Helper()
	st, _, code := ta.a.machineVerb("run", nil, &bytes.Buffer{})
	require.NotNil(t, st, "run: %d", code)
	var out, errb bytes.Buffer
	ta.a.runLoop(context.Background(), st, 50, n, &out, &errb)
	require.Empty(t, errb.String(), "run's stderr:\n%s", out.String())
	return out.String()
}

func TestRunReconcilesFriendsEveryTick(t *testing.T) {
	t.Parallel()
	primary := func(ta *testApp, id string) cardView {
		var c cardView
		ta.json("card "+id, &c)
		return c
	}

	t.Run("a phantom working count is returned by the next tick, one history line a card", func(t *testing.T) {
		t.Parallel()
		ta, root := tickReconcileApp(t, 4)
		writeTestQueueFile(t, ta, root, "amy", `{"tasks":[]}`)
		out := runTicks(t, ta, 1)
		for _, id := range []string{"s1-1", "s1-2", "s1-3", "s1-4"} {
			assert.Contains(t, out, "FRIEND-RECONCILE RETURNED friend=amy card="+id+".w1: her QUEUE.json, written at", id)
		}
		assert.Contains(t, ta.ok("inbox"), "a friend's card returned to ready", "each return is pushed to the coordinator")
		out += runTicks(t, ta, 2)
		for _, id := range []string{"s1-1", "s1-2", "s1-3", "s1-4"} {
			assert.Equal(t, 1, strings.Count(out, "RETURNED friend=amy card="+id+".w1"), id)
			assert.Equal(t, 1, strings.Count(ta.ok("log --card "+id), "returned by friend reconcile"), "one history line for %s", id)
			assert.NotEqual(t, id+".w1", primary(ta, id).Primary.F("work"), "the work card she never ran is off the primary (the next pump moves it)")
		}
		assert.NotContains(t, out, "disagrees", "her row and her queue agree after the pass")
	})

	t.Run("a finished card is collected", func(t *testing.T) {
		t.Parallel()
		ta, root := tickReconcileApp(t, 2)
		outboxReport(t, root, "amy", "s1-1.w1", "Verdict: LAND\nHead: "+landHead+"\n\nPushed and green.\n")
		writeTestQueueFile(t, ta, root, "amy", `{"tasks":[{"id":"s1-1.w1","state":"done"},{"id":"s1-2.w1","state":"working"}]}`)
		out := runTicks(t, ta, 2)
		assert.Equal(t, 1, strings.Count(out, "FRIEND-CARD FINISHED friend=amy card=s1-1.w1 result=ok head="+landHead), out)
		assert.NotContains(t, out, "card=s1-2.w1", "the card she works is kept, and a keep is not said each tick")
		assert.Equal(t, sprint.Review, primary(ta, "s1-1").Primary.Col, "the collected card goes to review")
		assert.Equal(t, sprint.Working, primary(ta, "s1-2").Primary.Col, "the kept card is hers still")
		assert.Contains(t, ta.ok("inbox"), "s1-1.w1 collected from friend amy by the run loop's reconcile", "the collect is pushed to the coordinator")
	})

	t.Run("a directory the server cannot reach is one record line", func(t *testing.T) {
		t.Parallel()
		ta, _ := reconcileApp(t, 1)
		homeIs(ta, t.TempDir()) // no amy-working under it
		out := runTicks(t, ta, 3)
		assert.Equal(t, 1, strings.Count(out, "FRIEND-RECONCILE SKIPPED friend=amy: "), out)
		assert.Contains(t, out, "is not reachable from the server")
		assert.NotContains(t, out, "FRIEND-RECONCILE FAILED")
		assert.Equal(t, sprint.Working, primary(ta, "s1-1").Primary.Col, "nothing is moved for a friend not read")
	})

	t.Run("a lasting disagreement is one note", func(t *testing.T) {
		t.Parallel()
		ta, root := tickReconcileApp(t, 1)
		// she holds it queued: kept, while her row says working
		writeTestQueueFile(t, ta, root, "amy", `{"tasks":[{"id":"s1-1.w1","state":"queued"}]}`)
		out := runTicks(t, ta, 3)
		assert.Equal(t, 1, strings.Count(out, "friend amy disagrees with her QUEUE.json: her row working=1, her QUEUE.json working=0"), out)
		assert.Contains(t, ta.ok("inbox"), "a friend's row disagrees with her QUEUE.json")
		assert.NotContains(t, runTicks(t, ta, 2), "disagrees", "a loop started again finds the episode said on the fleet table")
		writeTestQueueFile(t, ta, root, "amy", `{"tasks":[{"id":"s1-1.w1","state":"working"}]}`)
		out = runTicks(t, ta, 2)
		assert.Equal(t, 1, strings.Count(out, "friend amy agrees with her QUEUE.json again"), out)
		assert.Equal(t, sprint.Working, primary(ta, "s1-1").Primary.Col, "a disagreement is said, never acted on")
	})
}
