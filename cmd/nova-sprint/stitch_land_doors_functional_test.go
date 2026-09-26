//go:build functional

package main

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/land/stream"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/read"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws/wstest"
)

// The plan's issue every door must name when its stitch lands it (the
// origin on the reserved .test forge: a fixture, never a live host).
const (
	doorPlanRef    = "mas-bandwidth/nova-tools#4317"
	doorPlanOrigin = "https://forge.test/mas-bandwidth/nova-tools/issues/4317"
)

// The door that writes the stitch's PR: task done --pr (ns_tcard_done) or
// a copy's card end --ok --pr (ns_cm_end).
const (
	doorDone = "task done --pr"
	doorEnd  = "card end --ok --pr"
)

// doorPlan cuts plan p (one child, a stitch) on stream s through the verb,
// lands the child, records PR n at head (closes none), and ends the stitch
// on PR n through door, on the CLI with the store on --redis: by task done
// --pr the stitch is merging; by a copy's card end --ok --pr it is in
// review. Nothing indexes the stitch by hand (no ns_task_refs): the door
// that wrote its pr indexed it, or a CLOSE line on n does not find it.
func doorPlan(t *testing.T, addr string, c *redis.Client, p, s string, n int, head, door string) {
	t.Helper()
	ctx := context.Background()
	if _, err := taskcard.Push(ctx, c, taskcard.PushRequest{ID: p, Where: "waiting", Stream: s, Kind: "build",
		Ref: doorPlanRef, Origin: doorPlanOrigin, Title: "door " + p, Repo: lsRepo, By: "rowan",
		Fields: []string{"base", "dev", "base_sha", cutFromSHA, "paths", "a.go", "done_when", "holds"}}); err != nil {
		t.Fatal(err)
	}
	tsv := filepath.Join(t.TempDir(), "children.tsv")
	if err := os.WriteFile(tsv, []byte("id\ttitle\tpaths\tdone-when\n"+p+"-c\tone\ta.go\tholds\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, out, errOut := runSprint("card", "cut", "--parent", p, "--from", tsv, "--no-github", "--actor", "rowan", "--redis", addr); code != 0 {
		t.Fatalf("cut %s exit %d:\n%s%s", p, code, out, errOut)
	}
	if _, err := taskcard.Move(ctx, c, p+"-c", "ready", taskcard.Opts{By: "rowan", Why: "test"}); err != nil {
		t.Fatal(err)
	}
	if _, err := taskcard.Take(ctx, c, "emma", 1, "emma", p+"-c"); err != nil {
		t.Fatal(err)
	}
	if _, err := taskcard.Done(ctx, c, p+"-c", "emma", "DONE", strconv.Itoa(n-1)); err != nil {
		t.Fatal(err)
	}
	if _, err := taskcard.LandStream(ctx, c, s, strings.Repeat("1", 40), "rowan", ""); err != nil {
		t.Fatal(err)
	}
	st := p + "-stitch"
	if _, err := taskcard.Move(ctx, c, st, "ready", taskcard.Opts{By: "rowan", Why: "test"}); err != nil {
		t.Fatalf("%s ready: %v", st, err)
	}
	if code, out, errOut := runSprint("pr", "record", "--redis", addr, "--repo", lsRepo, "--n", strconv.Itoa(n),
		"--head", head, "--base", "dev", "--stream", s, "--task", st, "--closes", "-"); code != 0 {
		t.Fatalf("pr record: %d %s %s", code, out, errOut)
	}
	want := "merging"
	switch door {
	case doorDone:
		if _, err := taskcard.Take(ctx, c, "emma", 1, "emma", st); err != nil {
			t.Fatalf("take %s: %v", st, err)
		}
		if code, out, errOut := runSprint("task", "done", "--id", st, "--actor", "emma", "--evidence", "stitched",
			"--pr", strconv.Itoa(n), "--redis", addr); code != 0 || !strings.Contains(out, "TASK done id="+st+" from=working to=merging ms=") {
			t.Fatalf("task done --pr (exit %d):\n%s%s", code, out, errOut)
		}
	case doorEnd:
		want = "review"
		c.HSet(ctx, "bench:b:desired", "slots", "1", "tiers", "frontier,pro,flash")
		run := func(args ...string) string {
			t.Helper()
			code, out, errOut := runSprint(append(args, "--redis", addr)...)
			if code != 0 {
				t.Fatalf("%v (exit %d):\n%s%s", args, code, out, errOut)
			}
			return out
		}
		run("card", "deal", "--to", "bench:b", "--ids", st, "--actor", "rowan")
		run("card", "work", "--as", "bench:b", "--fill")
		if out := run("card", "end", "--id", st+"~1", "--ok", "--pr", "nova-tools#"+strconv.Itoa(n), "--head", head,
			"--line1", "RESULT: "+st, "--base", "dev", "--base-sha", cutFromSHA, "--paths", "a.go"); !strings.Contains(out, "ENDED "+st+"~1 primary="+st+" from=working to=review ") {
			t.Fatalf("card end --ok --pr:\n%s", out)
		}
	default:
		t.Fatalf("door %q", door)
	}
	if w := c.HGet(ctx, taskcard.Key(st), "where").Val(); w != want {
		t.Fatalf("%s is %s, want %s", st, w, want)
	}
}

// TestLandMergeNamesThePlanItsStitchLanded is the fix round's owed (3), the
// stream landing's door: land merge of a landing whose member is a plan's
// stitch lands the plan in ns_land_member's call, and the receipt names it,
// LAND MERGE PLAN parent=<plan> ref=<repo#n> origin=<url> (the member note
// PLAN ..., never counted as a skip). Parallel: the fake forge's token goes
// through land merge's own token parameter (landMerge), never a swap.
func TestLandMergeNamesThePlanItsStitchLanded(t *testing.T) {
	t.Parallel()
	addr, c := wstest.Start(t)
	ctx := context.Background()
	gh := newFakeGitHub(t)
	token := func() (string, error) { return "test-token", nil }
	landMergeCLI := func(args ...string) (int, string, string) {
		var out, errOut strings.Builder
		code := landMerge(ctx, args, &out, &errOut, token)
		return code, out.String(), errOut.String()
	}
	const s = "autonomy-lm"
	head := strings.Repeat("a", 40)
	doorPlan(t, addr, c, "lm", s, 82, head, doorDone)
	slug, err := stream.Slug(s)
	if err != nil {
		t.Fatal(err)
	}
	l := stream.Landing{Repo: lsRepo, Slug: slug, Streams: s, Base: "dev", BaseSHA: strings.Repeat("b", 40),
		Branch: "stream/" + slug, Head: strings.Repeat("c", 40), PR: 900, State: "open",
		Members: []stream.Member{{Task: "lm-stitch", Stream: s, N: 82, Head: head}}}
	if _, err := stream.SaveBuilt(ctx, c, l, "rowan"); err != nil {
		t.Fatal(err)
	}
	if _, err := stream.SaveLanded(ctx, c, l, "rowan", strings.Repeat("d", 40)); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := landMergeCLI("--redis", addr, "--repo", lsRepo, "--stream", s, "--api", gh.srv.URL)
	if code != 0 || !strings.Contains(out, " members=1 moved=1 missing=0 already=true ") || !strings.Contains(out, " skipped=0 ") ||
		!strings.Contains(out, "\nLAND MERGE PLAN parent=lm ref="+doorPlanRef+" origin="+doorPlanOrigin+"\n") {
		t.Fatalf("land merge (exit %d):\n%s%s", code, out, errOut)
	}
	if w := c.HGet(ctx, taskcard.Key("lm"), "where").Val(); w != "landed" {
		t.Fatalf("the plan is %s, want landed with its stitch", w)
	}
	// A re-run lands nothing twice and names no plan again.
	if code, out, _ = landMergeCLI("--redis", addr, "--repo", lsRepo, "--stream", s, "--api", gh.srv.URL); code != 0 || strings.Contains(out, "LAND MERGE PLAN") {
		t.Fatalf("re-run (exit %d):\n%s", code, out)
	}
}

// TestCloseLineNamesThePlanItsStitchLanded is the fix round's owed (3), the
// CLOSE-line door: a person's CLOSE line on the stitch's PR, through line
// post and through read post, lands the stitch and its plan in the one
// call, and each receipt names the plan with its ref and origin.
func TestCloseLineNamesThePlanItsStitchLanded(t *testing.T) {
	t.Parallel()
	addr, c := wstest.Start(t)
	ctx := context.Background()
	head := strings.Repeat("a", 40)
	doorPlan(t, addr, c, "cl", "autonomy-cl", 84, head, doorDone)
	close := func(n string) string {
		return "CLOSE who=rowan head=" + head[:8] + " landed: in dev at " + head[:8] + " with nova-tools#" + n + " (" + strings.Repeat("e", 8) + ")"
	}
	code, out, errOut := runSprint("line", "post", "--redis", addr, "--repo", "nova-tools", "--n", "84", "--line", close("84"))
	if code != 0 || !strings.Contains(out, "\nLINE POST PLAN parent=cl ref="+doorPlanRef+" origin="+doorPlanOrigin+"\n") {
		t.Fatalf("line post CLOSE (exit %d):\n%s%s", code, out, errOut)
	}
	if w := c.HGet(ctx, taskcard.Key("cl"), "where").Val(); w != "landed" {
		t.Fatalf("the plan is %s after the CLOSE, want landed", w)
	}
	head2 := strings.Repeat("f", 40)
	doorPlan(t, addr, c, "rc", "autonomy-rc", 86, head2, doorDone)
	var stdout, stderr strings.Builder
	line := "CLOSE who=rowan head=" + head2[:8] + " landed: in dev at " + head2[:8] + " with nova-tools#86 (" + strings.Repeat("e", 8) + ")"
	if code := read.Post(ctx, c, "nova-tools", "86", line, nil, &stdout, &stderr); code != 0 ||
		!strings.Contains(stdout.String(), "\nREAD POST PLAN repo=nova-tools n=86 parent=rc ref="+doorPlanRef+" origin="+doorPlanOrigin+"\n") ||
		strings.Contains(stderr.String(), "SKIPPED") {
		t.Fatalf("read post CLOSE (exit %d):\n%s%s", code, stdout.String(), stderr.String())
	}
	if w := c.HGet(ctx, taskcard.Key("rc"), "where").Val(); w != "landed" {
		t.Fatalf("the plan is %s after the read post CLOSE, want landed", w)
	}
}

// personClose is a person's CLOSE line on PR n at head.
func personClose(head string, n int) string {
	return "CLOSE who=rowan head=" + head[:8] + " landed: in dev at " + head[:8] + " with nova-tools#" + strconv.Itoa(n) + " (" + strings.Repeat("e", 8) + ")"
}

// readPostClose posts personClose through the read post verb (Redis only,
// the store on --redis), as the cold reader did.
func readPostClose(t *testing.T, addr, head string, n int) (int, string, string) {
	t.Helper()
	return runSprint("read", "post", "--repo", "nova-tools", "--n", strconv.Itoa(n), "--line", personClose(head, n), "--no-github", "--redis", addr)
}

// TestCloseAfterTaskDonePRLandsThePlan is PROBE (a), the cold reader's
// failing sequence: task done --pr 88 on a stitch, then a CLOSE line on 88
// through read post, with no hand ns_task_refs. ns_tcard_done indexed the
// stitch under nova-tools#88 when it wrote pr (TK.move, TK.writes_pr), so
// the CLOSE lands the stitch and its plan and the receipt names the plan.
func TestCloseAfterTaskDonePRLandsThePlan(t *testing.T) {
	t.Parallel()
	addr, c := wstest.Start(t)
	ctx := context.Background()
	head := strings.Repeat("a", 40)
	doorPlan(t, addr, c, "dd", "autonomy-dd", 88, head, doorDone)
	if !c.SIsMember(ctx, "ref:nova-tools#88:tasks", "dd-stitch").Val() {
		t.Fatalf("task done --pr 88 left dd-stitch out of ref:nova-tools#88:tasks: %v", c.SMembers(ctx, "ref:nova-tools#88:tasks").Val())
	}
	code, out, errOut := readPostClose(t, addr, head, 88)
	if code != 0 || !strings.Contains(out, " tasks_moved=1 ") || errOut != "" ||
		!strings.Contains(out, "\nREAD POST PLAN repo=nova-tools n=88 parent=dd ref="+doorPlanRef+" origin="+doorPlanOrigin+"\n") {
		t.Fatalf("CLOSE on 88 after task done --pr 88 (exit %d):\n%s%s", code, out, errOut)
	}
	for id, want := range map[string]string{"dd-stitch": "landed", "dd": "landed"} {
		if w := c.HGet(ctx, taskcard.Key(id), "where").Val(); w != want {
			t.Fatalf("%s is %s after the CLOSE, want %s", id, w, want)
		}
	}
}

// TestCloseAfterCardEndPRLandsThePlan is PROBE (b): the same through a
// copy of the stitch, card deal, card work, then card end --ok --pr
// nova-tools#90 (ns_cm_end: the stitch goes to review with its read
// copies), then a CLOSE line on 90: the stitch lands from review (its read
// copies retire), the plan lands with it and the receipt names the plan.
func TestCloseAfterCardEndPRLandsThePlan(t *testing.T) {
	t.Parallel()
	addr, c := wstest.Start(t)
	ctx := context.Background()
	head := strings.Repeat("b", 40)
	doorPlan(t, addr, c, "de", "autonomy-de", 90, head, doorEnd)
	if !c.SIsMember(ctx, "ref:nova-tools#90:tasks", "de-stitch").Val() {
		t.Fatalf("card end --ok --pr 90 left de-stitch out of ref:nova-tools#90:tasks: %v", c.SMembers(ctx, "ref:nova-tools#90:tasks").Val())
	}
	code, out, errOut := readPostClose(t, addr, head, 90)
	if code != 0 || !strings.Contains(out, " tasks_moved=1 ") || errOut != "" ||
		!strings.Contains(out, "\nREAD POST PLAN repo=nova-tools n=90 parent=de ref="+doorPlanRef+" origin="+doorPlanOrigin+"\n") {
		t.Fatalf("CLOSE on 90 after card end --ok --pr 90 (exit %d):\n%s%s", code, out, errOut)
	}
	rec := c.HMGet(ctx, taskcard.Key("de-stitch"), "where", "reads").Val()
	if rec[0] != "landed" || (rec[1] != nil && rec[1] != "") {
		t.Fatalf("de-stitch after the CLOSE: where=%v reads=%v, want landed with no open read copy", rec[0], rec[1])
	}
	if w := c.HGet(ctx, taskcard.Key("de"), "where").Val(); w != "landed" {
		t.Fatalf("the plan is %s after the CLOSE, want landed", w)
	}
}

// TestCloseForAPRNoTaskCarriesMovesNothing is PROBE (c): a CLOSE line on a
// PR no task names (its record exists, closes none) moves no task, prints
// no plan and no skip, and exits 0; a plan whose stitch is merging on
// another PR is left merging.
func TestCloseForAPRNoTaskCarriesMovesNothing(t *testing.T) {
	t.Parallel()
	addr, c := wstest.Start(t)
	ctx := context.Background()
	head := strings.Repeat("c", 40)
	doorPlan(t, addr, c, "dn", "autonomy-dn", 94, head, doorDone)
	if code, out, errOut := runSprint("pr", "record", "--redis", addr, "--repo", lsRepo, "--n", "92",
		"--head", head, "--base", "dev", "--stream", "autonomy-dn", "--closes", "-"); code != 0 {
		t.Fatalf("pr record 92: %d %s %s", code, out, errOut)
	}
	code, out, errOut := readPostClose(t, addr, head, 92)
	if code != 0 || !strings.Contains(out, "READ POST repo=nova-tools n=92 kind=CLOSE ") || !strings.Contains(out, " tasks_moved=0 ") ||
		strings.Contains(out, "PLAN") || errOut != "" {
		t.Fatalf("CLOSE on 92 (exit %d):\n%s%s", code, out, errOut)
	}
	for id, want := range map[string]string{"dn-stitch": "merging", "dn": "waiting"} {
		if w := c.HGet(ctx, taskcard.Key(id), "where").Val(); w != want {
			t.Fatalf("%s is %s after a CLOSE on another PR, want %s", id, w, want)
		}
	}
}

// TestEveryPRWriterIndexesTheRef is the DOORS gate: every writer of a
// task's pr field indexes the task under its PR's ref in the same call
// (TK.writes_pr in TK.move and TK.create): a push with pr, task move with a
// pr field, task done --pr (above), a copy's end with a PR (above); a pr
// rewritten moves the index with it, so a CLOSE on the old PR finds nothing.
func TestEveryPRWriterIndexesTheRef(t *testing.T) {
	t.Parallel()
	_, c := wstest.Start(t)
	ctx := context.Background()
	in := func(n, id string) bool { return c.SIsMember(ctx, "ref:nova-tools#"+n+":tasks", id).Val() }
	if _, err := taskcard.Push(ctx, c, taskcard.PushRequest{ID: "wp", Where: "waiting", Stream: "autonomy-wp", Kind: "build",
		Title: "push with pr", Repo: lsRepo, PR: "70", By: "rowan"}); err != nil {
		t.Fatal(err)
	}
	if !in("70", "wp") {
		t.Fatal("a push with pr 70 is not in ref:nova-tools#70:tasks")
	}
	if _, err := taskcard.Push(ctx, c, taskcard.PushRequest{ID: "wm", Where: "waiting", Stream: "autonomy-wp", Kind: "build",
		Title: "move with pr", Repo: lsRepo, By: "rowan"}); err != nil {
		t.Fatal(err)
	}
	if _, err := taskcard.Move(ctx, c, "wm", "ready", taskcard.Opts{By: "rowan", Why: "test", Fields: []string{"pr", "71"}}); err != nil {
		t.Fatal(err)
	}
	if !in("71", "wm") {
		t.Fatal("task move with pr 71 is not in ref:nova-tools#71:tasks")
	}
	// the same place, a new pr: the index follows (TK.move's same path)
	if _, err := taskcard.Move(ctx, c, "wm", "ready", taskcard.Opts{By: "rowan", Why: "test", Fields: []string{"pr", "72"}}); err != nil {
		t.Fatal(err)
	}
	if in("71", "wm") || !in("72", "wm") {
		t.Fatalf("pr 71 -> 72: in 71 %t, in 72 %t; want out of 71, in 72", in("71", "wm"), in("72", "wm"))
	}
}
