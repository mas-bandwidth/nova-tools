package main

import (
	"strconv"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
)

// The control-35 fixture (#2756 v6 section 8, control 35; nova-tools #3106):
// a PR with CI OK at head, one 9 read by stella at head, a carried HOLD by
// emma at an older head, and emma out-of-credits for 61 minutes. The records
// are written in the shapes #3091 (PR hash, landable) and #3092 (hold and
// disposition records) must produce; see internal/nsprint/land/doc.go.
const (
	c35Sprint = "control-35"
	c35Head   = "4139b79f0a1b2c3d4e5f60718293a4b5c6d7e8f9"
	c35Old    = "99a88149aabbccddeeff00112233445566778899"
	c35Now    = int64(1790000000)
)

func control35(t *testing.T) *miniredis.Miniredis {
	t.Helper()
	mr := miniredis.RunT(t)
	pr := "s:" + c35Sprint + ":pr:nova-tools:3200"
	mr.HSet(pr, "head", c35Head, "base", "dev", "author", "rowan", "draft", "false",
		"mergeable", "MERGEABLE", "stack_parent", "none", "land_bar", "8",
		"reads_ok", "1", "holds_open", "1", "state", "reading",
		"latest_comment_id", "501", "latest_record_id", "1790000000000-0")
	mr.HSet("ci:nova-tools:"+c35Head, "verdict", "OK", "head", c35Head)
	mr.HSet("s:"+c35Sprint+":disp:nova-tools:3200",
		"stella@"+c35Head, "APPROVE 9 comment-501 501",
		"emma@"+c35Old, "HOLD 4 comment-400 400")
	mr.HSet("s:"+c35Sprint+":hold:nova-tools:3200", "h2",
		`{"holder":"emma","head":"`+c35Old+`","kind":"substance","reason":"scope","url":"comment-400","at":"1789990000"}`)
	mr.HSet("s:"+c35Sprint+":policy", "readers", "1", "absent_after", "60m")
	mr.HSet("friend:emma:state", "state", "out-of-credits", "since", strconv.FormatInt(c35Now-61*60, 10))
	mr.HSet("friend:stella:state", "state", "up", "since", strconv.FormatInt(c35Now-3600, 10))
	mr.SAdd("s:"+c35Sprint+":prs", "nova-tools#3200")
	return mr
}

func why(t *testing.T, mr *miniredis.Miniredis) string {
	t.Helper()
	code, stdout, stderr := runSprint("why", "nova-tools#3200", "--redis", mr.Addr(), "--sprint", c35Sprint, "--now", strconv.FormatInt(c35Now, 10))
	if code != 0 {
		t.Fatalf("why exit %d, stderr %s", code, stderr)
	}
	return stdout
}

func mustLine(t *testing.T, out, want string) {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		if line == want {
			return
		}
	}
	t.Fatalf("want the line %q in:\n%s", want, out)
}

func TestWhyControl35(t *testing.T) {
	mr := control35(t)
	out := why(t, mr)
	t.Logf("control 35 before release:\n%s", out)
	mustLine(t, out, "ci OK@4139b79f")
	mustLine(t, out, "reads 1/1 (stella 9 @4139b79f)")
	mustLine(t, out, "holds 1 open (emma HOLD h2 @99a88149, emma out-of-credits 61m, releasable by stella)")
	if strings.Contains(out, "landable #") {
		t.Fatalf("a PR with an open hold printed a landable position:\n%s", out)
	}

	// hold release --as stella (#3092 writes the release; #3091's ns_pr_eval
	// then moves the PR into landable in the same pass).
	mr.HSet("s:"+c35Sprint+":hold:nova-tools:3200", "h2",
		`{"holder":"emma","head":"`+c35Old+`","kind":"substance","reason":"scope","url":"u","at":"1789990000","released_by":"stella","release_kind":"reader-absent-holder","release_url":"comment-502","released_at":"1790000000"}`)
	mr.HSet("s:"+c35Sprint+":pr:nova-tools:3200", "holds_open", "0", "state", "landable")
	mr.ZAdd("s:"+c35Sprint+":landable", 10, "nova-tools#3200")
	out = why(t, mr)
	mustLine(t, out, "holds 0 open (h2 released by stella reader-absent-holder)")
	mustLine(t, out, "landable #1 of 1")
	t.Logf("control 35 after release:\n%s", out)
}

func TestWhyHoldAtHeadIsNeverReleasableByAReader(t *testing.T) {
	mr := control35(t)
	mr.HSet("s:"+c35Sprint+":hold:nova-tools:3200", "h2",
		`{"holder":"emma","head":"`+c35Head+`","kind":"substance","reason":"scope","url":"u","at":"1789990000"}`)
	out := why(t, mr)
	mustLine(t, out, "holds 1 open (emma HOLD h2 @4139b79f, emma out-of-credits 61m, releasable by emma only: hold at head)")
}

func TestWhyCarriedHoldByAPresentHolderWaitsForTheHolder(t *testing.T) {
	mr := control35(t)
	mr.HSet("friend:emma:state", "state", "out-of-credits", "since", strconv.FormatInt(c35Now-20*60, 10))
	out := why(t, mr)
	mustLine(t, out, "holds 1 open (emma HOLD h2 @99a88149, emma out-of-credits 20m, releasable by emma; stella after 40m)")
}

func TestWhyNamesAStaleEvaluation(t *testing.T) {
	mr := control35(t)
	// Every gate passes on the records, but the PR hash still says reading:
	// the answer names ns_pr_eval, not a person.
	mr.HDel("s:"+c35Sprint+":hold:nova-tools:3200", "h2")
	out := why(t, mr)
	mustLine(t, out, "holds 0 open")
	mustLine(t, out, "state reading: every gate passes; ns_pr_eval has not run since (not in landable)")
}

func TestWhyMissingIsNeverSuccess(t *testing.T) {
	mr := control35(t)
	mr.Del("ci:nova-tools:" + c35Head)
	mr.HDel("s:"+c35Sprint+":pr:nova-tools:3200", "draft")
	out := why(t, mr)
	mustLine(t, out, "ci MISSING@4139b79f")
	mustLine(t, out, "draft MISSING")
}

func TestWhyDropKeyAndStackParent(t *testing.T) {
	mr := control35(t)
	pr := "s:" + c35Sprint + ":pr:nova-tools:3200"
	mr.HSet(pr, "state", "dropped", "lane", "tools-20260923T132214Z", "drop_reason", "carried HOLD",
		"drop_key", c35Head+":501:1790000000000-0", "stack_parent", "#3011")
	mr.HSet("s:"+c35Sprint+":pr:nova-tools:3011", "state", "reading", "head", c35Old)
	out := why(t, mr)
	mustLine(t, out, "stack parent #3011 open (reading)")
	mustLine(t, out, "drop tools-20260923T132214Z: carried HOLD, key unchanged")
	mustLine(t, out, "state dropped")

	mr.HSet(pr, "latest_comment_id", "777")
	mr.HSet("s:"+c35Sprint+":pr:nova-tools:3011", "state", "landed", "merge_sha", c35Head)
	out = why(t, mr)
	mustLine(t, out, "stack parent #3011 merged @4139b79f")
	mustLine(t, out, "drop tools-20260923T132214Z: carried HOLD, key changed (re-evaluation due)")
}

func TestWhyRefusesAnUnknownPR(t *testing.T) {
	mr := control35(t)
	code, stdout, _ := runSprint("why", "nova-tools#9999", "--redis", mr.Addr(), "--sprint", c35Sprint)
	if code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if !strings.Contains(stdout, "MISSING s:control-35:pr:nova-tools:9999") {
		t.Fatalf("stdout %q, want the missing key named", stdout)
	}
	if code, _, _ := runSprint("why", "3200", "--redis", mr.Addr(), "--sprint", c35Sprint); code != 2 {
		t.Fatalf("a PR without a repo exit %d, want 2", code)
	}
}

func TestLandStatus(t *testing.T) {
	mr := control35(t)
	s := "s:" + c35Sprint + ":"
	mr.SAdd(s+"prs", "nova-tools#3201", "nova-tools#3202", "nova-tools#3203", "nova-tools#3204", "rocketnet#77")
	mr.HSet(s+"pr:nova-tools:3201", "state", "landing", "lane", "tools-a", "lane_at", strconv.FormatInt(c35Now-4*60, 10), "head", c35Head)
	mr.HSet(s+"pr:nova-tools:3202", "state", "landing", "lane", "tools-a", "lane_at", strconv.FormatInt(c35Now-4*60, 10), "head", c35Head)
	mr.HSet(s+"pr:nova-tools:3203", "state", "landable", "head", c35Head)
	mr.HSet(s+"pr:nova-tools:3204", "state", "dropped", "drop_reason", "UNKNOWN mergeable", "head", c35Head)
	mr.HSet(s+"pr:rocketnet:77", "state", "landed", "merge_sha", c35Head, "head", c35Head)
	mr.ZAdd(s+"landable", -1, "nova-tools#3203")
	code, stdout, stderr := runSprint("land", "status", "--redis", mr.Addr(), "--sprint", c35Sprint, "--now", strconv.FormatInt(c35Now, 10))
	if code != 0 {
		t.Fatalf("exit %d, stderr %s", code, stderr)
	}
	for _, want := range []string{
		"lanes 1 in flight: tools-a (2 PRs, 4m)",
		"landable 1, head nova-tools#3203",
		"dropped 1: UNKNOWN mergeable 1",
		"held 1: emma 1 (out-of-credits 61m)",
		"states: dropped 1, landable 1, landed 1, landing 2, reading 1",
	} {
		mustLine(t, stdout, want)
	}
}
