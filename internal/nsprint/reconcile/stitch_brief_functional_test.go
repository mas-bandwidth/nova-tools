//go:build functional

package reconcile_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/reconcile"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws/wstest"
)

// TestReleasedStitchGetsItsBrief is nova-tools#4317: when the waiting
// resolver releases a plan's stitch (every child landed), it rewrites the
// stitch's body with the generated brief, so the stitch is dealt with every
// child's PR, RESULT.md summary and read score; a released card that is not
// a stitch is untouched, and the parent (waiting on the stitch) stays.
func TestReleasedStitchGetsItsBrief(t *testing.T) {
	t.Parallel()
	_, c := wstest.Start(t)
	ctx := context.Background()
	wrTask(t, c, "P", "waiting", 1000, "where", "waiting", "kind", "plan", "children", "A B", "stitch", "P-stitch", "blocked_on", "P-stitch",
		"title", "the plan", "done_when", "the plan holds")
	wrTask(t, c, "A", "landed", 1001, "where", "landed", "parent", "P", "phase", "child", "repo", "mas-bandwidth/nova-tools", "pr", "71", "score", "9", "line2", "DONE", "title", "child a")
	wrTask(t, c, "B", "landed", 1002, "where", "landed", "parent", "P", "phase", "child", "repo", "mas-bandwidth/nova-tools", "pr", "72", "score", "10", "line2", "DONE", "finding", "one helper twice")
	wrTask(t, c, "P-stitch", "waiting", 1003, "where", "waiting", "parent", "P", "phase", "stitch", "blocked_on", "A,B",
		"body", "STREAM: s\n\nStitch of plan P.\n\n"+taskcard.BriefMarker+" (0)\n\n- (no children)\n")
	wrTask(t, c, "C", "waiting", 1004, "blocked_on", "A", "body", "plain card")

	st := store.New(c)
	lease, err := reconcile.Acquire(ctx, st, reconcile.AcquireOptions{Host: "test"})
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	duty := &reconcile.WaitingResolve{Client: c, Out: &out}
	counts, err := duty.Run(ctx, lease)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if counts.Routed != 2 {
		t.Fatalf("routed %d, want 2 (the stitch and C):\n%s", counts.Routed, out.String())
	}
	body := c.HGet(ctx, "task:P-stitch", "body").Val()
	for _, want := range []string{
		"STREAM: s\n\nStitch of plan P.\n\n" + taskcard.BriefMarker,
		"Plan P, state ready. DONE-WHEN (the stitch's): the plan holds",
		"- A landed pr=nova-tools#71 head=- score=9/10\n  title: child a\n  result: DONE\n",
		"- B landed pr=nova-tools#72 head=- score=10/10\n  result: DONE\n  finding: one helper twice\n",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("stitch body lacks %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "(no children)") || strings.Count(body, taskcard.BriefMarker) != 1 {
		t.Errorf("the generated section is replaced, once:\n%s", body)
	}
	if got := c.HGet(ctx, "task:C", "body").Val(); got != "plain card" {
		t.Errorf("a plain released card's body changed to %q", got)
	}
	if w := c.HGet(ctx, "task:P", "where").Val(); w != "waiting" {
		t.Errorf("the parent is %s, want waiting on its stitch", w)
	}
	if w := c.HGet(ctx, "task:P-stitch", "where").Val(); w != "ready" {
		t.Errorf("the stitch is %s, want ready", w)
	}
}
