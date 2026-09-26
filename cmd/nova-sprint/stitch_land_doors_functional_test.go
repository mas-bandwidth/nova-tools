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

// The plan's issue every door must name when its stitch lands it.
const (
	doorPlanRef    = "mas-bandwidth/nova-tools#4317"
	doorPlanOrigin = "https://github.com/mas-bandwidth/nova-tools/issues/4317"
)

// doorPlan cuts plan p (one child, a stitch) on stream s through the verb,
// lands the child, and walks the stitch to merging on PR n with a pr record
// at head (closes none): the stitch waits for a door to land it.
func doorPlan(t *testing.T, addr string, c *redis.Client, p, s string, n int, head string) {
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
	walk(p+"-c", strconv.Itoa(n-1))
	if _, err := taskcard.LandStream(ctx, c, s, strings.Repeat("1", 40), "rowan", ""); err != nil {
		t.Fatal(err)
	}
	walk(p+"-stitch", strconv.Itoa(n))
	if code, out, errOut := runSprint("pr", "record", "--redis", addr, "--repo", lsRepo, "--n", strconv.Itoa(n),
		"--head", head, "--base", "dev", "--stream", s, "--task", p+"-stitch", "--closes", "-"); code != 0 {
		t.Fatalf("pr record: %d %s %s", code, out, errOut)
	}
	// The stitch is indexed under its PR's ref as the harvest indexes a
	// card whose PR opened (ns_task_refs): task done --pr writes the pr
	// field but indexes nothing (ns_tcard_done), so a CLOSE line finds it
	// only through the index.
	if err := c.FCall(ctx, "ns_task_refs", nil, p+"-stitch").Err(); err != nil {
		t.Fatal(err)
	}
	if w := c.HGet(ctx, taskcard.Key(p+"-stitch"), "where").Val(); w != "merging" {
		t.Fatalf("%s-stitch is %s, want merging", p, w)
	}
}

// TestLandMergeNamesThePlanItsStitchLanded is the fix round's owed (3), the
// stream landing's door: land merge of a landing whose member is a plan's
// stitch lands the plan in ns_land_member's call, and the receipt names it,
// LAND MERGE PLAN parent=<plan> ref=<repo#n> origin=<url> (the member note
// PLAN ..., never counted as a skip). Not parallel: it swaps the token
// seam for the fake forge's.
func TestLandMergeNamesThePlanItsStitchLanded(t *testing.T) {
	addr, c := wstest.Start(t)
	ctx := context.Background()
	gh := newFakeGitHub(t)
	prev := landStreamToken
	landStreamToken = func() (string, error) { return "test-token", nil }
	t.Cleanup(func() { landStreamToken = prev })
	const s = "autonomy-lm"
	head := strings.Repeat("a", 40)
	doorPlan(t, addr, c, "lm", s, 82, head)
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
	code, out, errOut := runSprint("land", "merge", "--redis", addr, "--repo", lsRepo, "--stream", s, "--api", gh.srv.URL)
	if code != 0 || !strings.Contains(out, " members=1 moved=1 missing=0 already=true ") || !strings.Contains(out, " skipped=0 ") ||
		!strings.Contains(out, "\nLAND MERGE PLAN parent=lm ref="+doorPlanRef+" origin="+doorPlanOrigin+"\n") {
		t.Fatalf("land merge (exit %d):\n%s%s", code, out, errOut)
	}
	if w := c.HGet(ctx, taskcard.Key("lm"), "where").Val(); w != "landed" {
		t.Fatalf("the plan is %s, want landed with its stitch", w)
	}
	// A re-run lands nothing twice and names no plan again.
	if code, out, _ = runSprint("land", "merge", "--redis", addr, "--repo", lsRepo, "--stream", s, "--api", gh.srv.URL); code != 0 || strings.Contains(out, "LAND MERGE PLAN") {
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
	doorPlan(t, addr, c, "cl", "autonomy-cl", 84, head)
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
	doorPlan(t, addr, c, "rc", "autonomy-rc", 86, head2)
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
