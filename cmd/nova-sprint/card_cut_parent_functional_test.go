//go:build functional

package main

import (
	"context"
	"os"
	"path/filepath"
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
	code, out, errOut := run("card", "cut", "--parent", "hier", "--from", tsv, "--no-github")
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
	code, out, _ = run("card", "cut", "--parent", "hier", "--from", tsv, "--no-github")
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
	code, out, _ = run("card", "cut", "--parent", "hier", "--from", more, "--no-github")
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
	if code, out, _ := run("card", "stitch", "--ids", "hier"); code != 0 || !strings.Contains(out, "stuck: the-docs ended done/fail and the stitch waits on it; nova-sprint card stitch --drop the-docs drops it from the plan") {
		t.Fatalf("the stuck remedy (exit %d):\n%s", code, out)
	}
	if code, out, _ := run("card", "stitch", "--drop", "the-model"); code != 1 || !strings.Contains(out, `REFUSED card stitch drop=the-model why=task:the-model\x20is\x20waiting,\x20not\x20done:`) {
		t.Fatalf("drop of a live child (exit %d):\n%s", code, out)
	}
	if code, out, _ := run("card", "stitch", "--drop", "the-docs"); code != 0 || !strings.Contains(out, "STITCH DROP parent=hier child=the-docs children=2 state=waiting ms=") {
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
		code, out, _ = run("card", "stitch", "--ids", id)
		if code != 0 || !strings.HasPrefix(out, "plan hier waiting children=2") || !strings.Contains(out, "\n"+taskcard.BriefMarker) ||
			!strings.Contains(out, "\nPLAN id=hier state=waiting children=2 stitch=hier-stitch:waiting written=0 ms=") {
			t.Fatalf("card stitch --ids %s exit %d:\n%s", id, code, out)
		}
	}
	if code, out, _ := run("card", "stitch", "--ids", "the-model"); code != 1 || !strings.Contains(out, `REFUSED card stitch id=the-model why=task:the-model\x20is\x20not\x20a\x20plan remedy=`) {
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
	code, out, _ = run("card", "stitch", "--ids", "hier", "--write")
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
	if code, out, _ := run("task", "land", "--ids", "hier", "--sha", sha(3)); code != 0 || !strings.Contains(out, "from=landed to=landed") {
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
