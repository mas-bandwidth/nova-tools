package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestCardMoveDispatch: the table moves own deal, work, land, cancel,
// expire, table, consumers and render; end and beat are theirs with --id,
// --ids or --as and the bench attempt form's with a label and --token; fsck
// is theirs with no --sprint.
func TestCardMoveDispatch(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		args []string
		want bool
	}{
		{[]string{"deal", "--to", "bench:b", "--n", "3"}, true},
		{[]string{"work", "--as", "friend:f", "--fill"}, true},
		{[]string{"end", "--id", "p~1", "--ok"}, true},
		{[]string{"end", "lbl", "--sprint", "s", "--token", "t"}, false},
		{[]string{"beat", "--as", "bench:b", "--id", "p~1"}, true},
		{[]string{"beat", "lbl", "--sprint", "s", "--token", "t"}, false},
		{[]string{"fsck"}, true},
		{[]string{"fsck", "--sprint", "s"}, false},
		{[]string{"push", "--sprint", "s"}, false},
		{[]string{"land", "--stream", "s", "--sha", "x"}, true},
	} {
		if got := isCardMove(tc.args[0], tc.args[1:]); got != tc.want {
			t.Errorf("isCardMove(%v) = %v, want %v", tc.args, got, tc.want)
		}
	}
}

// TestCardMovesCLI walks the verbs top to bottom (push -> deal -> work ->
// end -> read -> land) through the nova-sprint card verb, one receipt line
// each, with card fsck clean after every step, and task take/done working
// a friend's copies.
func TestCardMovesCLI(t *testing.T) {
	addr, c := sprintRedis(t)
	t.Setenv("NOVA_SPRINT_REDIS", addr)
	t.Setenv("NOVA_REDIS_ADDR", "")
	t.Setenv("NOVA_FRIEND", "")
	ctx := context.Background()
	const s = "swarm: cards"
	c.HSet(ctx, "bench:b:desired", "slots", "2")
	c.HSet(ctx, "friend:emma:desired", "slots", "2")
	for i := 0; i < 3; i++ {
		code, out, errOut := runTaskCLI("push", "--actor", "rowan", "--id", fmt.Sprintf("c%d", i), "--stream", s, "--waiting",
			"--kind", "build", "--repo", "mas-bandwidth/nova-tools", "--title", "t")
		if code != 0 {
			t.Fatalf("push %d %q %q", code, out, errOut)
		}
	}
	fsck := func(when string) {
		t.Helper()
		code, out, errOut := runCLI("card", "fsck")
		if code != 0 || !regexp.MustCompile(`^CARD FSCK consumers=\d+ live=\d+ retired=\d+ primaries=\d+ drift=0 fixed=0 ms=\d+\n$`).MatchString(out) {
			t.Fatalf("%s: card fsck = %d %q %q", when, code, out, errOut)
		}
	}
	code, out, errOut := runCLI("card", "deal", "--to", "bench:b", "--n", "2", "--actor", "rowan")
	if code != 0 || !strings.HasPrefix(out, "CARD DEAL to=bench:b n=2 copies=c0~1,c1~1 ms=") {
		t.Fatalf("deal = %d %q %q", code, out, errOut)
	}
	fsck("deal")
	code, out, _ = runCLI("card", "deal", "--to", "bench:b", "--ids", "c0")
	if code != 1 || !strings.Contains(out, "CARD DEAL REFUSED to=bench:b why=\"LIVECOPY task:c0 has live copy c0~1\"") {
		t.Fatalf("second deal = %d %q", code, out)
	}
	code, out, _ = runCLI("card", "work", "--as", "bench:b", "--fill")
	if code != 0 || !strings.HasPrefix(out, "CARD WORK as=bench:b n=2 free=0 ids=c0~1,c1~1 ms=") {
		t.Fatalf("work = %d %q", code, out)
	}
	code, out, _ = runCLI("card", "beat", "--as", "bench:b", "--ids", "c0~1,c1~1")
	if code != 0 || !strings.HasPrefix(out, "CARD BEAT as=bench:b n=2 lease_until=") {
		t.Fatalf("beat = %d %q", code, out)
	}
	h := strings.Repeat("e", 40)
	c.HSet(ctx, "pr:nova-tools:77", "head", h, "base", "dev")
	endOK := []string{"card", "end", "--id", "c0~1", "--ok", "--pr", "nova-tools#77", "--head", h, "--line1", "RESULT: c0",
		"--base", "dev", "--base-sha", strings.Repeat("1", 40), "--paths", "a.go"}
	code, out, _ = runCLI(endOK...)
	// the work copy's ok with a PR moves the primary to reading (no reader
	// enrolled: the read deal cuts its copy)
	if code != 0 || !strings.HasPrefix(out, "ENDED c0~1 primary=c0 from=working to=reading next=-\nCARD END n=1 ms=") {
		t.Fatalf("end ok with a PR = %d %q", code, out)
	}
	code, out, _ = runCLI(endOK...)
	if code != 0 || !strings.HasPrefix(out, "ALREADY c0~1 primary=c0 ended=ok\n") {
		t.Fatalf("repeat end = %d %q", code, out)
	}
	if code, out, _ = runCLI("card", "end", "--id", "c0~1", "--fail", "other"); code != 4 || !strings.Contains(out, "why=\"CONFLICT") {
		t.Fatalf("conflicting end = %d %q", code, out)
	}
	if code, out, _ = runCLI("card", "end", "--id", "c1~1", "--fail", "x", "--token", "stale"); code != 3 || !strings.Contains(out, "why=\"FENCED") {
		t.Fatalf("fenced end = %d %q", code, out)
	}
	ids := filepath.Join(t.TempDir(), "ids")
	if err := os.WriteFile(ids, []byte("c1~1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, _ = runCLI("card", "end", "--ids", "@"+ids, "--fail", "red at head")
	if code != 0 || !strings.HasPrefix(out, "ENDED c1~1 primary=c1 from=working to=review next=-\n") {
		t.Fatalf("end fail = %d %q", code, out)
	}
	code, out, _ = runCLI("card", "table", "--as", "bench:b")
	if code != 0 || !strings.HasPrefix(out, "CELLS bench:b ready=0 working=0 done=2 ok=1 fail=1 ok%=50\n") {
		t.Fatalf("table = %d %q", code, out)
	}
	fsck("end")
	// the read: a friend reader through task take / task done's copy form,
	// the score through card end. There is no card ci: no hand verb moves a
	// primary on a CI word.
	if code, out, _ = runCLI("card", "ci", "--repo", "nova-tools", "--head", h, "--ok"); code == 0 || strings.Contains(out, "CARD CI") {
		t.Fatalf("card ci = %d %q, want a refusal (the verb is gone)", code, out)
	}
	if w := c.HGet(ctx, "task:c0", "where").Val(); w != "reading" {
		t.Fatalf("c0 is %s after card ci, want reading", w)
	}
	code, out, _ = runCLI("card", "deal", "--to", "friend:emma", "--n", "1")
	if code != 0 || !strings.HasPrefix(out, "CARD DEAL to=friend:emma n=1 copies=c0~2 ms=") {
		t.Fatalf("read deal = %d %q", code, out)
	}
	code, out, _ = runCLI("card", "render", "--id", "c0~2")
	if code != 0 || !strings.Contains(out, "\nKIND: read\n") || !strings.Contains(out, "card end --id c0~2 --score N/10") {
		t.Fatalf("render = %d %q", code, out)
	}
	code, out, _ = runTaskCLI("take", "--actor", "emma")
	if code != 0 || !strings.HasPrefix(out, "TASK take n=1 ids=c0~2 ms=") {
		t.Fatalf("task take of a copy = %d %q", code, out)
	}
	// CI gates the read copy: a passing score waits for CI OK at the head
	code, out, _ = runCLI("card", "end", "--id", "c0~2", "--score", "9/10", "--gates", "ci:green,base:ok,scope:ok")
	if code != 1 || !strings.Contains(out, "why=\"CIPENDING") {
		t.Fatalf("read end before CI = %d %q", code, out)
	}
	if w := c.HGet(ctx, "task:c0", "where").Val(); w != "reading" {
		t.Fatalf("c0 is %s after a refused read end, want reading", w)
	}
	c.HSet(ctx, "ci:nova-tools:"+h, "final", "OK", "ci", "green")
	code, out, _ = runCLI("card", "end", "--id", "c0~2", "--score", "9/10", "--gates", "ci:green,base:ok,scope:ok")
	if code != 0 || !strings.HasPrefix(out, "ENDED c0~2 primary=c0 from=reading to=merging next=-\n") {
		t.Fatalf("read end = %d %q", code, out)
	}
	if got := c.HGet(ctx, "pr:nova-tools:77", "reads").Val(); got != "SCORE who=emma head="+h+" score=9/10 gates=ci:green,base:ok,scope:ok" {
		t.Fatalf("SCORE line %q", got)
	}
	fsck("read")
	code, out, _ = runCLI("card", "land", "--stream", s, "--sha", "abc12345")
	if code != 0 || !strings.Contains(out, "LANDED c0 ") || !strings.Contains(out, "CARD LAND stream=\"swarm: cards\" sha=abc12345 n=1 refused=0") {
		t.Fatalf("land = %d %q", code, out)
	}
	// task done of a friend's work copy is card end
	code, out, _ = runCLI("card", "deal", "--to", "friend:emma", "--ids", "c2")
	if code != 0 {
		t.Fatalf("deal c2 = %d %q", code, out)
	}
	if code, out, _ = runTaskCLI("take", "--actor", "emma"); code != 0 || !strings.Contains(out, "ids=c2~1") {
		t.Fatalf("take c2 = %d %q", code, out)
	}
	code, out, _ = runTaskCLI("done", "--actor", "emma", "--id", "c2~1", "--evidence", "report written")
	if code != 0 || !strings.HasPrefix(out, "TASK done id=c2~1 from=working to=ok primary=c2 primary_to=done ms=") {
		t.Fatalf("task done of a copy = %d %q", code, out)
	}
	fsck("task done")
	// c1's fail put it in review (#4072): a typed verdict is the way out
	code, out, _ = runSprint("review", "post", "--id", "c1", "--verdict", "redeal", "--why", "red at head was a flake")
	if code != 0 || !strings.HasPrefix(out, "REVIEW POST id=c1 verdict=redeal to=ready copy=- ms=") {
		t.Fatalf("review post = %d %q", code, out)
	}
	code, out, _ = runCLI("card", "assign", "--id", "c1", "--to", "friend:emma")
	if code != 0 || !strings.HasPrefix(out, "CARD ASSIGN id=c1 to=friend:emma copy=c1~2 revoked=- ms=") {
		t.Fatalf("assign = %d %q", code, out)
	}
	if code, out, _ = runCLI("card", "assign", "--id", "c1", "--to", "bench:b"); code != 1 || !strings.Contains(out, "LIVECOPY") {
		t.Fatalf("assign of a live card without --revoke = %d %q", code, out)
	}
	code, out, _ = runCLI("card", "assign", "--id", "c1", "--to", "bench:b", "--revoke", "--why", "rebalance")
	if code != 0 || !strings.HasPrefix(out, "CARD ASSIGN id=c1 to=bench:b copy=c1~3 revoked=c1~2 ms=") {
		t.Fatalf("assign --revoke = %d %q", code, out)
	}
	fsck("assign")
	code, out, _ = runCLI("card", "cancel", "--id", "c1", "--why", "superseded")
	if code != 0 || !strings.HasPrefix(out, "CANCELLED c1 to=done\nCARD CANCEL n=1 ms=") {
		t.Fatalf("cancel = %d %q", code, out)
	}
	fsck("cancel")
	// usage is exit 2
	if code, _, errOut := runCLI("card", "work", "--as", "bench:b"); code != 2 || !strings.Contains(errOut, "--fill") {
		t.Fatalf("usage = %d %q", code, errOut)
	}

	// #3916's push-from-issue and render checks, sharing this test's serial seat.
	cardRenderFromIssuePush(t)
}

// TestCardSessionCLI (#3998): `card session --as bench:<b>` is the bench
// harness's session start. A friend is refused (its seat is friend serve), a
// bench that is not enrolled takes nothing, and an enrolled bench takes its
// ready copies with one card work --fill; a copy whose wrapper cannot start
// is given back to its primary (card cancel), so nothing is left working
// without a live wrapper.
func TestCardSessionCLI(t *testing.T) {
	addr, c := sprintRedis(t)
	t.Setenv("NOVA_SPRINT_REDIS", addr)
	t.Setenv("NOVA_REDIS_ADDR", "")
	t.Setenv("NOVA_FRIEND", "")
	ctx := context.Background()
	c.HSet(ctx, "bench:b:desired", "slots", "2")
	if code, out, errOut := runTaskCLI("push", "--actor", "rowan", "--id", "s0", "--stream", "swarm: cards", "--waiting",
		"--kind", "build", "--repo", "mas-bandwidth/nova-tools", "--title", "t"); code != 0 {
		t.Fatalf("push %d %q %q", code, out, errOut)
	}
	if code, _, errOut := runCLI("card", "session", "--as", "friend:emma"); code != 2 || !strings.Contains(errOut, "session wants --as bench:<b>") {
		t.Fatalf("friend session = %d %q", code, errOut)
	}
	missing := filepath.Join(t.TempDir(), "nova-card")
	if code, out, _ := runCLI("card", "session", "--as", "bench:b", "--wrapper", missing); code != 0 || !strings.HasPrefix(out, "CARD SESSION bench:b not enrolled") {
		t.Fatalf("not enrolled = %d %q", code, out)
	}
	if code, out, _ := runCLI("card", "consumers", "--add", "bench:b"); code != 0 {
		t.Fatalf("enroll = %d %q", code, out)
	}
	if code, out, _ := runCLI("card", "deal", "--to", "bench:b", "--n", "1", "--actor", "rowan"); code != 0 {
		t.Fatalf("deal = %d %q", code, out)
	}
	code, out, _ := runCLI("card", "session", "--as", "bench:b", "--wrapper", missing)
	if code != 1 || !strings.HasPrefix(out, "CARD SESSION bench:b worked=1 launched=0 given_back=1 free=1 ms=") {
		t.Fatalf("session with no wrapper = %d %q", code, out)
	}
	if n := c.ZCard(ctx, "bench:b:cards:working").Val(); n != 0 {
		t.Fatalf("bench:b:cards:working holds %d after the give-back", n)
	}
	if w := c.HGet(ctx, "task:s0", "where").Val(); w != "waiting" {
		t.Fatalf("primary where=%s after the give-back, want waiting", w)
	}
}
