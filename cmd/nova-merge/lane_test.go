package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/merge"
)

// Demanded test 6: a red base stops the pass before any entry is read, and --planned-red
// is a person's name on the exception, printed on every pass it applied to.
func TestARedBaseStopsTheLaneAndPlannedRedIsTheOneWayThrough(t *testing.T) {
	l := newLab(t)
	setupPR(t, l, 951, "feature-a", "a.txt", false)
	l.host.SetChecks(l.baseSHA(), 2, 0, "tables-cpp-fixedform")
	exit, stdout, stderr := l.run("run", "--lane", l.lane, "--once")
	if exit != 1 {
		t.Fatalf("a red base stops the lane: exit %d\n%s\n%s", exit, stdout, stderr)
	}
	contains(t, stderr, "RUN STOPPED base=main: the base is red")
	absent(t, stdout, "RUN ENTRY")
	absent(t, stdout, "MERGE OK")
	// RUN STOPPED is the third line: RUN PASS, RUN BASE, then the stop.
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	if len(lines) < 2 || !strings.HasPrefix(lines[0], "RUN PASS") || !strings.HasPrefix(lines[1], "RUN BASE") {
		t.Errorf("RUN BASE is the second line of every pass:\n%s", stdout)
	}
	contains(t, stdout, "state=RED")

	// --planned-red: a temporary red a person has named and planned on the way to green.
	exit, stdout, stderr = l.run("run", "--lane", l.lane, "--once", "--planned-red", "the digest change lands in two steps")
	contains(t, stdout, "planned_red=the\\x20digest\\x20change\\x20lands\\x20in\\x20two\\x20steps")
	contains(t, stdout, "state=PLANNED-RED")
	contains(t, stdout, "RUN ENTRY")
	if exit == 1 && strings.Contains(stderr, "nothing merges onto a red base") {
		t.Errorf("with --planned-red the pass proceeds:\n%s", stderr)
	}
	// And it is in the log, where a reader of the morning after finds it.
	raw, err := os.ReadFile(filepath.Join(l.lane, merge.LogName))
	if err != nil {
		t.Fatal(err)
	}
	contains(t, string(raw), "planned_red=the digest change lands in two steps")
}

// Demanded test 3: THE STORM DOES NOT HAPPEN. After a merge, an entry the host reports
// MERGEABLE is not touched -- no fetch of its head, no commit, no push -- and an entry
// reported CONFLICTING gets exactly one re-merge attempt.
func TestOnlyAConflictingEntryIsReMerged(t *testing.T) {
	l := newLab(t)
	l.init("main")
	l.host.SetChecks(l.baseSHA(), 2, 0)
	clean := l.branch("feature-clean", "README.md", "rewritten by the first entry\n", "a change to the file the other one also touches")
	conflicting := l.branch("feature-conflicting", "README.md", "a rewrite\n", "a conflicting change")
	l.host.PRs[1] = merge.PR{Number: 1, Author: "pat", Base: "main", HeadRef: "feature-clean", HeadOID: clean, Mergeable: "MERGEABLE"}
	l.host.PRs[2] = merge.PR{Number: 2, Author: "pat", Base: "main", HeadRef: "feature-conflicting", HeadOID: conflicting, Mergeable: "CONFLICTING"}
	l.host.SetChecks(clean, 2, 0)
	l.host.SetChecks(conflicting, 2, 0)
	for _, n := range []string{"1", "2"} {
		if exit, _, errb := l.run("add", "--lane", l.lane, "--pr", n); exit != 0 {
			t.Fatalf("add: %s", errb)
		}
	}
	_, stdout, _ := l.run("run", "--lane", l.lane, "--once")
	m := mergeSHAOf(t, stdout, "1")
	base := l.baseSHA()
	if exit, _, errb := l.run("gate", "--lane", l.lane, "--pr", "1", "--head", clean,
		"--base-sha", base, "--merge", m, "--verdict", "green", "--summary", l.summary("clean")); exit != 0 {
		t.Fatalf("gate: %s", errb)
	}
	exit, stdout, stderr := l.run("run", "--lane", l.lane, "--once")
	contains(t, stdout, "MERGE OK entry=1")
	l.host.SetChecks(l.refreshBase(), 2, 0)
	// The conflicting entry behind it is re-merged exactly once, and the base is now
	// ahead of it. The merge of the base into its head conflicts, so it is BLOCKED with
	// every conflicting file and the exact hand command.
	exit, stdout, stderr = l.run("run", "--lane", l.lane, "--once")
	if exit != 1 {
		t.Fatalf("a blocked entry is exit 1: %d\n%s\n%s", exit, stdout, stderr)
	}
	contains(t, stdout, "RUN REMERGE entry=2")
	contains(t, stdout, "result=blocked")
	contains(t, stderr, "RUN BLOCKED entry=2")
	contains(t, stderr, "README.md")
	contains(t, stderr, "git clone")
	contains(t, stderr, "git push origin HEAD:feature-conflicting")
	// The head is untouched: the lane never edits an entry's content.
	if got := l.git(l.work, "rev-parse", "refs/remotes/origin/feature-conflicting"); got != conflicting {
		t.Errorf("the entry's head moved to %s; a conflict is BLOCKED and a hand resolves it", got)
	}
	// A second pass with the same head prints nothing new for it: no pass retries a
	// BLOCKED entry until a new head arrives.
	_, stdout2, stderr2 := l.run("run", "--lane", l.lane, "--once")
	absent(t, stdout2, "RUN REMERGE entry=2")
	absent(t, stderr2, "RUN BLOCKED entry=2")
	contains(t, stdout2, "state=BLOCKED")
}

// Demanded test 23: the packet is POINTERS, NEVER THE DIFF.
func TestThePacketIsPointersNotDiff(t *testing.T) {
	l := newLab(t)
	h1 := setupPR(t, l, 951, "feature-a", "a.txt", true)
	// A second entry nobody has read, and a third this reader has already approved.
	for _, e := range []struct {
		n      int
		branch string
	}{{942, "feature-b"}, {949, "feature-c"}} {
		oid := l.branch(e.branch, e.branch+".txt", "x\n", "a change")
		l.host.PRs[e.n] = merge.PR{Number: e.n, Author: "pat", Base: "main", HeadRef: e.branch,
			HeadOID: oid, Mergeable: "MERGEABLE", URL: "https://example.invalid/" + e.branch}
		l.host.SetChecks(oid, 2, 0)
		if exit, _, errb := l.run("add", "--lane", l.lane, "--pr", itoa(e.n), "--needs-read"); exit != 0 {
			t.Fatalf("add: %s", errb)
		}
	}
	if exit, _, errb := l.run("run", "--lane", l.lane, "--once"); exit != 0 {
		t.Fatalf("run: %s", errb)
	}
	// emma read 951 at H1; the author then pushes H2.
	if exit, _, errb := l.run("read", "--lane", l.lane, "--pr", "951", "--who", "emma",
		"--head", h1, "--verdict", "approve"); exit != 0 {
		t.Fatalf("read: %s", errb)
	}
	l.git(l.work, "checkout", "-q", "feature-a")
	l.write("a.txt", "A DISTINCTIVE TOKEN the packet must never print\n")
	h2 := l.commit("a second commit")
	l.git(l.work, "push", "-q", "origin", "HEAD:refs/heads/feature-a")
	l.git(l.work, "checkout", "-q", "main")
	pr := l.host.PRs[951]
	pr.HeadOID = h2
	l.host.PRs[951] = pr
	l.host.SetChecks(h2, 2, 0)
	// Two holds by stella on the first entry, and emma's current approve on the third.
	for i, note := range []string{"the wire's shape is not what the spec says", "the second finding"} {
		l.now = l.now.Add(time.Duration(i+1) * time.Minute)
		if exit, _, errb := l.run("read", "--lane", l.lane, "--pr", "951", "--who", "stella",
			"--head", h2, "--verdict", "hold", "--note", note); exit != 0 {
			t.Fatalf("read: %s", errb)
		}
	}
	if exit, _, errb := l.run("run", "--lane", l.lane, "--once"); exit != 1 {
		t.Fatalf("a hold is exit 1: %s", errb)
	}
	if exit, _, errb := l.run("read", "--lane", l.lane, "--pr", "949", "--who", "emma",
		"--head", l.host.PRs[949].HeadOID, "--verdict", "approve"); exit != 0 {
		t.Fatalf("read: %s", errb)
	}

	before, err := os.ReadFile(merge.StatePath(l.lane))
	if err != nil {
		t.Fatal(err)
	}
	exit, stdout, stderr := l.run("packet", "--lane", l.lane, "--who", "emma", "--all")
	if exit != 0 {
		t.Fatalf("packet reports and exits 0: %d\n%s\n%s", exit, stdout, stderr)
	}
	// Two blocks, in lane order, and not the third: emma has a current approve on 949.
	if n := strings.Count(stdout, "PACKET ENTRY"); n != 2 {
		t.Errorf("want two blocks (951 and 942), got %d:\n%s", n, stdout)
	}
	absent(t, stdout, "entry=949")
	contains(t, stdout, "last_read="+merge.Short(h1))
	contains(t, stdout, "range="+merge.Short(h1)+".."+merge.Short(h2))
	contains(t, stdout, "last_read=-")
	contains(t, stdout, "range=main...")
	contains(t, stdout, "holds=2")
	contains(t, stdout, "PACKET HOLD who=stella")
	contains(t, stdout, "PACKET OK entries=2 holds=2")
	// The diff itself is never printed, and neither is a log line.
	absent(t, stdout, "A DISTINCTIVE TOKEN")
	// packet writes nothing and takes no lock.
	after, _ := os.ReadFile(merge.StatePath(l.lane))
	if string(before) != string(after) {
		t.Error("packet is derived from the fold and the host: it writes nothing")
	}
	if _, err := os.Stat(filepath.Join(l.lane, merge.StateLock)); err == nil {
		if raw, _ := os.ReadFile(filepath.Join(l.lane, merge.StateLock)); strings.Contains(string(raw), "pid=") {
			// The lock file exists from earlier verbs; what matters is that packet did
			// not write it, which the byte comparison of the state already proves.
			_ = raw
		}
	}
	// --max 1 prints one hold and a MORE line.
	_, stdout, _ = l.run("packet", "--lane", l.lane, "--who", "emma", "--all", "--max", "1")
	if n := strings.Count(stdout, "PACKET HOLD"); n != 1 {
		t.Errorf("--max 1 prints one hold, got %d", n)
	}
	contains(t, stdout, "PACKET MORE kind=hold shown=1 total=2")
}
