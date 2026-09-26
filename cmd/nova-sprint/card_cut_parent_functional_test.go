//go:build functional

package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws/wstest"
)

// TestCardCutParentEndToEnd is nova-tools#4317's DONE-WHEN on a real store,
// through the verbs: a parent card is cut into two children and a stitch in
// one call (--no-github); the parent DEPENDS-ON the stitch and shows under
// its stream in stream ls --tree, collapsed and expanded, with its derived
// state; card stitch prints the brief and --write refreshes it; when the
// children and the stitch land, the parent is landed with them and the tree
// says so.
func TestCardCutParentEndToEnd(t *testing.T) {
	t.Parallel()
	addr, c := wstest.Start(t)
	ctx := context.Background()
	// Every verb takes the store on its flags: no env, so the test runs
	// beside every other.
	run := func(args ...string) (int, string, string) {
		return runSprint(append(args, "--redis", addr)...)
	}
	const stream = "autonomy"
	if _, err := taskcard.Push(ctx, c, taskcard.PushRequest{ID: "hier", Where: "waiting", Stream: stream, Kind: "build",
		Ref: "mas-bandwidth/nova-tools#4317", Title: "work as a hierarchy", Repo: "mas-bandwidth/nova-tools", By: "rowan",
		Fields: []string{"base", "dev", "base_sha", cutFromSHA, "paths", "internal/nsprint/taskcard", "done_when", "the plan holds"}}); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	tsv := filepath.Join(dir, "children.tsv")
	rows := "id\ttitle\tpaths\tdone-when\tdepends-on\troute\n" +
		"the-model\tthe model\tinternal/nsprint/taskcard/hierarchy.go\tPlan.State is derived\tnone\tpro\n" +
		"the-verb\tthe verb\tcmd/nova-sprint/card_cut_from.go\tcard cut --parent\tthe-model\tfriend\n"
	if err := os.WriteFile(tsv, []byte(rows), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := run("card", "cut", "--parent", "hier", "--from", tsv, "--no-github", "--actor", "rowan")
	if code != 0 {
		t.Fatalf("cut exit %d:\n%s%s", code, out, errOut)
	}
	for _, want := range []string{
		"CARD CUT row=1 id=the-model ref=- stream=autonomy to=waiting depends=none\n",
		"CARD CUT row=2 id=the-verb ref=- stream=autonomy to=waiting depends=the-model\n",
		"CARD CUT row=stitch id=hier-stitch ref=- stream=autonomy to=waiting depends=the-model,the-verb\n",
		"CARD CUT PLAN parent=hier children=2 stitch=hier-stitch parent_to=waiting depends=hier-stitch\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	rec := c.HGetAll(ctx, taskcard.Key("hier")).Val()
	for k, want := range map[string]string{"where": "waiting", "kind": "plan", "children": "the-model the-verb", "stitch": "hier-stitch", "blocked_on": "hier-stitch"} {
		if rec[k] != want {
			t.Errorf("parent %s = %q, want %q", k, rec[k], want)
		}
	}
	child := c.HGetAll(ctx, taskcard.Key("the-verb")).Val()
	if child["parent"] != "hier" || child["phase"] != "child" || child["blocked_on"] != "the-model" || child["stream"] != stream {
		t.Errorf("child record %v", child)
	}
	stitch := c.HGetAll(ctx, taskcard.Key("hier-stitch")).Val()
	if stitch["parent"] != "hier" || stitch["phase"] != "stitch" || stitch["kind"] != "stitch" || stitch["route"] != "frontier" ||
		stitch["done_when"] != "the plan holds" || stitch["paths"] != "internal/nsprint/taskcard/hierarchy.go cmd/nova-sprint/card_cut_from.go" ||
		!strings.Contains(stitch["body"], "- the-model waiting pr=-") {
		t.Errorf("stitch record %v", stitch)
	}
	// A rerun of the same file is idempotent: every row to=already, the
	// bind a no-op, exit 0, nothing pushed twice.
	code, out, _ = run("card", "cut", "--parent", "hier", "--from", tsv, "--no-github", "--actor", "rowan")
	if code != 0 || strings.Count(out, " to=already ") != 3 || strings.Contains(out, "REFUSED") ||
		!strings.Contains(out, "CARD CUT PLAN parent=hier children=2 stitch=hier-stitch parent_to=waiting depends=hier-stitch\n") {
		t.Fatalf("rerun: exit %d:\n%s", code, out)
	}
	if got := c.HGetAll(ctx, taskcard.Key("hier")).Val(); got["children"] != "the-model the-verb" || got["where"] != "waiting" {
		t.Fatalf("parent after the rerun %v", got)
	}
	// More children while the stitch waits: the new child is pushed, the
	// parent lists it, and the waiting stitch's edge grows to it (it is
	// never pushed again, so the store's EXISTS never orphans a child).
	more := filepath.Join(dir, "more.tsv")
	if err := os.WriteFile(more, []byte("id\ttitle\tpaths\tdone-when\nthe-docs\tthe docs\tdocs/CLI.md\tdocumented\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, _ = run("card", "cut", "--parent", "hier", "--from", more, "--no-github", "--actor", "rowan")
	if code != 0 || !strings.Contains(out, "CARD CUT row=1 id=the-docs ref=- stream=autonomy to=waiting depends=none\n") ||
		!strings.Contains(out, "CARD CUT row=stitch id=hier-stitch ref=- stream=autonomy to=already depends=the-docs,the-model,the-verb\n") ||
		!strings.Contains(out, "CARD CUT PLAN parent=hier children=1 stitch=hier-stitch") {
		t.Fatalf("more children: exit %d:\n%s", code, out)
	}
	if got := c.HGetAll(ctx, taskcard.Key("hier")).Val(); got["children"] != "the-model the-verb the-docs" {
		t.Fatalf("parent children %q after more", got["children"])
	}
	if got := c.HGetAll(ctx, taskcard.Key("hier-stitch")).Val(); got["blocked_on"] != "the-model,the-verb,the-docs" || got["where"] != "waiting" || !strings.Contains(got["body"], "- the-docs waiting") {
		t.Fatalf("stitch after more: blocked_on=%q where=%s", got["blocked_on"], got["where"])
	}
	if got := c.HGetAll(ctx, taskcard.Key("the-docs")).Val(); got["parent"] != "hier" || got["phase"] != "child" {
		t.Fatalf("new child %v", got)
	}
	// The third child is cancelled: the plan is stuck (the stitch waits on
	// an edge that is never met) and names the remedy; card stitch --drop
	// takes the child out of the plan and the stitch's edges, and the plan
	// moves on with two children.
	if _, err := taskcard.Cancel(ctx, c, "the-docs", "rowan", "not needed"); err != nil {
		t.Fatal(err)
	}
	if code, out, _ := run("stream", "ls", "--tree"); !strings.Contains(out, "  plan hier stuck children=3 ") {
		t.Fatalf("a failed child derives stuck (exit %d):\n%s", code, out)
	}
	if code, out, _ := run("card", "stitch", "--id", "hier"); code != 0 || !strings.Contains(out, "stuck: the-docs ended done/fail and the stitch waits on it; nova-sprint card stitch --drop the-docs drops it from the plan") {
		t.Fatalf("the stuck remedy (exit %d):\n%s", code, out)
	}
	if code, out, _ := run("card", "stitch", "--drop", "the-model"); code != 1 || !strings.Contains(out, `REFUSED card stitch drop=the-model why=task:the-model\x20is\x20waiting,\x20not\x20done:`) {
		t.Fatalf("drop of a live child (exit %d):\n%s", code, out)
	}
	if code, out, _ := run("card", "stitch", "--drop", "the-docs", "--as", "rowan"); code != 0 || !strings.Contains(out, "STITCH DROP parent=hier child=the-docs children=2 state=waiting ms=") {
		t.Fatalf("drop (exit %d):\n%s", code, out)
	}
	if got := c.HGetAll(ctx, taskcard.Key("hier")).Val(); got["children"] != "the-model the-verb" {
		t.Fatalf("children after drop %q", got["children"])
	}
	if got := c.HGetAll(ctx, taskcard.Key("hier-stitch")).Val(); got["blocked_on"] != "the-model,the-verb" || strings.Contains(got["body"], "the-docs") {
		t.Fatalf("stitch after drop: blocked_on=%q body has the-docs=%v", got["blocked_on"], strings.Contains(got["body"], "the-docs"))
	}
	if code, out, _ := run("card", "stitch", "--drop", "the-docs"); code != 1 || !strings.Contains(out, `is\x20not\x20among\x20plan\x20hier's\x20children`) {
		t.Fatalf("a second drop (exit %d):\n%s", code, out)
	}

	// The tree, collapsed and expanded.
	code, out, _ = run("stream", "ls", "--tree")
	if code != 0 || !strings.Contains(out, "\n  plan hier waiting children=2 waiting=2 ready=0 working=0 review=0 merging=0 landed=0 done=0 parked=0 stitch=hier-stitch:waiting\n") || strings.Contains(out, "child ") || !strings.Contains(out, "STREAMS n=1 plans=1 ms=") {
		t.Fatalf("stream ls --tree exit %d:\n%s", code, out)
	}
	code, out, _ = run("stream", "ls", "--tree", "--expand")
	if code != 0 || !strings.Contains(out, "\n    child the-model waiting pr=- score=-\n    child the-verb waiting pr=- score=-\n    stitch hier-stitch waiting pr=-\n") {
		t.Fatalf("stream ls --tree --expand exit %d:\n%s", code, out)
	}
	if code, _, errOut := run("stream", "ls", "--expand"); code != 2 || !strings.Contains(errOut, "--expand goes with --tree") {
		t.Fatalf("--expand alone: exit %d %q", code, errOut)
	}
	// card stitch prints the plan and the brief, by the parent or the stitch.
	for _, id := range []string{"hier", "hier-stitch"} {
		code, out, _ = run("card", "stitch", "--id", id)
		if code != 0 || !strings.HasPrefix(out, "plan hier waiting children=2") || !strings.Contains(out, "\n"+taskcard.BriefMarker) ||
			!strings.Contains(out, "\nPLAN id=hier state=waiting children=2 stitch=hier-stitch:waiting written=0 ms=") {
			t.Fatalf("card stitch --id %s exit %d:\n%s", id, code, out)
		}
	}
	if code, out, _ := run("card", "stitch", "--id", "the-model"); code != 1 || !strings.Contains(out, `REFUSED card stitch id=the-model why=task:the-model\x20is\x20not\x20a\x20plan remedy=`) {
		t.Fatalf("card stitch on a child: exit %d:\n%s", code, out)
	}

	// The children land; the stitch, once released and landed, lands the
	// parent; the tree shows every state on the way.
	land := func(id, pr, sha string) {
		t.Helper()
		if _, err := taskcard.Move(ctx, c, id, "ready", taskcard.Opts{By: "rowan", Why: "test"}); err != nil {
			t.Fatal(err)
		}
		if _, err := taskcard.Take(ctx, c, "emma", 1, "emma", id); err != nil {
			t.Fatal(err)
		}
		if _, err := taskcard.Done(ctx, c, id, "emma", "DONE", pr); err != nil {
			t.Fatal(err)
		}
		c.HSet(ctx, taskcard.Key(id), "line2", "DONE", "score", "10", "head", sha)
		if _, err := taskcard.LandStream(ctx, c, stream, sha, "rowan", ""); err != nil {
			t.Fatal(err)
		}
	}
	sha := func(i int) string { return strings.Repeat("0", 39) + string(rune('0'+i)) }
	land("the-model", "71", sha(1))
	if _, err := taskcard.Move(ctx, c, "the-verb", "ready", taskcard.Opts{By: "rowan", Why: "test"}); err != nil {
		t.Fatal(err)
	}
	if _, err := taskcard.Take(ctx, c, "emma", 1, "emma", "the-verb"); err != nil {
		t.Fatal(err)
	}
	code, out, _ = run("stream", "ls", "--tree")
	if !strings.Contains(out, "  plan hier working children=2 waiting=0 ready=0 working=1 review=0 merging=0 landed=1 done=0 parked=0 stitch=hier-stitch:waiting\n") {
		t.Fatalf("tree with a child working:\n%s", out)
	}
	if _, err := taskcard.Done(ctx, c, "the-verb", "emma", "DONE", "72"); err != nil {
		t.Fatal(err)
	}
	c.HSet(ctx, taskcard.Key("the-verb"), "line2", "DONE", "score", "9", "head", sha(2))
	if _, err := taskcard.LandStream(ctx, c, stream, sha(2), "rowan", ""); err != nil {
		t.Fatal(err)
	}
	code, out, _ = run("card", "stitch", "--id", "hier", "--write")
	if code != 0 || !strings.Contains(out, "- the-model landed pr=nova-tools#71 head=000000000000 score=10/10") || !strings.Contains(out, "- the-verb landed pr=nova-tools#72 head=000000000000 score=9/10") || !strings.Contains(out, " written=1 ") {
		t.Fatalf("card stitch --write exit %d:\n%s", code, out)
	}
	if body := c.HGet(ctx, taskcard.Key("hier-stitch"), "body").Val(); !strings.Contains(body, "- the-verb landed pr=nova-tools#72") || strings.Count(body, taskcard.BriefMarker) != 1 {
		t.Fatalf("stitch body after --write:\n%s", body)
	}
	if code, out, _ := run("card", "stitch", "--drop", "the-model"); code != 1 || !strings.Contains(out, `REFUSED card stitch drop=the-model why=task:the-model\x20landed:`) {
		t.Fatalf("drop of a landed child (exit %d):\n%s", code, out)
	}
	land("hier-stitch", "73", sha(3))
	code, out, _ = run("stream", "ls", "--tree")
	if !strings.Contains(out, "  plan hier landed children=2 waiting=0 ready=0 working=0 review=0 merging=0 landed=2 done=0 parked=0 stitch=hier-stitch:landed\n") {
		t.Fatalf("tree after the stitch landed:\n%s", out)
	}
	if got := c.HGet(ctx, taskcard.Key("hier"), "where").Val(); got != "landed" {
		t.Fatalf("parent where %q, want landed", got)
	}
	// task land on the parent is the same door by hand: already landed, it
	// stays (landed -> landed, nothing written).
	if code, out, _ := run("task", "land", "--id", "hier", "--sha", sha(3), "--actor", "rowan"); code != 0 || !strings.Contains(out, "from=landed to=landed") {
		t.Fatalf("task land on the landed parent: exit %d %s", code, out)
	}
}

// TestCardCutParentGrowFilesNoSecondStitchIssue is the cold read's #2 on a
// real store with GitHub on (the fake forge): growing a plan whose stitch
// waits files the new child's issue and never the stitch's again; the
// stitch's receipt names each child once.
func TestCardCutParentGrowFilesNoSecondStitchIssue(t *testing.T) {
	t.Parallel()
	_, c := wstest.Start(t)
	ctx := context.Background()
	if _, err := taskcard.Push(ctx, c, taskcard.PushRequest{ID: "grow", Where: "waiting", Stream: "autonomy", Kind: "build",
		Ref: "mas-bandwidth/nova-tools#4317", Title: "grow", Repo: "mas-bandwidth/nova-tools", By: "rowan",
		Fields: []string{"base", "dev", "base_sha", cutFromSHA, "paths", "a.go", "done_when", "holds"}}); err != nil {
		t.Fatal(err)
	}
	forge := &fakeCutForge{}
	d := cutDepsRedis(forge, c)
	d.Plan = func(ctx context.Context, id string) (planFacts, error) { return readPlanFacts(ctx, c, id) }
	d.Bind = func(ctx context.Context, parent string, children []string, stitch, by string) (taskcard.Result, error) {
		return taskcard.BindPlan(ctx, c, parent, children, stitch, by)
	}
	first := "id\ttitle\tpaths\tdone-when\nc1\tone\ta.go\tholds\nc2\ttwo\tb.go\tholds\n"
	code, out := runCutFrom(cutFromOpts{Text: []byte(first), Parent: "grow"}, d)
	if code != 0 || len(forge.titles) != 3 || forge.titles[2] != "stitch: grow" {
		t.Fatalf("first cut exit %d filed %v:\n%s", code, forge.titles, out)
	}
	// A rerun: every row already, no issue filed, the receipt's edges once.
	code, out = runCutFrom(cutFromOpts{Text: []byte(first), Parent: "grow"}, d)
	if code != 0 || len(forge.titles) != 3 || strings.Count(out, "to=already") != 3 ||
		!strings.Contains(out, "CARD CUT row=stitch id=grow-stitch ref=mas-bandwidth/nova-tools#5002 stream=autonomy to=already depends=c1,c2\n") {
		t.Fatalf("rerun exit %d filed %v:\n%s", code, forge.titles, out)
	}
	// Growing: one issue for the new child, none for the stitch.
	code, out = runCutFrom(cutFromOpts{Text: []byte("id\ttitle\tpaths\tdone-when\nc3\tthree\tc.go\tholds\n"), Parent: "grow"}, d)
	if code != 0 || len(forge.titles) != 4 || forge.titles[3] != "three" ||
		!strings.Contains(out, "CARD CUT row=stitch id=grow-stitch ref=mas-bandwidth/nova-tools#5002 stream=autonomy to=already depends=c3,c1,c2\n") {
		t.Fatalf("grow exit %d filed %v:\n%s", code, forge.titles, out)
	}
	if got := c.HGet(ctx, taskcard.Key("grow-stitch"), "blocked_on").Val(); got != "c1,c2,c3" {
		t.Fatalf("stitch edges %q", got)
	}
	if got := c.HGet(ctx, taskcard.Key("grow"), "children").Val(); got != "c1 c2 c3" {
		t.Fatalf("children %q", got)
	}
}

// TestCancelledStitchIsStuckThenRecutLandsThePlan is the #4317 fix, the cold
// reader's exact sequence on a real store through the verbs: after the
// child e1 landed, task cancel --id pc-stitch (from ready) leaves the plan
// stuck, never waiting, and the cancel's receipt, card stitch and the tree
// name the remedy; running it (card cut --parent pc, no --from) re-cuts the
// stitch as pc-stitch-2 over every child and the parent waits on it; task
// land --id pc-stitch-2 lands the plan and its receipt names the plan with
// the ref and origin the lander closes; the stream's stop lands with it.
func TestCancelledStitchIsStuckThenRecutLandsThePlan(t *testing.T) {
	t.Parallel()
	addr, c := wstest.Start(t)
	ctx := context.Background()
	run := func(args ...string) (int, string, string) {
		return runSprint(append(args, "--redis", addr)...)
	}
	const stream = "autonomy"
	const ref, origin = "mas-bandwidth/nova-tools#4317", "https://forge.test/mas-bandwidth/nova-tools/issues/4317"
	if _, err := taskcard.Push(ctx, c, taskcard.PushRequest{ID: "pc", Where: "waiting", Stream: stream, Kind: "build",
		Ref: ref, Origin: origin, Title: "plan cancel", Repo: "mas-bandwidth/nova-tools", By: "rowan",
		Fields: []string{"base", "dev", "base_sha", cutFromSHA, "paths", "a.go", "done_when", "the plan holds"}}); err != nil {
		t.Fatal(err)
	}
	tsv := filepath.Join(t.TempDir(), "children.tsv")
	if err := os.WriteFile(tsv, []byte("id\ttitle\tpaths\tdone-when\ne1\tone\ta.go\tholds\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, out, errOut := run("card", "cut", "--parent", "pc", "--from", tsv, "--no-github", "--actor", "rowan"); code != 0 {
		t.Fatalf("cut exit %d:\n%s%s", code, out, errOut)
	}
	sha := func(i int) string { return strings.Repeat("0", 39) + string(rune('0'+i)) }
	walk := func(id, pr string) {
		t.Helper()
		if _, err := taskcard.Move(ctx, c, id, "ready", taskcard.Opts{By: "rowan", Why: "test"}); err != nil {
			t.Fatalf("%s ready: %v", id, err)
		}
		if _, err := taskcard.Take(ctx, c, "emma", 1, "emma", id); err != nil {
			t.Fatalf("take %s: %v", id, err)
		}
		if _, err := taskcard.Done(ctx, c, id, "emma", "DONE", pr); err != nil {
			t.Fatalf("done %s: %v", id, err)
		}
	}
	walk("e1", "81")
	if _, err := taskcard.LandStream(ctx, c, stream, sha(1), "rowan", ""); err != nil {
		t.Fatal(err)
	}
	// The resolver's release of the stitch, by hand; then the cancel.
	if _, err := taskcard.Move(ctx, c, "pc-stitch", "ready", taskcard.Opts{By: "rowan", Why: "depends-on met"}); err != nil {
		t.Fatal(err)
	}
	code, out, _ := run("task", "cancel", "--id", "pc-stitch", "--why", "wrong approach", "--actor", "rowan")
	if code != 0 || !strings.Contains(out, "TASK cancel id=pc-stitch from=ready to=done ms=") ||
		!strings.Contains(out, `PLAN id=pc state=stuck remedy="stuck: stitch pc-stitch ended done/fail and the plan lands only with its stitch; nova-sprint card cut --parent pc re-cuts the stitch (pc-stitch-2, DEPENDS-ON every child)"`) {
		t.Fatalf("cancel of the stitch (exit %d):\n%s", code, out)
	}
	if code, out, _ := run("card", "stitch", "--id", "pc"); code != 0 || !strings.Contains(out, "\nPLAN id=pc state=stuck children=1 stitch=pc-stitch:done written=0 ms=") ||
		!strings.Contains(out, "stuck: stitch pc-stitch ended done/fail and the plan lands only with its stitch; nova-sprint card cut --parent pc re-cuts the stitch (pc-stitch-2, DEPENDS-ON every child)") {
		t.Fatalf("card stitch on the stuck plan (exit %d):\n%s", code, out)
	}
	if _, out, _ := run("stream", "ls", "--tree"); !strings.Contains(out, "  plan pc stuck children=1 ") {
		t.Fatalf("tree:\n%s", out)
	}
	// The remedy: card cut --parent pc alone re-cuts the stitch.
	code, out, errOut := run("card", "cut", "--parent", "pc", "--no-github", "--actor", "rowan")
	if code != 0 || !strings.Contains(out, "CARD CUT row=stitch id=pc-stitch-2 ref=- stream=autonomy to=waiting depends=e1\n") ||
		!strings.Contains(out, "CARD CUT PLAN parent=pc children=0 stitch=pc-stitch-2 parent_to=waiting depends=pc-stitch-2\n") {
		t.Fatalf("re-cut (exit %d):\n%s%s", code, out, errOut)
	}
	rec := c.HGetAll(ctx, taskcard.Key("pc")).Val()
	for k, want := range map[string]string{"where": "waiting", "kind": "plan", "children": "e1", "stitch": "pc-stitch-2", "blocked_on": "pc-stitch-2"} {
		if rec[k] != want {
			t.Errorf("parent %s = %q, want %q", k, rec[k], want)
		}
	}
	if s := c.HGetAll(ctx, taskcard.Key("pc-stitch-2")).Val(); s["parent"] != "pc" || s["phase"] != "stitch" || s["blocked_on"] != "e1" || !strings.Contains(s["body"], "- e1 landed pr=") {
		t.Fatalf("the re-cut stitch %v", s)
	}
	if w := c.HGet(ctx, taskcard.Key("pc-stitch"), "where").Val(); w != "done" {
		t.Fatalf("the old stitch is %s, want it left done", w)
	}
	if _, out, _ := run("card", "stitch", "--id", "pc"); !strings.Contains(out, "\nPLAN id=pc state=waiting children=1 stitch=pc-stitch-2:waiting ") {
		t.Fatalf("the plan after the re-cut:\n%s", out)
	}
	// A second bare cut while the new stitch waits is refused by name.
	if code, out, _ := run("card", "cut", "--parent", "pc", "--no-github", "--actor", "rowan"); code != 1 || !strings.Contains(out, `why="no children rows: --from <children.tsv> cuts children; card cut --parent pc alone re-cuts a stitch that ended done, and its stitch pc-stitch-2 is waiting"`) || !strings.Contains(out, " rows=0 cut=0 already=0 refused=1 ") {
		t.Fatalf("bare cut on a waiting stitch (exit %d):\n%s", code, out)
	}
	// The re-cut stitch lands by task land --id: the plan lands with it and
	// the receipt names it, its ref and origin; the stream's stop lands too.
	walk("pc-stitch-2", "82")
	code, out, _ = run("task", "land", "--id", "pc-stitch-2", "--sha", sha(2), "--actor", "rowan")
	if code != 0 || !strings.Contains(out, "TASK land id=pc-stitch-2 from=merging to=landed parent=pc ref="+ref+" origin="+origin+" ms=") {
		t.Fatalf("task land on the stitch (exit %d):\n%s", code, out)
	}
	rec = c.HGetAll(ctx, taskcard.Key("pc")).Val()
	if rec["where"] != "landed" || rec["merge_sha"] != sha(2) || rec["ref"] != ref || rec["origin"] != origin {
		t.Fatalf("parent after the stitch landed: where=%s merge_sha=%s ref=%s origin=%s", rec["where"], rec["merge_sha"], rec["ref"], rec["origin"])
	}
	if w := c.HGet(ctx, taskcard.Key(stream+":sentinel"), "where").Val(); w != "landed" {
		t.Fatalf("the stream's stop is %s, want landed with the plan", w)
	}
	// A plain card's land receipt names no plan.
	if _, out, _ := run("task", "land", "--id", "e1", "--sha", sha(1), "--actor", "rowan"); strings.Contains(out, "parent=") {
		t.Fatalf("a child's land names a plan:\n%s", out)
	}
}

// TestRecutStitchFilesItsOwnIssueWithGitHubOn is the fix round's owed (1)
// on a real store with GitHub on (the fake forge): a bare card cut --parent
// files each re-cut stitch's issue through the ledger keyed by plan and
// stitch id (taskcard.RecutLedgerKey), so two plans' re-cuts file two
// issues (never the first re-cut's reused), a plan on another repo re-cuts
// (never refused by an empty file's ledger), and a --from re-cut with the
// plan's own file files a new issue for the new stitch, not the old one's.
func TestRecutStitchFilesItsOwnIssueWithGitHubOn(t *testing.T) {
	t.Parallel()
	_, c := wstest.Start(t)
	ctx := context.Background()
	forge := &fakeCutForge{}
	d := cutDepsRedis(forge, c)
	d.Plan = func(ctx context.Context, id string) (planFacts, error) { return readPlanFacts(ctx, c, id) }
	d.Bind = func(ctx context.Context, parent string, children []string, stitch, by string) (taskcard.Result, error) {
		return taskcard.BindPlan(ctx, c, parent, children, stitch, by)
	}
	repoOf := map[string]string{"pa": "mas-bandwidth/nova-tools", "pb": "mas-bandwidth/nova-tools", "pz": "mas-bandwidth/other"}
	files := map[string]string{}
	for _, p := range []string{"pa", "pb", "pz"} {
		if _, err := taskcard.Push(ctx, c, taskcard.PushRequest{ID: p, Where: "waiting", Stream: "autonomy", Kind: "build",
			Ref: repoOf[p] + "#1", Title: "plan " + p, Repo: repoOf[p], By: "rowan",
			Fields: []string{"base", "dev", "base_sha", cutFromSHA, "paths", "a.go", "done_when", "holds"}}); err != nil {
			t.Fatal(err)
		}
		files[p] = "id\ttitle\tpaths\tdone-when\n" + p + "-c1\tone\ta.go\tholds\n"
		if code, out := runCutFrom(cutFromOpts{Text: []byte(files[p]), Parent: p, Repo: repoOf[p]}, d); code != 0 {
			t.Fatalf("cut %s exit %d:\n%s", p, code, out)
		}
		if _, err := taskcard.Cancel(ctx, c, p+"-stitch", "rowan", "wrong approach"); err != nil {
			t.Fatal(err)
		}
	}
	if len(forge.titles) != 6 {
		t.Fatalf("filed %v, want a child and a stitch per plan", forge.titles)
	}
	bare := func(p string) (int, string) {
		var b strings.Builder
		return cardCutFrom(ctx, cutFromOpts{Parent: p, Base: "dev", Actor: "rowan"}, d, &b), b.String()
	}
	for i, p := range []string{"pa", "pb", "pz"} {
		n := 5006 + i
		code, out := bare(p)
		want := fmt.Sprintf("CARD CUT row=stitch id=%s-stitch-2 ref=%s#%d stream=autonomy to=waiting depends=%s-c1\n", p, repoOf[p], n, p)
		if code != 0 || !strings.Contains(out, want) || !strings.Contains(out, " rows=1 cut=1 already=0 refused=0 filed=1 reused=0 github=on ") {
			t.Fatalf("bare re-cut of %s: exit %d, want %q:\n%s", p, code, want, out)
		}
		if got := c.HGetAll(ctx, taskcard.RecutLedgerKey(p, p+"-stitch-2")).Val(); got["repo"] != repoOf[p] || got["1"] != strconv.Itoa(n) || len(got) != 2 {
			t.Fatalf("%s's re-cut ledger %v", p, got)
		}
	}
	if n := c.Exists(ctx, taskcard.CutLedgerKey(nil)).Val(); n != 0 {
		t.Fatal("a bare re-cut wrote the empty file's ledger")
	}
	// A --from re-cut with pa's own file: the child is the ledger's (reused,
	// already), the new stitch files its own issue, not pa-stitch's #5001.
	if _, err := taskcard.Cancel(ctx, c, "pa-stitch-2", "rowan", "wrong again"); err != nil {
		t.Fatal(err)
	}
	code, out := runCutFrom(cutFromOpts{Text: []byte(files["pa"]), Parent: "pa"}, d)
	if code != 0 || !strings.Contains(out, "CARD CUT row=stitch id=pa-stitch-3 ref=mas-bandwidth/nova-tools#5009 stream=autonomy to=waiting depends=pa-c1\n") ||
		!strings.Contains(out, " filed=1 reused=1 ") || len(forge.titles) != 10 {
		t.Fatalf("--from re-cut exit %d filed %d:\n%s", code, len(forge.titles), out)
	}
	if got := c.HGet(ctx, taskcard.Key("pa"), "stitch").Val(); got != "pa-stitch-3" {
		t.Fatalf("pa's stitch %q", got)
	}
}

// TestRecutStitchFilingRefusalNamesItsRemedy is the third fix round's owed
// (3): a bare re-cut whose stitch issue the forge refuses prints the row's
// refusal with a remedy= field (rerun the cut; the re-cut's own ledger
// holds every issue filed), exits 1 and binds nothing; the rerun it names
// files the stitch's issue once and cuts it.
func TestRecutStitchFilingRefusalNamesItsRemedy(t *testing.T) {
	t.Parallel()
	_, c := wstest.Start(t)
	ctx := context.Background()
	forge := &fakeCutForge{}
	d := cutDepsRedis(forge, c)
	d.Plan = func(ctx context.Context, id string) (planFacts, error) { return readPlanFacts(ctx, c, id) }
	d.Bind = func(ctx context.Context, parent string, children []string, stitch, by string) (taskcard.Result, error) {
		return taskcard.BindPlan(ctx, c, parent, children, stitch, by)
	}
	if _, err := taskcard.Push(ctx, c, taskcard.PushRequest{ID: "rf", Where: "waiting", Stream: "autonomy", Kind: "build",
		Ref: "mas-bandwidth/nova-tools#1", Title: "plan rf", Repo: "mas-bandwidth/nova-tools", By: "rowan",
		Fields: []string{"base", "dev", "base_sha", cutFromSHA, "paths", "a.go", "done_when", "holds"}}); err != nil {
		t.Fatal(err)
	}
	if code, out := runCutFrom(cutFromOpts{Text: []byte("id\ttitle\tpaths\tdone-when\nrf-c1\tone\ta.go\tholds\n"), Parent: "rf"}, d); code != 0 {
		t.Fatalf("cut rf exit %d:\n%s", code, out)
	}
	if _, err := taskcard.Cancel(ctx, c, "rf-stitch", "rowan", "wrong approach"); err != nil {
		t.Fatal(err)
	}
	bare := func() (int, string) {
		var b strings.Builder
		return cardCutFrom(ctx, cutFromOpts{Parent: "rf", Base: "dev", Actor: "rowan"}, d, &b), b.String()
	}
	forge.failAt = len(forge.titles) + 1
	code, out := bare()
	want := `why="file: HTTP 403: rate limited" remedy="rerun nova-sprint card cut --parent rf with the same flags; ` +
		taskcard.RecutLedgerKey("rf", "rf-stitch-2") + ` holds every issue filed, so none is filed twice"` + "\n"
	if code != 1 || !strings.Contains(out, "CARD CUT REFUSED row=stitch ") || !strings.Contains(out, want) || strings.Contains(out, "CARD CUT PLAN") {
		t.Fatalf("refused re-cut filing: exit %d, want %q:\n%s", code, want, out)
	}
	if got := c.HGet(ctx, taskcard.Key("rf"), "stitch").Val(); got != "rf-stitch" {
		t.Fatalf("a refused re-cut bound stitch %q", got)
	}
	forge.failAt = 0
	code, out = bare()
	if code != 0 || !strings.Contains(out, "CARD CUT row=stitch id=rf-stitch-2 ref=mas-bandwidth/nova-tools#5002 stream=autonomy to=waiting depends=rf-c1\n") ||
		!strings.Contains(out, " filed=1 ") || len(forge.titles) != 3 {
		t.Fatalf("the rerun the remedy names: exit %d filed %v:\n%s", code, forge.titles, out)
	}
}

// TestStuckPlanRemedyRunsAsPrinted is the fix round's owed (2): the drop a
// stuck plan's remedy prints is card stitch --drop <child> (the old --id
// <plan> --drop <child> form exits 2), and the test runs the printed
// command verbatim through the CLI, the store on --redis: first a failed
// child alone, then a failed child and an ended stitch, where the drop runs
// as printed and then the printed re-cut (with --no-github --actor) lands
// the plan a new stitch.
func TestStuckPlanRemedyRunsAsPrinted(t *testing.T) {
	t.Parallel()
	addr, c := wstest.Start(t)
	ctx := context.Background()
	run := func(args ...string) (int, string, string) {
		return runSprint(append(args, "--redis", addr)...)
	}
	if _, err := taskcard.Push(ctx, c, taskcard.PushRequest{ID: "rp", Where: "waiting", Stream: "autonomy", Kind: "build",
		Ref: "mas-bandwidth/nova-tools#4317", Title: "remedy", Repo: "mas-bandwidth/nova-tools", By: "rowan",
		Fields: []string{"base", "dev", "base_sha", cutFromSHA, "paths", "a.go", "done_when", "holds"}}); err != nil {
		t.Fatal(err)
	}
	tsv := filepath.Join(t.TempDir(), "children.tsv")
	if err := os.WriteFile(tsv, []byte("id\ttitle\tpaths\tdone-when\nd1\tone\ta.go\tholds\nd2\ttwo\tb.go\tholds\nd3\tthree\tc.go\tholds\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, out, errOut := run("card", "cut", "--parent", "rp", "--from", tsv, "--no-github", "--actor", "rowan"); code != 0 {
		t.Fatalf("cut exit %d:\n%s%s", code, out, errOut)
	}
	if code, _, errOut := run("card", "stitch", "--id", "rp", "--drop", "d3"); code != 2 || !strings.Contains(errOut, "wants --id <parent|stitch> [--write], or --drop <child>") {
		t.Fatalf("the old remedy form: exit %d %q, want 2", code, errOut)
	}
	printed := regexp.MustCompile(`nova-sprint (card stitch --drop \S+)`)
	remedy := func(want string) []string {
		t.Helper()
		code, out, _ := run("card", "stitch", "--id", "rp")
		m := printed.FindStringSubmatch(out)
		if code != 0 || m == nil || !strings.Contains(out, want) || strings.Contains(out, "--id rp --drop") {
			t.Fatalf("remedy (exit %d), want %q:\n%s", code, want, out)
		}
		return strings.Fields(m[1])
	}
	// A failed child alone: the drop as printed.
	if _, err := taskcard.Cancel(ctx, c, "d3", "rowan", "not needed"); err != nil {
		t.Fatal(err)
	}
	drop := remedy("stuck: d3 ended done/fail and the stitch waits on it; nova-sprint card stitch --drop d3 drops it from the plan")
	if code, out, errOut := run(drop...); code != 0 || !strings.Contains(out, "STITCH DROP parent=rp child=d3 children=2 state=waiting ms=") {
		t.Fatalf("%v as printed: exit %d\n%s%s", drop, code, out, errOut)
	}
	// A failed child and an ended stitch: the drop as printed, then the re-cut.
	if _, err := taskcard.Cancel(ctx, c, "d2", "rowan", "not needed"); err != nil {
		t.Fatal(err)
	}
	if _, err := taskcard.Cancel(ctx, c, "rp-stitch", "rowan", "wrong approach"); err != nil {
		t.Fatal(err)
	}
	drop = remedy("stuck: stitch rp-stitch ended done/fail and the plan lands only with its stitch, and d2 ended done/fail; nova-sprint card stitch --drop d2 drops it, then nova-sprint card cut --parent rp re-cuts the stitch (rp-stitch-2, DEPENDS-ON every child)")
	if code, out, errOut := run(drop...); code != 0 || !strings.Contains(out, "STITCH DROP parent=rp child=d2 children=1 state=stuck ms=") {
		t.Fatalf("%v as printed: exit %d\n%s%s", drop, code, out, errOut)
	}
	if code, out, errOut := run("card", "cut", "--parent", "rp", "--no-github", "--actor", "rowan"); code != 0 ||
		!strings.Contains(out, "CARD CUT PLAN parent=rp children=0 stitch=rp-stitch-2 parent_to=waiting depends=rp-stitch-2\n") ||
		!strings.Contains(out, "CARD CUT row=stitch id=rp-stitch-2 ref=- stream=autonomy to=waiting depends=d1\n") {
		t.Fatalf("the printed re-cut: exit %d\n%s%s", code, out, errOut)
	}
	if _, out, _ := run("card", "stitch", "--id", "rp"); !strings.Contains(out, "\nPLAN id=rp state=waiting children=1 stitch=rp-stitch-2:waiting ") {
		t.Fatalf("the plan after the remedy:\n%s", out)
	}
}

// TestStitchDoneWithNoPRPrintsThePlanStuck is the fix round's owed (4):
// task done of a stitch with no PR ends it done, and the receipt is
// followed by the plan's PLAN stuck line with its remedy, as task cancel's
// is; a done with a PR (to merging) prints no PLAN line.
func TestStitchDoneWithNoPRPrintsThePlanStuck(t *testing.T) {
	t.Parallel()
	addr, c := wstest.Start(t)
	ctx := context.Background()
	run := func(args ...string) (int, string, string) {
		return runSprint(append(args, "--redis", addr)...)
	}
	for _, p := range []string{"sd", "sm"} {
		if _, err := taskcard.Push(ctx, c, taskcard.PushRequest{ID: p, Where: "waiting", Stream: "autonomy-" + p, Kind: "build",
			Ref: "mas-bandwidth/nova-tools#4317", Title: "done " + p, Repo: "mas-bandwidth/nova-tools", By: "rowan",
			Fields: []string{"base", "dev", "base_sha", cutFromSHA, "paths", "a.go", "done_when", "holds"}}); err != nil {
			t.Fatal(err)
		}
		tsv := filepath.Join(t.TempDir(), "children.tsv")
		if err := os.WriteFile(tsv, []byte("id\ttitle\tpaths\tdone-when\n"+p+"-c\tone\ta.go\tholds\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if code, out, errOut := run("card", "cut", "--parent", p, "--from", tsv, "--no-github", "--actor", "rowan"); code != 0 {
			t.Fatalf("cut exit %d:\n%s%s", code, out, errOut)
		}
		if _, err := taskcard.Move(ctx, c, p+"-stitch", "ready", taskcard.Opts{By: "rowan", Why: "test"}); err != nil {
			t.Fatal(err)
		}
		if _, err := taskcard.Take(ctx, c, "emma", 1, "emma", p+"-stitch"); err != nil {
			t.Fatal(err)
		}
	}
	code, out, errOut := run("task", "done", "--id", "sd-stitch", "--actor", "emma", "--evidence", "nothing to stitch")
	if code != 0 || !strings.Contains(out, "TASK done id=sd-stitch from=working to=done ms=") ||
		!strings.Contains(out, `PLAN id=sd state=stuck remedy="stuck: stitch sd-stitch ended done/`) ||
		!strings.Contains(out, `nova-sprint card cut --parent sd re-cuts the stitch (sd-stitch-2, DEPENDS-ON every child)"`) {
		t.Fatalf("task done of a stitch with no PR (exit %d):\n%s%s", code, out, errOut)
	}
	code, out, errOut = run("task", "done", "--id", "sm-stitch", "--actor", "emma", "--evidence", "stitched", "--pr", "91")
	if code != 0 || !strings.Contains(out, "TASK done id=sm-stitch from=working to=merging ms=") || strings.Contains(out, "PLAN ") {
		t.Fatalf("task done of a stitch with a PR (exit %d):\n%s%s", code, out, errOut)
	}
}
