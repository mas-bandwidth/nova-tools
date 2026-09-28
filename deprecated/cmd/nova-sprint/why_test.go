package main

import (
	"strconv"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/mas-bandwidth/nova-tools/internal/civerdict"
)

// The control-35 fixture (#2756 v6 section 8, control 35; nova-tools #3106),
// on the unit keys of nova-tools#3139 rev 7 (2.2, build B14): a unit with CI
// OK at head, one 9 read by stella at head, a carried HOLD by emma at an
// older head, and emma out-of-credits for 61 minutes. The records are in the
// shapes land.lua and hold.lua write (internal/nsprint/land/doc.go); the PR
// resolves through s:<S>:prunit, and no s:<S>:pr:* key exists.
const (
	c35Sprint = "control-35"
	c35Unit   = "gh/mas-bandwidth/nova-tools/3200"
	c35Parent = "gh/mas-bandwidth/nova-tools/3011"
	c35Head   = "4139b79f0a1b2c3d4e5f60718293a4b5c6d7e8f9"
	c35Old    = "99a88149aabbccddeeff00112233445566778899"
	c35Now    = int64(1790000000)
	c35U      = "s:" + c35Sprint + ":u:" + c35Unit
	c35Hold   = "s:" + c35Sprint + ":hold:" + c35Unit + ":emma"
	c35PU     = "s:" + c35Sprint + ":u:" + c35Parent
)

func control35(t *testing.T) *miniredis.Miniredis {
	t.Helper()
	mr := miniredis.RunT(t)
	s := "s:" + c35Sprint + ":"
	mr.HSet(c35U, "repo", "nova-tools", "head", c35Head, "base", "dev", "base_sha", c35Head,
		"author", "rowan", "mergeable", "MERGEABLE", "stack_parent", "none", "pr", "3200",
		"holds_open", "1", "state", "reading", "seq", "1")
	mr.SAdd(s+"units", c35Unit)
	mr.Set(s+"prunit:nova-tools:3200", c35Unit)
	mr.SAdd("friends", "emma", "stella")
	mr.HSet(civerdict.PolicyKey("nova-tools", "dev"), "policy_id", "pol1", "required_set_id", "req1", "runner_id", "run1",
		"readers", "1", "absent_after", "60m")
	gid := civerdict.GID("single", "dev", c35Head, "req1", "pol1", "run1")
	mr.HSet(civerdict.Key("nova-tools", c35Head, gid), "verdict", "OK", "head", c35Head)
	mr.SAdd(civerdict.GIDsKey("nova-tools", c35Head), gid)
	mr.HSet(s+"read:"+c35Unit+":stella", "seq", "5", "head", c35Head, "verdict", "APPROVE", "score", "9", "kind", "substance")
	mr.HSet(s+"read:"+c35Unit+":emma", "seq", "2", "head", c35Old, "verdict", "HOLD", "score", "4", "kind", "substance")
	mr.HSet(c35Hold, "seq", "2", "head", c35Old, "kind", "substance", "reason", "scope", "url", "comment-400", "post_land", "0", "at", "1789990000000")
	mr.HSet("friend:emma:state", "state", "out-of-credits", "since", strconv.FormatInt(c35Now-61*60, 10))
	mr.HSet("friend:stella:state", "state", "up", "since", strconv.FormatInt(c35Now-3600, 10))
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
	t.Parallel()

	mr := control35(t)
	out := why(t, mr)
	t.Logf("control 35 before release:\n%s", out)
	mustLine(t, out, "unit "+c35Unit+" nova-tools#3200")
	mustLine(t, out, "ci OK@4139b79f")
	mustLine(t, out, "reads 1/1 (stella 9 @4139b79f)")
	mustLine(t, out, "holds 1 open (emma HOLD h2 @99a88149, emma out-of-credits 61m, releasable by stella)")
	if strings.Contains(out, "landable #") {
		t.Fatalf("a unit with an open hold printed a landable position:\n%s", out)
	}

	// hold release --as stella (ns_release writes the release; ns_unit_eval
	// then moves the unit into landable).
	mr.HSet(c35Hold, "released_by", "stella", "release_kind", "reader-absent-holder", "release_url", "comment-502",
		"released_at", "1790000000000", "release_seq", "6")
	mr.HSet(c35U, "holds_open", "0", "state", "landable")
	mr.ZAdd("s:"+c35Sprint+":landable:nova-tools:dev", 10, c35Unit)
	out = why(t, mr)
	mustLine(t, out, "holds 0 open (h2 released by stella reader-absent-holder)")
	mustLine(t, out, "landable #1 of 1")
	t.Logf("control 35 after release:\n%s", out)
}

func TestWhyHoldAtHeadIsNeverReleasableByAReader(t *testing.T) {
	t.Parallel()

	mr := control35(t)
	mr.HSet(c35Hold, "head", c35Head)
	out := why(t, mr)
	mustLine(t, out, "holds 1 open (emma HOLD h2 @4139b79f, emma out-of-credits 61m, releasable by emma only: hold at head)")
}

func TestWhyCarriedHoldByAPresentHolderWaitsForTheHolder(t *testing.T) {
	t.Parallel()

	mr := control35(t)
	mr.HSet("friend:emma:state", "state", "out-of-credits", "since", strconv.FormatInt(c35Now-20*60, 10))
	out := why(t, mr)
	mustLine(t, out, "holds 1 open (emma HOLD h2 @99a88149, emma out-of-credits 20m, releasable by emma; stella after 40m)")
}

func TestWhyNamesAStaleEvaluation(t *testing.T) {
	t.Parallel()

	mr := control35(t)
	// Every condition passes on the records, but the unit still says
	// reading: the answer names ns_unit_eval, not a person.
	mr.Del(c35Hold)
	mr.HSet(c35U, "holds_open", "0")
	out := why(t, mr)
	mustLine(t, out, "holds 0 open")
	mustLine(t, out, "state reading: every gate passes; ns_unit_eval has not run since (not in landable)")
}

// A hold counted on the unit but keyed outside the friends set (an inbound
// login hold, 3.4) is never a pass: holds_open is the count.
func TestWhyCountsHoldsOpenBeyondTheListedHolds(t *testing.T) {
	t.Parallel()

	mr := control35(t)
	mr.Del(c35Hold)
	out := why(t, mr)
	mustLine(t, out, "holds 0 open; holds_open=1 on "+c35U)
	if strings.Contains(out, "every gate passes") {
		t.Fatalf("holds_open=1 passed:\n%s", out)
	}
}

func TestWhyMissingIsNeverSuccess(t *testing.T) {
	t.Parallel()

	mr := control35(t)
	gid := civerdict.GID("single", "dev", c35Head, "req1", "pol1", "run1")
	mr.Del(civerdict.Key("nova-tools", c35Head, gid))
	mr.Del(civerdict.GIDsKey("nova-tools", c35Head))
	mr.HDel(c35U, "mergeable")
	out := why(t, mr)
	mustLine(t, out, "ci MISSING@4139b79f")
	mustLine(t, out, "mergeable MISSING")

	// No policy record for the base: ci nopolicy (3.3 (1)), never a pass.
	mr.Del(civerdict.PolicyKey("nova-tools", "dev"))
	out = why(t, mr)
	mustLine(t, out, "ci nopolicy@4139b79f")
}

func TestWhyDropKeyAndStackParent(t *testing.T) {
	t.Parallel()

	mr := control35(t)
	mr.HSet(c35U, "state", "dropped", "drop_reason", "carried HOLD", "drop_key", "4139b79f:9", "stack_parent", c35Parent)
	mr.HSet(c35PU, "state", "reading", "head", c35Old)
	out := why(t, mr)
	mustLine(t, out, "stack parent "+c35Parent+" open (reading)")
	mustLine(t, out, "drop carried HOLD, key 4139b79f:9 unchanged")
	mustLine(t, out, "state dropped")

	// A read record after the drop's rec_seq changes the key's inputs.
	mr.HSet("s:"+c35Sprint+":read:"+c35Unit+":stella", "seq", "12")
	mr.HSet(c35PU, "state", "landed", "merge_sha", c35Head)
	out = why(t, mr)
	mustLine(t, out, "stack parent "+c35Parent+" merged @4139b79f")
	mustLine(t, out, "drop carried HOLD, key 4139b79f:9 changed (re-evaluation due)")
}

// Negative control (stella's review at 9444edf4): a parent record with
// state=landed but no merge_sha is incomplete; the stack-parent condition
// fails and the state line never claims every gate passes.
func TestWhyLandedParentWithoutMergeSHAIsNeverAPass(t *testing.T) {
	t.Parallel()

	mr := control35(t)
	mr.Del(c35Hold)
	mr.HSet(c35U, "holds_open", "0", "stack_parent", c35Parent)
	mr.HSet(c35PU, "state", "landed", "head", c35Head)
	out := why(t, mr)
	mustLine(t, out, "stack parent "+c35Parent+" landed, merge_sha MISSING "+c35PU)
	mustLine(t, out, "state reading")
	if strings.Contains(out, "every gate passes") || strings.Contains(out, "merged @") {
		t.Fatalf("a landed parent without merge_sha passed the stack-parent condition:\n%s", out)
	}

	// Positive control: the same record with its merge_sha passes.
	mr.HSet(c35PU, "merge_sha", c35Head)
	out = why(t, mr)
	mustLine(t, out, "stack parent "+c35Parent+" merged @4139b79f")
	mustLine(t, out, "state reading: every gate passes; ns_unit_eval has not run since (not in landable)")
}

func TestWhyRefusesAnUnknownPR(t *testing.T) {
	t.Parallel()

	mr := control35(t)
	code, stdout, _ := runSprint("why", "nova-tools#9999", "--redis", mr.Addr(), "--sprint", c35Sprint)
	if code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if !strings.Contains(stdout, "MISSING s:control-35:prunit:nova-tools:9999") {
		t.Fatalf("stdout %q, want the missing key named", stdout)
	}
	if code, _, _ := runSprint("why", "3200", "--redis", mr.Addr(), "--sprint", c35Sprint); code != 2 {
		t.Fatalf("a PR without a repo exit %d, want 2", code)
	}
}

func TestLandStatus(t *testing.T) {
	t.Parallel()

	mr := control35(t)
	s := "s:" + c35Sprint + ":"
	u := func(n string) string { return "gh/mas-bandwidth/nova-tools/" + n }
	rn := "gh/mas-bandwidth/rocketnet/77"
	mr.SAdd(s+"units", u("3201"), u("3202"), u("3203"), u("3204"), rn)
	for _, n := range []string{"3201", "3202"} {
		mr.HSet(s+"u:"+u(n), "repo", "nova-tools", "base", "dev", "state", "batched", "batch", "b1", "head", c35Head)
	}
	mr.HSet(s+"u:"+u("3203"), "repo", "nova-tools", "base", "dev", "state", "landable", "head", c35Head)
	mr.HSet(s+"u:"+u("3204"), "repo", "nova-tools", "base", "dev", "state", "dropped", "drop_reason", "CONFLICT", "head", c35Head)
	mr.HSet(s+"u:"+rn, "repo", "rocketnet", "base", "main", "state", "landed", "merge_sha", c35Head, "head", c35Head,
		"merged_at", strconv.FormatInt(c35Now-10*60, 10), "last_read_at", strconv.FormatInt(c35Now-40*60, 10))
	mr.ZAdd(s+"landable:nova-tools:dev", -1, u("3203"))
	mr.ZAdd("land:nova-tools:dev:chain", 1, "b1")
	mr.HSet("land:nova-tools:dev:batch:b1", "state", "gating", "attempt", "1", "bench", "bench-1", "slot", "0",
		"members", u("3201")+"@"+c35Head+","+u("3202")+"@"+c35Head, "claimed_at", strconv.FormatInt((c35Now-4*60)*1000, 10))
	mr.Set("worker:bench-1:0", "b1:1:7")
	code, stdout, stderr := runSprint("land", "status", "--redis", mr.Addr(), "--sprint", c35Sprint, "--now", strconv.FormatInt(c35Now, 10))
	if code != 0 {
		t.Fatalf("exit %d, stderr %s", code, stderr)
	}
	t.Logf("land status:\n%s", stdout)
	for _, want := range []string{
		"units 6: batched 2, dropped 1, landable 1, landed 1, reading 1",
		"chain nova-tools/dev 1: b1 gating attempt 1 units " + u("3201") + "@4139b79f," + u("3202") + "@4139b79f",
		"chain rocketnet/main 0",
		"gates 1: bench-1/0 b1 attempt 1 4m worker live",
		"landable nova-tools/dev 1, head " + u("3203"),
		"landable rocketnet/main 0",
		"dropped 1: CONFLICT 1",
		"waiting 1: hold 1",
		"freezes 0",
		"landed 1 in the last hour, p50 read to landed 30m",
	} {
		mustLine(t, stdout, want)
	}
}

func TestLandVerbDispatch(t *testing.T) {
	t.Parallel()

	if code, _, stderr := runSprint("land", "nope"); code != 2 || !strings.Contains(stderr, "want status or flaky") {
		t.Fatalf("land nope: exit=%d stderr=%q", code, stderr)
	}
	if code, _, stderr := runSprint("land", "flaky", "nope"); code != 2 || !strings.Contains(stderr, "want list or observe") {
		t.Fatalf("land flaky nope: exit=%d stderr=%q", code, stderr)
	}
	if code, _, _ := runSprint("land", "flaky", "observe"); code != 2 {
		t.Fatalf("observe without flags exit=%d, want 2", code)
	}
	if code, _, _ := runSprint("land", "flaky", "observe", "--redis", "127.0.0.1:1", "--sprint", "s", "--repo", "a:b", "--pkg", "p", "--test", "T", "--lane", "l"); code != 2 {
		t.Fatalf("observe with colon repo exit=%d, want 2", code)
	}
}
