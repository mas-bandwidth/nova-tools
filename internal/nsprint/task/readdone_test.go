package task_test

import (
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/land/stream"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/task"
)

// readCard is a read of nova-tools#7 at head in sprint S, pushed to emma and
// taken by her, with the PR record the line store keys on (pr:nova-tools:7).
func readCard(t *testing.T, head string) (*oneStore, task.Claim) {
	t.Helper()
	fx := newOneStore(t)
	seedFriend(t, fx.client, "emma", 4)
	fx.client.HSet(fx.ctx, "s:"+fx.S+":pr:nova-tools:7", "head", head)
	fx.client.HSet(fx.ctx, "pr:nova-tools:7", "head", head, "base", "dev")
	if res := fx.push("read-7", "emma", func(r *task.PushRequest) {
		r.Kind, r.Title, r.Author, r.Repo, r.PR, r.Head = task.KindRead, "read nova-tools#7", "a", "nova-tools", 7, head
	}); res.Status != task.PushCreated {
		t.Fatalf("push read = %+v", res)
	}
	return fx, fx.take("read-7", "emma")
}

// TestReadDoneRequiresScoreOnRecord is the DONE-WHEN of nova-tools#3897: a
// read is done only by the verb. done without a line refuses (exit 1, the
// remedy names --line); a line whose head is not the task's head, whose who
// is not the task's friend, or whose typed gate disagrees with the measured
// one refuses; a valid line appends exactly one line to pr:<name>:<n>:lines
// and closes the card in the same call, and the lander's ReadAt sees it at
// head. Nothing is written by a refusal.
func TestReadDoneRequiresScoreOnRecord(t *testing.T) {
	head := strings.Repeat("a", 40)
	fx, claim := readCard(t, head)
	ctx := fx.ctx
	lines := func() int64 { return fx.client.LLen(ctx, "pr:nova-tools:7:lines").Val() }
	untouched := func(step string) {
		t.Helper()
		if n := lines(); n != 0 {
			t.Fatalf("%s: %d lines on the record, want 0", step, n)
		}
		if st := fx.state("read-7"); st != "claimed" {
			t.Fatalf("%s: card is %s, want claimed", step, st)
		}
	}

	// The legacy close (verdict, score, head, no line) refuses.
	res, err := task.DoneTyped(ctx, fx.st, task.DoneRequest{Sprint: fx.S, ID: "read-7", Token: claim.Token,
		Evidence: "read on the bus", Verdict: "APPROVE", Score: "9", Head: head})
	if err != nil || res.Status != task.DoneNoLine || res.Status.ExitCode() != 1 || !strings.Contains(res.Why, "--line") {
		t.Fatalf("done without a line = %+v, %v; want NOLINE exit 1 naming --line", res, err)
	}
	untouched("legacy")
	// So does the verb's call with an empty line.
	rd, err := task.ReadDone(ctx, fx.st, task.ReadDoneRequest{Sprint: fx.S, ID: "read-7", Token: claim.Token})
	res = rd.DoneOutcome
	if err != nil || res.Status != task.DoneNoLine || res.Status.ExitCode() != 1 || !strings.Contains(res.Why, "--line") {
		t.Fatalf("read done with no line = %+v, %v; want NOLINE exit 1 naming --line", res, err)
	}
	untouched("empty line")

	refuse := func(step, line, want string) {
		t.Helper()
		res, err := task.ReadDone(ctx, fx.st, task.ReadDoneRequest{Sprint: fx.S, ID: "read-7", Token: claim.Token, Line: line})
		if err != nil || res.Status != task.DoneBadLine || res.Status.ExitCode() != 1 || !strings.Contains(res.Why, want) {
			t.Fatalf("%s = %+v, %v; want BADLINE exit 1 with %q", step, res, err, want)
		}
		untouched(step)
	}
	other := strings.Repeat("b", 40)
	refuse("head not the task's", "SCORE who=emma head="+other+" score=9/10 gates=ci:ok,base:ok,scope:ok: fine", "head")
	seedFriend(t, fx.client, "stella", 4)
	refuse("who not the task's friend", "SCORE who=stella head="+head+" score=9/10: fine", "who")
	refuse("not a SCORE", "REPAIR who=emma head="+head+": fix it", "SCORE")
	fx.client.HSet(ctx, "ci:nova-tools:"+head, "ci", "red")
	refuse("gate typed against the measure", "SCORE who=emma head="+head+" score=9/10 gates=ci:ok,base:ok,scope:ok: fine", "measured red")
	fx.client.HSet(ctx, "ci:nova-tools:"+head, "ci", "green")

	valid := "SCORE who=emma head=" + head + " score=9/10 gates=ci:ok,base:ok,scope:ok: fine"
	rd, err = task.ReadDone(ctx, fx.st, task.ReadDoneRequest{Sprint: fx.S, ID: "read-7", Token: claim.Token, Line: valid})
	if err != nil || rd.Status != task.DoneClosed || rd.Status.ExitCode() != 0 || rd.Lines != 1 ||
		rd.LineKey != "pr:nova-tools:7:line:"+head+":emma:SCORE" {
		t.Fatalf("valid line = %+v, %v; want DONE with the line record", rd, err)
	}
	if n := lines(); n != 1 {
		t.Fatalf("%d lines after the read, want exactly 1", n)
	}
	if st := fx.state("read-7"); st != "closed" || !fx.member("s:"+fx.S+":idx:task:closed", "read-7") {
		t.Fatalf("card is %s after the read, want closed and indexed", st)
	}
	if got := fx.client.HGet(ctx, "pr:nova-tools:7:line:"+head+":emma:SCORE", "gates").Val(); got != "ci:ok,base:ok,scope:ok" {
		t.Fatalf("line record gates %q", got)
	}
	reads := strings.Split(fx.client.HGet(ctx, "pr:nova-tools:7", "reads").Val(), "\n")
	if r := stream.ReadAt(reads, head); r.Who != "emma" || r.Score != 9 {
		t.Fatalf("lander ReadAt at head = %+v, want emma 9", r)
	}

	// A repeat of the same done is CLOSED and appends nothing.
	rd, err = task.ReadDone(ctx, fx.st, task.ReadDoneRequest{Sprint: fx.S, ID: "read-7", Token: claim.Token, Line: valid})
	if err != nil || rd.Status != task.DoneRepeat {
		t.Fatalf("repeat = %+v, %v; want CLOSED", rd, err)
	}
	if n := lines(); n != 1 {
		t.Fatalf("%d lines after a repeat, want 1", n)
	}
}

// TestReadDoneBlockedAndReportReadsKeepTheirClose: a read that could not
// happen still closes BLOCKED with score 0 (no read counter reads it), and a
// report read (pr 0, no PR record) keeps the flag close.
func TestReadDoneBlockedAndReportReadsKeepTheirClose(t *testing.T) {
	head := strings.Repeat("c", 40)
	fx, claim := readCard(t, head)
	res, err := task.DoneTyped(fx.ctx, fx.st, task.DoneRequest{Sprint: fx.S, ID: "read-7", Token: claim.Token,
		Evidence: "blocked: exit=3", Verdict: "BLOCKED", Score: "0", Head: head})
	if err != nil || res.Status != task.DoneClosed {
		t.Fatalf("blocked read = %+v, %v; want DONE", res, err)
	}
	if n := fx.client.LLen(fx.ctx, "pr:nova-tools:7:lines").Val(); n != 0 {
		t.Fatalf("a blocked read wrote %d lines", n)
	}
	fx.client.HSet(fx.ctx, fx.key("report-1"), "kind", "read", "repo", "nova-tools", "pr", "0", "head", "",
		"owner", "emma", "state", "claimed", "token", "t1", "attempt", "1")
	res, err = task.DoneTyped(fx.ctx, fx.st, task.DoneRequest{Sprint: fx.S, ID: "report-1", Token: "t1",
		Evidence: "SCORE who=emma report", Verdict: "APPROVE", Score: "8"})
	if err != nil || res.Status != task.DoneClosed {
		t.Fatalf("report read = %+v, %v; want DONE", res, err)
	}
}
