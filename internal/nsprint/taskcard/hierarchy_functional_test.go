//go:build functional

package taskcard_test

import (
	"context"
	"sort"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws/wstest"
	"github.com/redis/go-redis/v9"
)

const hierStream = "autonomy"

// pushPlanCards pushes a parent (waiting, no dependency), its children and
// the stitch as card cut --parent does: children carry parent and phase,
// the stitch DEPENDS-ON every child; nothing binds the parent yet.
func pushPlanCards(t *testing.T, c *redis.Client, parent string, children ...string) string {
	t.Helper()
	ctx := context.Background()
	stitch := taskcard.StitchID(parent)
	reqs := []taskcard.PushRequest{{ID: parent, Where: "waiting", Stream: hierStream, Sprint: sprint, Kind: "build",
		Ref: "mas-bandwidth/nova-tools#4317", Title: "plan: work as a hierarchy", Repo: "mas-bandwidth/nova-tools", By: "rowan",
		Fields: []string{"base", "dev", "base_sha", baseSHA, "paths", "internal/x.go", "done_when", "the whole plan holds"}}}
	for i, ch := range children {
		reqs = append(reqs, taskcard.PushRequest{ID: ch, Where: "waiting", Stream: hierStream, Sprint: sprint, Kind: "build",
			Ref: "mas-bandwidth/nova-tools#" + ch, Title: "child " + ch, Repo: "mas-bandwidth/nova-tools", By: "rowan",
			Fields: []string{"base", "dev", "base_sha", baseSHA, "paths", "internal/c" + string(rune('a'+i)) + ".go",
				"done_when", "child " + ch + " holds", taskcard.FieldParent, parent, taskcard.FieldPhase, taskcard.PhaseChild}})
	}
	reqs = append(reqs, taskcard.PushRequest{ID: stitch, Where: "waiting", Stream: hierStream, Sprint: sprint, Kind: taskcard.KindStitch,
		Title: "stitch: plan", Repo: "mas-bandwidth/nova-tools", By: "rowan", DependsOn: strings.Join(children, ","),
		Fields: []string{"base", "dev", "base_sha", baseSHA, "paths", "internal/x.go", "done_when", "the whole plan holds",
			"body", "STREAM: autonomy\n\nStitch of plan " + parent + ".\n\n" + taskcard.BriefMarker + " (0)\n\n- (no children)\n",
			taskcard.FieldParent, parent, taskcard.FieldPhase, taskcard.PhaseStitch}})
	out, err := taskcard.PushMany(ctx, c, reqs)
	if err != nil {
		t.Fatal(err)
	}
	for i, o := range out {
		if o.Err != nil {
			t.Fatalf("push %s: %v", reqs[i].ID, o.Err)
		}
	}
	return stitch
}

// landCard walks a waiting card to landed the way the stream does: ready
// (by hand), working (a friend's take), merging (done with a PR), landed
// (the stream landing). It returns the landing reply.
func landCard(t *testing.T, c *redis.Client, id string, pr string, sha string) taskcard.StreamLanding {
	t.Helper()
	ctx := context.Background()
	if _, err := taskcard.Move(ctx, c, id, "ready", taskcard.Opts{By: "rowan", Why: "test"}); err != nil {
		t.Fatalf("%s -> ready: %v", id, err)
	}
	if _, err := taskcard.Take(ctx, c, "emma", 1, "emma", id); err != nil {
		t.Fatalf("take %s: %v", id, err)
	}
	if _, err := taskcard.Done(ctx, c, id, "emma", "DONE", pr); err != nil {
		t.Fatalf("done %s: %v", id, err)
	}
	c.HSet(ctx, taskcard.Key(id), "line2", "DONE", "score", "9", "head", sha)
	r, err := taskcard.LandStream(ctx, c, hierStream, sha, "rowan", "")
	if err != nil {
		t.Fatalf("land stream after %s: %v", id, err)
	}
	return r
}

func where(t *testing.T, c *redis.Client, id string) string {
	t.Helper()
	return c.HGet(context.Background(), taskcard.Key(id), "where").Val()
}

// TestPlanBindsLandsWithItsStitchAndIsNeverDealt is nova-tools#4317's
// DONE-WHEN on a real store: BindPlan makes the parent a plan that DEPENDS-ON
// its stitch (waiting, never dealt: card deal refuses it by name); the
// parent's state is derived as its children move; task land on the parent is
// refused by name until the stitch lands; the stream landing that lands the
// stitch lands the parent at the same sha and names it in the reply; the
// stitch's brief carries every child's PR and score; fsck is clean.
func TestPlanBindsLandsWithItsStitchAndIsNeverDealt(t *testing.T) {
	t.Parallel()
	_, c := wstest.Start(t)
	ctx := context.Background()
	parent := "plan-4317"
	stitch := pushPlanCards(t, c, parent, "5001", "5002")

	// Bind from ready too: the parent goes back to waiting on its stitch.
	if _, err := taskcard.Move(ctx, c, parent, "ready", taskcard.Opts{By: "rowan", Why: "test"}); err != nil {
		t.Fatal(err)
	}
	res, err := taskcard.BindPlan(ctx, c, parent, []string{"5001", "5002"}, stitch, "rowan")
	if err != nil || res.To != "waiting" {
		t.Fatalf("bind: %v %v", res, err)
	}
	rec := c.HGetAll(ctx, taskcard.Key(parent)).Val()
	for k, want := range map[string]string{"where": "waiting", "kind": "plan", "children": "5001 5002", "stitch": stitch, "blocked_on": stitch} {
		if rec[k] != want {
			t.Errorf("parent %s = %q, want %q", k, rec[k], want)
		}
	}
	if body := c.HGet(ctx, taskcard.Key(stitch), "body").Val(); !strings.Contains(body, "- 5001 waiting pr=mas-bandwidth/nova-tools#5001") || !strings.HasPrefix(body, "STREAM: autonomy") {
		t.Fatalf("the stitch's brief is written at bind, its head kept:\n%s", body)
	}

	// Never dealt: the deal names the plan and why.
	bench := enroll(t, c, "bench:hetzner", 4, "")
	if _, err := taskcard.Deal(ctx, c, taskcard.DealRequest{To: bench, IDs: []string{parent}, By: "rowan"}); err == nil || !strings.Contains(err.Error(), "PLAN task:plan-4317 is a plan") {
		t.Fatalf("deal of a plan: %v; want the PLAN refusal", err)
	}
	if got := where(t, c, parent); got != "waiting" {
		t.Fatalf("parent is %s after the refused deal, want waiting", got)
	}
	// A bound stitch without its children landed is a normal waiting card.
	if _, err := taskcard.Land(ctx, c, parent, "rowan", head(1), ""); err == nil || !strings.Contains(err.Error(), "PLAN task:plan-4317 lands when its stitch "+stitch+" lands (now waiting)") {
		t.Fatalf("land of the plan before its stitch: %v", err)
	}

	// The children land, one stream landing each; the plan's state follows.
	state := func() string {
		p, err := taskcard.ReadPlan(ctx, c, parent)
		if err != nil {
			t.Fatal(err)
		}
		return p.State()
	}
	if s := state(); s != "waiting" {
		t.Fatalf("state %s, want waiting", s)
	}
	if _, err := taskcard.Move(ctx, c, "5001", "ready", taskcard.Opts{By: "rowan", Why: "test"}); err != nil {
		t.Fatal(err)
	}
	if s := state(); s != "ready" {
		t.Fatalf("state %s with a child ready, want ready", s)
	}
	if _, err := taskcard.Take(ctx, c, "emma", 1, "emma", "5001"); err != nil {
		t.Fatal(err)
	}
	if s := state(); s != "working" {
		t.Fatalf("state %s with a child working, want working", s)
	}
	if _, err := taskcard.Done(ctx, c, "5001", "emma", "DONE", "6001"); err != nil {
		t.Fatal(err)
	}
	c.HSet(ctx, taskcard.Key("5001"), "line2", "DONE", "score", "9", "head", head(1), "finding", "one helper twice")
	if r, err := taskcard.LandStream(ctx, c, hierStream, head(1), "rowan", ""); err != nil || len(r.Landed) != 1 || r.Landed[0].ID != "5001" {
		t.Fatalf("first landing: %v %v", r, err)
	}
	landCard(t, c, "5002", "6002", head(2))
	if s := state(); s != "waiting" {
		t.Fatalf("state %s with both children landed and the stitch waiting, want waiting", s)
	}
	if got := where(t, c, parent); got != "waiting" {
		t.Fatalf("parent is %s, want waiting until the stitch lands", got)
	}

	// The stitch's brief, regenerated, carries both PRs and scores.
	p, brief, err := taskcard.WriteStitchBrief(ctx, c, stitch)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"- 5001 landed pr=nova-tools#6001 head=" + head(1)[:12] + " score=9/10", "finding: one helper twice", "- 5002 landed pr=nova-tools#6002"} {
		if !strings.Contains(brief, want) {
			t.Errorf("brief lacks %q:\n%s", want, brief)
		}
	}
	if len(p.Children) != 2 || p.Stitch.ID != stitch {
		t.Fatalf("plan read %+v", p)
	}

	// The stitch lands: the same landing lands the parent at the same sha
	// and names it, so the lander closes its issue.
	r := landCard(t, c, stitch, "6003", head(3))
	ids := []string{}
	for _, m := range r.Landed {
		ids = append(ids, m.ID)
	}
	if strings.Join(ids, ",") != stitch+","+parent || r.Landed[1].Ref != "mas-bandwidth/nova-tools#4317" || len(r.Refused) != 0 {
		t.Fatalf("landing reply %+v, want the stitch then the parent with its ref", r)
	}
	rec = c.HGetAll(ctx, taskcard.Key(parent)).Val()
	if rec["where"] != "landed" || rec["merge_sha"] != head(3) || !strings.Contains(rec["why"], "stitch "+stitch+" landed") {
		t.Fatalf("parent after the stitch landed: where=%s merge_sha=%s why=%q", rec["where"], rec["merge_sha"], rec["why"])
	}
	if s := state(); s != "landed" {
		t.Fatalf("state %s, want landed", s)
	}
	// Five landed: two children, the stitch, the parent, and the stream's
	// sentinel (#4318), which the parent's landing landed as the last live
	// card (the two hooks in the one move compose).
	landedIDs := c.ZRange(ctx, taskcard.StreamKey(hierStream, "landed"), 0, -1).Val()
	sort.Strings(landedIDs)
	if got := strings.Join(landedIDs, " "); got != "5001 5002 autonomy:sentinel "+parent+" "+stitch {
		t.Fatalf("ws:%s:landed is %q, want the children, the sentinel, the parent and the stitch", hierStream, got)
	}
	// Plans lists the plan for the stream, its children in order.
	plans, err := taskcard.Plans(ctx, c, []string{hierStream, "no such stream"})
	if err != nil || len(plans) != 1 || plans[0].ID != parent || len(plans[0].Children) != 2 || plans[0].Children[0].ID != "5001" {
		t.Fatalf("plans %+v %v", plans, err)
	}
	if line := plans[0].Line(); line != "plan plan-4317 landed children=2 waiting=0 ready=0 working=0 review=0 merging=0 landed=2 done=0 parked=0 stitch="+stitch+":landed" {
		t.Fatalf("line %q", line)
	}
	clean(t, c, "after the plan landed")
}

// TestPlanBindRefusesAParentThatMovedOn: a parent that is working, landed
// or missing is refused by name; a rerun on a waiting stitch appends
// children; nothing else is written.
func TestPlanBindRefusesAParentThatMovedOn(t *testing.T) {
	t.Parallel()
	_, c := wstest.Start(t)
	ctx := context.Background()
	if _, err := taskcard.BindPlan(ctx, c, "ghost", []string{"a"}, "ghost-stitch", "rowan"); err == nil || !strings.Contains(err.Error(), "no task:ghost: push the parent first") {
		t.Fatalf("missing parent: %v", err)
	}
	parent := "plan-two"
	stitch := pushPlanCards(t, c, parent, "5101")
	if _, err := taskcard.BindPlan(ctx, c, parent, []string{"5101"}, stitch, "rowan"); err != nil {
		t.Fatal(err)
	}
	// More children while the stitch waits: appended, not replaced.
	if _, err := taskcard.Push(ctx, c, taskcard.PushRequest{ID: "5102", Where: "waiting", Stream: hierStream, Sprint: sprint, Kind: "build",
		Title: "child 5102", By: "rowan", Fields: []string{taskcard.FieldParent, parent, taskcard.FieldPhase, taskcard.PhaseChild}}); err != nil {
		t.Fatal(err)
	}
	if _, err := taskcard.BindPlan(ctx, c, parent, []string{"5102"}, stitch, "rowan"); err != nil {
		t.Fatal(err)
	}
	if got := c.HGet(ctx, taskcard.Key(parent), "children").Val(); got != "5101 5102" {
		t.Fatalf("children %q, want appended", got)
	}
	if _, err := taskcard.BindPlan(ctx, c, parent, nil, "other-stitch", "rowan"); err == nil || !strings.Contains(err.Error(), "is a plan whose stitch is "+stitch+", not other-stitch") {
		t.Fatalf("another stitch: %v", err)
	}
	// A re-cut's stitch while the old one lives is refused by name.
	if _, err := taskcard.BindPlan(ctx, c, parent, nil, taskcard.NextStitchID(parent, stitch), "rowan"); err == nil ||
		!strings.Contains(err.Error(), stitch+" is waiting, and a stitch is re-cut (as plan-two-stitch-2) only once the old one ended done") {
		t.Fatalf("re-cut over a live stitch: %v", err)
	}
	// A parent that was taken is refused: a plan is cut while it waits.
	other := "plan-three"
	pushPlanCards(t, c, other, "5201")
	if _, err := taskcard.Move(ctx, c, other, "ready", taskcard.Opts{By: "rowan", Why: "test"}); err != nil {
		t.Fatal(err)
	}
	if _, err := taskcard.Take(ctx, c, "emma", 1, "emma", other); err != nil {
		t.Fatal(err)
	}
	if _, err := taskcard.BindPlan(ctx, c, other, []string{"5201"}, taskcard.StitchID(other), "rowan"); err == nil || !strings.Contains(err.Error(), "task:plan-three is working; a plan is cut while its parent waits") {
		t.Fatalf("working parent: %v", err)
	}
	if got := c.HGet(ctx, taskcard.Key(other), "kind").Val(); got != "build" {
		t.Fatalf("the refused bind wrote kind %q", got)
	}
	clean(t, c, "after the refusals")
}

// TestStitchLandedByAnyDoorLandsThePlan is the cold read's #1: a stitch
// landed by task land --id (merging -> landed), not the stream landing,
// lands its parent in the same move; and a plan is never moved to ready
// (the resolver's door refuses it by name).
func TestStitchLandedByAnyDoorLandsThePlan(t *testing.T) {
	t.Parallel()
	_, c := wstest.Start(t)
	ctx := context.Background()
	parent := "plan-door"
	stitch := pushPlanCards(t, c, parent, "5301")
	if _, err := taskcard.BindPlan(ctx, c, parent, []string{"5301"}, stitch, "rowan"); err != nil {
		t.Fatal(err)
	}
	if _, err := taskcard.Move(ctx, c, parent, "ready", taskcard.Opts{By: "rowan", Why: "depends-on met"}); err == nil || !strings.Contains(err.Error(), "PLAN task:plan-door is a plan: it waits on its stitch and lands with it, never ready") {
		t.Fatalf("plan -> ready: %v", err)
	}
	landCard(t, c, "5301", "6301", head(31))
	for _, step := range []func() error{
		func() error {
			_, err := taskcard.Move(ctx, c, stitch, "ready", taskcard.Opts{By: "rowan", Why: "test"})
			return err
		},
		func() error { _, err := taskcard.Take(ctx, c, "emma", 1, "emma", stitch); return err },
		func() error { _, err := taskcard.Done(ctx, c, stitch, "emma", "DONE", "6302"); return err },
	} {
		if err := step(); err != nil {
			t.Fatal(err)
		}
	}
	// The move's reply names the plan it landed with its ref (the fix of
	// #4317: task land --id <stitch> prints it so the plan's issue closes).
	if r, err := taskcard.Land(ctx, c, stitch, "rowan", head(32), ""); err != nil || r.To != "landed" || r.Parent != parent || r.ParentRef != "mas-bandwidth/nova-tools#4317" || r.ParentOrigin != "" {
		t.Fatalf("land of the stitch: %+v %v", r, err)
	}
	rec := c.HGetAll(ctx, taskcard.Key(parent)).Val()
	if rec["where"] != "landed" || rec["merge_sha"] != head(32) || !strings.Contains(rec["why"], "stitch "+stitch+" landed") {
		t.Fatalf("parent after task land on the stitch: where=%s merge_sha=%s why=%q", rec["where"], rec["merge_sha"], rec["why"])
	}
	if n := c.ZCard(ctx, taskcard.StreamKey(hierStream, "ready")).Val(); n != 0 {
		t.Fatalf("ws:ready holds %d, want 0 (a plan is never ready)", n)
	}
	clean(t, c, "after the hand landing")
}

// TestCancelOfAPlanCascadesOrRefuses is the cold read's #4: cancelling a
// plan cancels its live children and stitch first; a child in flight
// refuses the whole cancel by name before any write; the Lua door refuses a
// hand move of a plan to done while children are live.
func TestCancelOfAPlanCascadesOrRefuses(t *testing.T) {
	t.Parallel()
	_, c := wstest.Start(t)
	ctx := context.Background()
	parent := "plan-cancel"
	stitch := pushPlanCards(t, c, parent, "5401", "5402")
	if _, err := taskcard.BindPlan(ctx, c, parent, []string{"5401", "5402"}, stitch, "rowan"); err != nil {
		t.Fatal(err)
	}
	if _, err := taskcard.Move(ctx, c, parent, "done", taskcard.Opts{By: "rowan", Why: "by hand", OK: "fail"}); err == nil ||
		!strings.Contains(err.Error(), "PLAN task:plan-cancel has live children 5401,5402 and stitch "+stitch+": nova-sprint task cancel --id plan-cancel cancels them first") {
		t.Fatalf("hand move to done: %v", err)
	}
	if _, err := taskcard.Move(ctx, c, "5401", "ready", taskcard.Opts{By: "rowan", Why: "test"}); err != nil {
		t.Fatal(err)
	}
	if _, err := taskcard.Take(ctx, c, "emma", 1, "emma", "5401"); err != nil {
		t.Fatal(err)
	}
	if _, err := taskcard.Cancel(ctx, c, parent, "rowan", "scrapped"); err == nil || !strings.Contains(err.Error(), "PLAN task:plan-cancel has children in flight: 5401 (working); end or cancel them first") {
		t.Fatalf("cancel with a child working: %v", err)
	}
	for _, id := range []string{"5402", stitch, parent} {
		if w := where(t, c, id); w == "done" {
			t.Fatalf("%s is done after the refused cancel", id)
		}
	}
	if _, err := taskcard.Cancel(ctx, c, "5401", "emma", "gave up"); err != nil {
		t.Fatal(err)
	}
	if _, err := taskcard.Cancel(ctx, c, parent, "rowan", "scrapped"); err != nil {
		t.Fatalf("cascade: %v", err)
	}
	for _, id := range []string{"5401", "5402", stitch, parent} {
		rec := c.HGetAll(ctx, taskcard.Key(id)).Val()
		if rec["where"] != "done" || rec["where_ok"] != "fail" {
			t.Errorf("%s after the cascade: where=%s ok=%s", id, rec["where"], rec["where_ok"])
		}
		if id != parent && id != "5401" && !strings.Contains(rec["why"], "plan plan-cancel cancelled: scrapped") {
			t.Errorf("%s why %q", id, rec["why"])
		}
	}
	// Nothing of the plan is left waiting: only the stream's sentinel (#4318).
	if got := strings.Join(c.ZRange(ctx, taskcard.StreamKey(hierStream, "waiting"), 0, -1).Val(), " "); got != "autonomy:sentinel" {
		t.Fatalf("ws:waiting holds %q after the cascade, want only the sentinel", got)
	}
	if p, err := taskcard.ReadPlan(ctx, c, parent); err != nil || p.State() != "done" {
		t.Fatalf("plan state %v %v", p.State(), err)
	}
	clean(t, c, "after the cascade")
}

// TestDropChildUnsticksAPlan: a cancelled child leaves the plan stuck;
// DropChild removes it from the parent and the stitch's edges through the
// one move and the plan waits again; a live child is refused.
func TestDropChildUnsticksAPlan(t *testing.T) {
	t.Parallel()
	_, c := wstest.Start(t)
	ctx := context.Background()
	parent := "plan-drop"
	stitch := pushPlanCards(t, c, parent, "5501", "5502")
	if _, err := taskcard.BindPlan(ctx, c, parent, []string{"5501", "5502"}, stitch, "rowan"); err != nil {
		t.Fatal(err)
	}
	if _, err := taskcard.DropChild(ctx, c, "5501", "rowan"); err == nil || !strings.Contains(err.Error(), "task:5501 is waiting, not done: nova-sprint task cancel --id 5501") {
		t.Fatalf("drop of a live child: %v", err)
	}
	if _, err := taskcard.Cancel(ctx, c, "5501", "rowan", "not needed"); err != nil {
		t.Fatal(err)
	}
	p, err := taskcard.ReadPlan(ctx, c, parent)
	if err != nil || p.State() != taskcard.Stuck || !strings.Contains(p.Remedy(), "card stitch --id plan-drop --drop 5501") {
		t.Fatalf("stuck: %s %q %v", p.State(), p.Remedy(), err)
	}
	p, err = taskcard.DropChild(ctx, c, "5501", "rowan")
	if err != nil || p.State() != "waiting" || len(p.Children) != 1 || p.Children[0].ID != "5502" {
		t.Fatalf("after drop: %+v %v", p, err)
	}
	if got := c.HGet(ctx, taskcard.Key(stitch), "blocked_on").Val(); got != "5502" {
		t.Fatalf("stitch edges %q", got)
	}
	if body := c.HGet(ctx, taskcard.Key(stitch), "body").Val(); strings.Contains(body, "5501") || !strings.Contains(body, "- 5502 waiting") {
		t.Fatalf("brief after drop:\n%s", body)
	}
	clean(t, c, "after the drop")
}
