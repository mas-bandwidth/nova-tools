package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// briefOf is the brief the work card of the primary is handed, as queue --json
// prints it for the member.
func (ta *testApp) briefOf(member, card string) string {
	ta.t.Helper()
	var q struct{ Cards []queueCard }
	ta.json("queue --as "+member, &q)
	for _, c := range q.Cards {
		if c.Primary == card && c.Packet != nil {
			return c.Packet.Brief
		}
	}
	ta.t.Fatalf("no packet of %s in %s's queue: %+v", card, member, q.Cards)
	return ""
}

// add --brief-file: a brief of many paragraphs, read from a file and stored
// byte for byte with its one trailing newline cut; queue --json and take
// --json carry it whole.
func TestAddBriefFileStoresTheBriefByteForByte(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	dir := t.TempDir()
	for _, c := range []struct{ name, file, want string }{
		{"paragraphs", "Fix the empty case.\n\nThen:\n\t- keep the tab\n  - keep the indent, and \"quotes\", 'ticks', ünï\n", "Fix the empty case.\n\nThen:\n\t- keep the tab\n  - keep the indent, and \"quotes\", 'ticks', ünï"},
		{"two newlines leave one", "one line\n\n", "one line\n"},
		{"no newline", "one line", "one line"},
	} {
		path := filepath.Join(dir, strings.ReplaceAll(c.name, " ", "-")+".md")
		if err := os.WriteFile(path, []byte(c.file), 0o600); err != nil {
			t.Fatal(err)
		}
		stream := "s" + strings.ReplaceAll(c.name, " ", "")
		ta.ok("add --stream " + stream + " --count 1 --brief-file " + path)
		ta.deal(1)
		if got := ta.briefOf("m1", stream+"-1"); got != c.want {
			t.Errorf("%s: the packet in queue --json carries %q, want %q", c.name, got, c.want)
		}
		var took struct{ Packets []sprint.Packet }
		ta.json("take --as m1 --limit 1", &took)
		if len(took.Packets) != 1 || took.Packets[0].Brief != c.want {
			t.Errorf("%s: take --json carries %+v, want brief %q", c.name, took.Packets, c.want)
		}
	}
	ta.clean()
}

// --brief and --brief-file together, a missing file and a file that is not
// one are refused with exit 2 and nothing written.
func TestAddBriefFileRefusals(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	dir := t.TempDir()
	path := filepath.Join(dir, "brief.md")
	if err := os.WriteFile(path, []byte("a brief\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(dir, "absent.md")
	before := ta.applies()
	for _, c := range []struct{ line, want string }{
		{"add --stream s1 --count 1 --brief x --brief-file " + path, "--brief and --brief-file are two ways to give the brief"},
		{"add --stream s1 --count 1 --brief-file " + missing, missing},
		{"add --stream s1 --count 1 --brief-file " + dir, dir},
	} {
		code, out, errs := ta.do(c.line)
		if code != 2 || !strings.Contains(errs, c.want) || strings.Contains(out, "MOVED") {
			t.Errorf("%s: exit %d, out %q, err %q; want exit 2 naming %q", c.line, code, out, errs, c.want)
		}
	}
	if ta.applies() != before {
		t.Fatal("a refused add wrote")
	}
}

// raw runs a command line as typed: no epoch is added for a report.
func (ta *testApp) raw(line string) (int, string, string) {
	var out, errb bytes.Buffer
	ta.beat()
	code := ta.a.run(split(line), &out, &errb)
	return code, out.String(), errb.String()
}

// Which verbs need --epoch: the verbs that act on cards handed to an actor
// outside the sprint (take by id, finish, read, ci, and a merge by anyone but
// the coordinator), never the coordinator's own. A table over every verb.
func TestWhichVerbsNeedAnEpoch(t *testing.T) {
	t.Parallel()
	always := map[string]bool{"finish": true, "read": true, "ci": true, "take by id": true}
	seen := map[string]bool{}
	for _, v := range append(append([]verb(nil), verbs...), verb{name: "take by id"}) {
		seen[v.name] = true
		if got := needsEpoch(v.name, false); got != (always[v.name] || v.name == "merge") {
			t.Errorf("%s (not the coordinator's): needs --epoch is %v", v.name, got)
		}
		if got := needsEpoch(v.name, true); got != always[v.name] {
			t.Errorf("%s (the coordinator's): needs --epoch is %v", v.name, got)
		}
		if class := verbClasses[v.name]; class == classCoordinator && needsEpoch(v.name, false) {
			t.Errorf("%s is the coordinator's and needs --epoch", v.name)
		}
	}
	for name := range epochVerbs {
		if !seen[name] {
			t.Errorf("%s needs --epoch and is no verb", name)
		}
	}
}

// The coordinator's merge and accept need no --epoch; an outside actor's
// merge is refused without it, and a stale one is refused naming the clear.
func TestTheCoordinatorsMergeNeedsNoEpoch(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("add --stream s1 --count 3")
	ta.deal(3)
	ta.ok("take --as m1 --limit 3")
	ta.ok("finish --as m1 s1-1.w1@1 s1-2.w1@1 s1-3.w1@1")
	ta.ok("ask")
	for _, r := range []string{"reader-a", "reader-b"} {
		ta.ok("read --as " + r + " --ok --limit 10")
	}
	if code, out, errs := ta.raw("accept --read-ok"); code != 0 || !strings.Contains(out, "ACCEPT OK moved=3") {
		t.Fatalf("accept with no --epoch: %d\n%s%s", code, out, errs)
	}
	before := ta.applies()
	code, _, errs := ta.raw("merge --stream s1 --batch 3 --actor outsider")
	if code != 2 || !strings.Contains(errs, "a report names the epoch its cards were handed at: --epoch <n>") || ta.applies() != before {
		t.Fatalf("another actor's merge with no --epoch: %d %s", code, errs)
	}
	code, out, errs := ta.raw("merge --stream s1 --batch 3")
	if code != 0 || !strings.Contains(out, "MERGE OK") {
		t.Fatalf("the coordinator's merge with no --epoch: %d\n%s%s", code, out, errs)
	}
	ta.ok("clear --confirm sprint")
	before = ta.applies()
	if code, _, errs := ta.raw("merge --stream s1 --batch 3 --actor outsider --epoch 0"); code == 0 || !strings.Contains(errs, "cleared") || ta.applies() != before {
		t.Fatalf("an outsider's merge with a stale epoch: %d %s", code, errs)
	}
	if code, _, errs := ta.raw("merge --stream s1 --batch 3 --epoch 0"); code == 0 || !strings.Contains(errs, "cleared") {
		t.Fatalf("the coordinator's merge at the epoch before the clear: %d %s", code, errs)
	}
	if code, out, errs := ta.raw("merge --stream s1 --batch 3"); strings.Contains(out+errs, "--epoch <n>") || strings.Contains(out+errs, "cleared") {
		t.Fatalf("the coordinator's merge with no --epoch after a clear reads the new epoch: %d\n%s%s", code, out, errs)
	}
}

// An outside actor attempting a merge with a stale epoch after a clear is
// refused naming the clear.
func TestOutsidersStaleEpochMergeIsRefusedNamingTheClear(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("clear --confirm sprint")
	before := ta.applies()
	code, _, errs := ta.raw("merge --stream s1 --batch 1 --actor outsider --epoch 0")
	if code != 1 || !strings.Contains(errs, "cleared at") || !strings.Contains(errs, "epoch is now 1") || ta.applies() != before {
		t.Fatalf("outsider merge with stale epoch: exit %d, err %q", code, errs)
	}
}

// inbox --json prints the open judgments as an array a coordinator acts on:
// each with its id, kind, what, size, stream, cards and the exact command
// lines of each decision; the happened notes and the done flag beside it. A
// line of the answer, run as printed (a '<...>' filled in), answers the
// judgment.
func TestInboxJSONCarriesTheJudgmentsToActOn(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("add --stream s1 --count 3")
	ta.ok("add --stream s2 --count 1")
	ta.deal(4)
	ta.failOnce("m1", "s1-1.w1@1", "the tests went red")
	ta.failOnce("m1", "s1-2.w1@1", "the tests went red")
	ta.failOnce("m1", "s2-1.w1@1", "abandoned idea")
	ta.ok("take --as m1 s1-3.w1@1")
	ta.ok("finish --as m1 s1-3.w1@1")
	var in struct {
		Judgments []inboxJudgment
		Happened  []inboxHappened
		Done      bool
	}
	ta.json("inbox", &in)
	// the stopped machine's judgment, and the failed work's (one per stream)
	if len(in.Judgments) != 3 || in.Done || len(in.Happened) == 0 {
		t.Fatalf("the inbox: %+v", in)
	}
	j1 := byStream(t, in.Judgments, "s1")
	if j1.ID == "" || j1.Kind != sprint.Judgment || j1.Type != sprint.NWorkFailed || j1.Size != 2 ||
		strings.Join(j1.Cards, ",") != "s1-1,s1-2" || len(j1.Notes) == 0 ||
		j1.What != "the tests went red" || j1.Due == nil || j1.Due.IsZero() || j1.Waited != "2s" {
		t.Fatalf("the s1 judgment: %+v", j1)
	}
	j2 := byStream(t, in.Judgments, "s2")
	if j2.ID == "" || j2.Kind != sprint.Judgment || j2.Type != sprint.NWorkFailed || j2.Size != 1 ||
		strings.Join(j2.Cards, ",") != "s2-1" || len(j2.Notes) == 0 ||
		j2.What != "abandoned idea" || j2.Due == nil || j2.Due.IsZero() || j2.Waited != "0s" {
		t.Fatalf("the s2 judgment: %+v", j2)
	}
	var rework, drop1 []string
	for _, a := range j1.Answers {
		switch {
		case strings.HasPrefix(a.Decision, "rework"):
			rework = a.Commands
		case a.Decision == "drop":
			drop1 = a.Commands
		}
	}
	if len(rework) != 1 || !strings.HasPrefix(rework[0], "nova-sprint rework --group "+j1.ID+" --expect 2 --answers ") {
		t.Fatalf("the s1 rework answers: %+v", j1.Answers)
	}
	if len(drop1) != 1 || !strings.HasPrefix(drop1[0], "nova-sprint drop --group "+j1.ID+" --expect 2 --reason '<why>' --answers ") {
		t.Fatalf("the s1 drop answers: %+v", j1.Answers)
	}
	var drop2 []string
	for _, a := range j2.Answers {
		if a.Decision == "drop" {
			drop2 = a.Commands
		}
	}
	if len(drop2) != 1 || !strings.HasPrefix(drop2[0], "nova-sprint drop --group "+j2.ID+" --expect 1 --reason '<why>' --answers ") {
		t.Fatalf("the s2 drop answers: %+v", j2.Answers)
	}
	var h inboxHappened
	for _, x := range in.Happened {
		if x.Type == sprint.NWorkOK {
			h = x
		}
	}
	if h.ID == "" || h.Kind != sprint.Happened || h.Type != sprint.NWorkOK || h.Stream != "s1" || strings.Join(h.Cards, ",") != "s1-3" {
		t.Fatalf("the happened note: %+v", h)
	}
	// the line as printed answers the judgment
	ta.ok(strings.TrimPrefix(rework[0], "nova-sprint "))
	// the drop line as printed, with '<why>' filled in, answers the judgment
	dropCmd := strings.Replace(drop2[0], "'<why>'", "'not needed'", 1)
	ta.ok(strings.TrimPrefix(dropCmd, "nova-sprint "))
	ta.json("inbox", &in)
	if len(in.Judgments) != 1 || in.Judgments[0].Type == sprint.NWorkFailed {
		t.Fatalf("the judgments after the answers: %+v", in.Judgments)
	}
}

// byType is the judgment of a type.
func byType(t *testing.T, js []inboxJudgment, typ string) inboxJudgment {
	t.Helper()
	for _, j := range js {
		if j.Type == typ {
			return j
		}
	}
	t.Fatalf("no judgment %q in %+v", typ, js)
	return inboxJudgment{}
}

// byStream is the judgment of a stream.
func byStream(t *testing.T, js []inboxJudgment, stream string) inboxJudgment {
	t.Helper()
	for _, j := range js {
		if j.Stream == stream {
			return j
		}
	}
	t.Fatalf("no judgment for stream %q in %+v", stream, js)
	return inboxJudgment{}
}

// done is true once the machine has stopped because the sprint is done, with
// the coordinator's happened note carrying what to do next.
func TestInboxJSONSaysWhenTheSprintIsDone(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b,reader-c --members m1,m2")
	ta.ok("add --stream s1 --count 3")
	ta.ok("start")
	ta.playToLanded(1)
	ta.ok("tick")
	if out := ta.ok("inbox --json"); !strings.Contains(out, `"judgments":[]`) || !strings.Contains(out, `"done":true`) {
		t.Fatalf("an empty judgments array and the done flag are printed:\n%s", out)
	}
	var in struct {
		Judgments []inboxJudgment
		Happened  []inboxHappened
		Done      bool
	}
	ta.json("inbox", &in)
	if !in.Done || len(in.Judgments) != 0 || len(in.Happened) == 0 || in.Happened[0].Type != sprint.NSprintDone || in.Happened[0].To != "coordinator" || in.Happened[0].Hint == "" {
		t.Fatalf("the inbox of a done sprint: %+v", in)
	}
}
