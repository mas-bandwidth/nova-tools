//go:build functional

package taskcard_test

import (
	"context"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws/wstest"
)

// TestStitchBriefCarriesEveryChildsResult: the generated brief (ns_stitch_brief,
// the store's one implementation since nova-tools #4449) names each child's
// PR, head, read score, RESULT.md line 2 and finding, in the plan's order,
// and the plan's DONE-WHEN as the stitch's. The records are written as the
// verbs leave them; the render reads them and writes nothing.
func TestStitchBriefCarriesEveryChildsResult(t *testing.T) {
	t.Parallel()
	_, c := wstest.Start(t)
	ctx := context.Background()
	const parent = "nova-tools-4317"
	children := []string{"nova-tools-5001", "nova-tools-5002", "nova-tools-5003"}
	stitch := pushPlanCards(t, c, parent, children...)
	if _, err := taskcard.BindPlan(ctx, c, parent, children, stitch, "rowan"); err != nil {
		t.Fatal(err)
	}
	c.HSet(ctx, taskcard.Key(parent), "done_when", "the plan's DONE-WHEN holds at the stitch's head")
	c.HSet(ctx, taskcard.Key("nova-tools-5001"), "title", "child one", "where", "landed", "pr", "6001",
		"head", "abcdef0123456789abcdef0123456789abcdef01", "score", "9", "line2", "DONE", "finding", "one dup helper\nleft in pass.go", "paths", "a.go b.go")
	c.HSet(ctx, taskcard.Key("nova-tools-5002"), "title", "child two", "where", "review", "ref", "mas-bandwidth/nova-tools#5002",
		"line2", "BLOCKED the fixture is red on dev", "where_ok", "fail")
	c.HDel(ctx, taskcard.Key("nova-tools-5003"), "ref")

	b, err := taskcard.RenderStitchBrief(ctx, c, parent)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(b, taskcard.BriefMarker+" ") {
		t.Fatalf("brief starts %q, want the marker %q", b[:20], taskcard.BriefMarker)
	}
	for _, want := range []string{
		"Plan nova-tools-4317 (mas-bandwidth/nova-tools#4317), state working. DONE-WHEN (the stitch's): the plan's DONE-WHEN holds at the stitch's head",
		"- nova-tools-5001 landed pr=nova-tools#6001 head=abcdef012345 score=9/10\n  title: child one\n  result: DONE\n  finding: one dup helper left in pass.go\n  paths: a.go b.go\n",
		"- nova-tools-5002 review pr=mas-bandwidth/nova-tools#5002 head=- score=- outcome=fail\n  title: child two\n  result: BLOCKED the fixture is red on dev\n",
		"- nova-tools-5003 waiting pr=- head=- score=-\n",
	} {
		if !strings.Contains(b, want) {
			t.Errorf("brief lacks %q:\n%s", want, b)
		}
	}
	// The order is the plan's.
	if strings.Index(b, "nova-tools-5001") > strings.Index(b, "nova-tools-5002") || strings.Index(b, "nova-tools-5002") > strings.Index(b, "nova-tools-5003") {
		t.Errorf("children out of order:\n%s", b)
	}
	// The stitch's id names the same plan.
	if bs, err := taskcard.RenderStitchBrief(ctx, c, stitch); err != nil || bs != b {
		t.Fatalf("render by the stitch's id: err=%v same=%v", err, bs == b)
	}
	// An empty plan's brief says so.
	c.HSet(ctx, taskcard.Key("p"), "kind", taskcard.KindPlan, "where", "waiting")
	if got, err := taskcard.RenderStitchBrief(ctx, c, "p"); err != nil || !strings.Contains(got, "- (no children)") {
		t.Errorf("an empty plan's brief says so; err=%v got:\n%s", err, got)
	}
	// Neither a plan nor a stitch is refused with the remedy.
	if _, err := taskcard.RenderStitchBrief(ctx, c, "nova-tools-5001"); err == nil || !strings.Contains(err.Error(), "is not a stitch or a plan") {
		t.Errorf("a child renders no brief; err=%v", err)
	}
}

// TestWithBriefReplacesTheGeneratedSectionOnly: the stitch's own text
// before the marker is kept verbatim across rewrites; the section after it
// is replaced, not appended; a body that is empty or only the section is
// the brief alone. The write goes through the one task writer.
func TestWithBriefReplacesTheGeneratedSectionOnly(t *testing.T) {
	t.Parallel()
	_, c := wstest.Start(t)
	ctx := context.Background()
	const parent = "p-4317"
	stitch := pushPlanCards(t, c, parent, "c1", "c2")
	head := "STREAM: autonomy\n\nStitch of plan " + parent + "."
	if _, err := taskcard.BindPlan(ctx, c, parent, []string{"c1", "c2"}, stitch, "rowan"); err != nil {
		t.Fatal(err)
	}
	first, _ := c.HGet(ctx, taskcard.Key(stitch), "body").Result()
	c.HSet(ctx, taskcard.Key("c2"), "pr", "1", "repo", "mas-bandwidth/x")
	brief, err := taskcard.WriteStitchBrief(ctx, c, stitch)
	if err != nil {
		t.Fatal(err)
	}
	second, _ := c.HGet(ctx, taskcard.Key(stitch), "body").Result()
	if !strings.HasPrefix(first, head+"\n\n"+taskcard.BriefMarker) || !strings.HasPrefix(second, head+"\n\n"+taskcard.BriefMarker) {
		t.Fatalf("the head is not kept verbatim:\n%s\n---\n%s", first, second)
	}
	if strings.Count(second, taskcard.BriefMarker) != 1 || strings.Contains(second, "(no children)") || !strings.Contains(second, "pr=x#1") || !strings.HasSuffix(second, strings.TrimRight(brief, "\n")+"\n") {
		t.Fatalf("the generated section is replaced, once, by the brief returned:\n%s", second)
	}
	// Written through the one writer's same-place path (TK.move: only the
	// fields change; the pointer, state and why stay the push's), so the
	// stitch is still where it was, with its why unchanged.
	if where, _ := c.HGet(ctx, taskcard.Key(stitch), "where").Result(); where != "waiting" {
		t.Errorf("where=%q after the brief, want waiting", where)
	}
	if why, _ := c.HGet(ctx, taskcard.Key(stitch), "why").Result(); why != "push" {
		t.Errorf("why=%q after the brief; the same-place write changes only the fields (want push)", why)
	}
	c.HSet(ctx, taskcard.Key(stitch), "body", "")
	if _, err := taskcard.WriteStitchBrief(ctx, c, parent); err != nil {
		t.Fatal(err)
	}
	if got, _ := c.HGet(ctx, taskcard.Key(stitch), "body").Result(); got != strings.TrimRight(brief, "\n")+"\n" && !strings.HasPrefix(got, taskcard.BriefMarker) {
		t.Errorf("an empty body is the brief alone; got %q", got)
	}
	c.HSet(ctx, taskcard.Key(stitch), "body", "## Children (0)\n- old")
	if _, err := taskcard.WriteStitchBrief(ctx, c, stitch); err != nil {
		t.Fatal(err)
	}
	if got, _ := c.HGet(ctx, taskcard.Key(stitch), "body").Result(); !strings.HasPrefix(got, taskcard.BriefMarker) || strings.Contains(got, "- old") {
		t.Errorf("a body that is only the section is replaced; got %q", got)
	}
}
