package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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
	// the brief is the card lint's passing brief, then the paragraphs under test
	rules := strings.TrimSuffix(passingBrief("Fix the empty case."), "\n")
	for _, c := range []struct{ name, file, want string }{
		{"paragraphs", rules + "\n\nThen:\n\t- keep the tab\n  - keep the indent, and \"quotes\", 'ticks', ünï\n", rules + "\n\nThen:\n\t- keep the tab\n  - keep the indent, and \"quotes\", 'ticks', ünï"},
		{"two newlines leave one", rules + "\n\n", rules + "\n"},
		{"no newline", rules, rules},
	} {
		path := filepath.Join(dir, strings.ReplaceAll(c.name, " ", "-")+".md")
		require.NoError(t, os.WriteFile(path, []byte(c.file), 0o600))
		stream := "s" + strings.ReplaceAll(c.name, " ", "")
		ta.ok("add --stream " + stream + " --count 1 --brief-file " + path)
		ta.deal(1)
		assert.Equal(t, c.want, ta.briefOf("m1", stream+"-1"), "%s: the packet in queue --json carries the brief", c.name)
		var took struct{ Packets []sprint.Packet }
		ta.json("take --as m1 --limit 1", &took)
		if !assert.Len(t, took.Packets, 1, "%s: take --json carries %+v, want brief %q", c.name, took.Packets, c.want) {
			continue
		}
		assert.Equal(t, c.want, took.Packets[0].Brief, "%s: take --json carries %+v, want brief %q", c.name, took.Packets, c.want)
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
	require.NoError(t, os.WriteFile(path, []byte("a brief\n"), 0o600))
	missing := filepath.Join(dir, "absent.md")
	before := ta.applies()
	for _, c := range []struct{ line, want string }{
		{"add --stream s1 --count 1 --brief x --brief-file " + path, "--brief and --brief-file are two ways to give the brief"},
		{"add --stream s1 --count 1 --brief-file " + missing, missing},
		{"add --stream s1 --count 1 --brief-file " + dir, dir},
	} {
		code, out, errs := ta.do(c.line)
		assert.Equal(t, 2, code, "%s: exit %d, out %q, err %q; want exit 2 naming %q", c.line, code, out, errs, c.want)
		assert.Contains(t, errs, c.want, "%s: exit %d, out %q, err %q; want exit 2 naming %q", c.line, code, out, errs, c.want)
		assert.NotContains(t, out, "MOVED", "%s: exit %d, out %q, err %q; want exit 2 naming %q", c.line, code, out, errs, c.want)
	}
	require.Equal(t, before, ta.applies(), "a refused add wrote")
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
		assert.Equal(t, always[v.name] || v.name == "merge", needsEpoch(v.name, false), "%s (not the coordinator's): needs --epoch", v.name)
		assert.Equal(t, always[v.name], needsEpoch(v.name, true), "%s (the coordinator's): needs --epoch", v.name)
		class := verbClasses[v.name]
		assert.False(t, class == classCoordinator && needsEpoch(v.name, false), "%s is the coordinator's and needs --epoch", v.name)
	}
	for name := range epochVerbs {
		assert.True(t, seen[name], "%s needs --epoch and is no verb", name)
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
	code, out, errs := ta.raw("accept --read-ok")
	require.Equal(t, 0, code, "accept with no --epoch: %d\n%s%s", code, out, errs)
	require.Contains(t, out, "ACCEPT OK moved=3", "accept with no --epoch: %d\n%s%s", code, out, errs)
	before := ta.applies()
	code, _, errs = ta.raw("merge --stream s1 --batch 3 --actor outsider")
	require.Equal(t, 2, code, "another actor's merge with no --epoch: %d %s", code, errs)
	require.Contains(t, errs, "a report names the epoch its cards were handed at: --epoch <n>", "another actor's merge with no --epoch: %d %s", code, errs)
	require.Equal(t, before, ta.applies(), "another actor's merge with no --epoch: %d %s", code, errs)
	code, out, errs = ta.raw("merge --stream s1 --batch 3")
	require.Equal(t, 0, code, "the coordinator's merge with no --epoch: %d\n%s%s", code, out, errs)
	require.Contains(t, out, "MERGE OK", "the coordinator's merge with no --epoch: %d\n%s%s", code, out, errs)
	ta.ok("clear --confirm sprint")
	before = ta.applies()
	code, _, errs = ta.raw("merge --stream s1 --batch 3 --actor outsider --epoch 0")
	require.NotEqual(t, 0, code, "an outsider's merge with a stale epoch: %d %s", code, errs)
	require.Contains(t, errs, "cleared", "an outsider's merge with a stale epoch: %d %s", code, errs)
	require.Equal(t, before, ta.applies(), "an outsider's merge with a stale epoch: %d %s", code, errs)
	code, _, errs = ta.raw("merge --stream s1 --batch 3 --epoch 0")
	require.NotEqual(t, 0, code, "the coordinator's merge at the epoch before the clear: %d %s", code, errs)
	require.Contains(t, errs, "cleared", "the coordinator's merge at the epoch before the clear: %d %s", code, errs)
	code, out, errs = ta.raw("merge --stream s1 --batch 3")
	require.NotContains(t, out+errs, "--epoch <n>", "the coordinator's merge with no --epoch after a clear reads the new epoch: %d\n%s%s", code, out, errs)
	require.NotContains(t, out+errs, "cleared", "the coordinator's merge with no --epoch after a clear reads the new epoch: %d\n%s%s", code, out, errs)
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
	require.Equal(t, 1, code, "outsider merge with stale epoch: exit %d, err %q", code, errs)
	require.Contains(t, errs, "cleared at", "outsider merge with stale epoch: exit %d, err %q", code, errs)
	require.Contains(t, errs, "epoch is now 1", "outsider merge with stale epoch: exit %d, err %q", code, errs)
	require.Equal(t, before, ta.applies(), "outsider merge with stale epoch: exit %d, err %q", code, errs)
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
	require.Len(t, in.Judgments, 3, "the inbox: %+v", in)
	require.False(t, in.Done, "the inbox: %+v", in)
	require.NotEmpty(t, in.Happened, "the inbox: %+v", in)
	j1 := byStream(t, in.Judgments, "s1")
	require.NotEmpty(t, j1.ID, "the s1 judgment: %+v", j1)
	require.Equal(t, sprint.Judgment, j1.Kind, "the s1 judgment: %+v", j1)
	require.Equal(t, sprint.NWorkFailed, j1.Type, "the s1 judgment: %+v", j1)
	require.Equal(t, 2, j1.Size, "the s1 judgment: %+v", j1)
	require.Equal(t, []string{"s1-1", "s1-2"}, j1.Cards, "the s1 judgment: %+v", j1)
	require.NotEmpty(t, j1.Notes, "the s1 judgment: %+v", j1)
	require.Equal(t, "the tests went red", j1.What, "the s1 judgment: %+v", j1)
	require.NotNil(t, j1.Due, "the s1 judgment: %+v", j1)
	require.False(t, j1.Due.IsZero(), "the s1 judgment: %+v", j1)
	require.Equal(t, "2s", j1.Waited, "the s1 judgment: %+v", j1)
	j2 := byStream(t, in.Judgments, "s2")
	require.NotEmpty(t, j2.ID, "the s2 judgment: %+v", j2)
	require.Equal(t, sprint.Judgment, j2.Kind, "the s2 judgment: %+v", j2)
	require.Equal(t, sprint.NWorkFailed, j2.Type, "the s2 judgment: %+v", j2)
	require.Equal(t, 1, j2.Size, "the s2 judgment: %+v", j2)
	require.Equal(t, []string{"s2-1"}, j2.Cards, "the s2 judgment: %+v", j2)
	require.NotEmpty(t, j2.Notes, "the s2 judgment: %+v", j2)
	require.Equal(t, "abandoned idea", j2.What, "the s2 judgment: %+v", j2)
	require.NotNil(t, j2.Due, "the s2 judgment: %+v", j2)
	require.False(t, j2.Due.IsZero(), "the s2 judgment: %+v", j2)
	require.Equal(t, "0s", j2.Waited, "the s2 judgment: %+v", j2)
	var rework, drop1 []string
	for _, a := range j1.Answers {
		switch {
		case strings.HasPrefix(a.Decision, "rework"):
			rework = a.Commands
		case a.Decision == "drop":
			drop1 = a.Commands
		}
	}
	require.Len(t, rework, 1, "the s1 rework answers: %+v", j1.Answers)
	require.True(t, strings.HasPrefix(rework[0], "nova-sprint rework --group "+j1.ID+" --expect 2 --answers "), "the s1 rework answers: %+v", j1.Answers)
	require.Len(t, drop1, 1, "the s1 drop answers: %+v", j1.Answers)
	require.True(t, strings.HasPrefix(drop1[0], "nova-sprint drop --group "+j1.ID+" --expect 2 --reason '<why>' --answers "), "the s1 drop answers: %+v", j1.Answers)
	var drop2 []string
	for _, a := range j2.Answers {
		if a.Decision == "drop" {
			drop2 = a.Commands
		}
	}
	require.Len(t, drop2, 1, "the s2 drop answers: %+v", j2.Answers)
	require.True(t, strings.HasPrefix(drop2[0], "nova-sprint drop --group "+j2.ID+" --expect 1 --reason '<why>' --answers "), "the s2 drop answers: %+v", j2.Answers)
	var h inboxHappened
	for _, x := range in.Happened {
		if x.Type == sprint.NWorkOK {
			h = x
		}
	}
	require.NotEmpty(t, h.ID, "the happened note: %+v", h)
	require.Equal(t, sprint.Happened, h.Kind, "the happened note: %+v", h)
	require.Equal(t, sprint.NWorkOK, h.Type, "the happened note: %+v", h)
	require.Equal(t, "s1", h.Stream, "the happened note: %+v", h)
	require.Equal(t, []string{"s1-3"}, h.Cards, "the happened note: %+v", h)
	// the line as printed answers the judgment
	ta.ok(strings.TrimPrefix(rework[0], "nova-sprint "))
	// the drop line as printed, with '<why>' filled in, answers the judgment
	dropCmd := strings.Replace(drop2[0], "'<why>'", "'not needed'", 1)
	ta.ok(strings.TrimPrefix(dropCmd, "nova-sprint "))
	ta.json("inbox", &in)
	require.Len(t, in.Judgments, 1, "the judgments after the answers: %+v", in.Judgments)
	require.NotEqual(t, sprint.NWorkFailed, in.Judgments[0].Type, "the judgments after the answers: %+v", in.Judgments)
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
	ta.playToDone(1)
	out := ta.ok("inbox --json")
	require.Contains(t, out, `"judgments":[]`, "an empty judgments array and the done flag are printed")
	require.Contains(t, out, `"done":true`, "an empty judgments array and the done flag are printed")
	var in struct {
		Judgments []inboxJudgment
		Happened  []inboxHappened
		Done      bool
	}
	ta.json("inbox", &in)
	require.True(t, in.Done, "the inbox of a done sprint: %+v", in)
	require.Empty(t, in.Judgments, "the inbox of a done sprint: %+v", in)
	require.NotEmpty(t, in.Happened, "the inbox of a done sprint: %+v", in)
	require.Equal(t, sprint.NSprintDone, in.Happened[0].Type, "the inbox of a done sprint: %+v", in)
	require.Equal(t, "coordinator", in.Happened[0].To, "the inbox of a done sprint: %+v", in)
	require.NotEmpty(t, in.Happened[0].Hint, "the inbox of a done sprint: %+v", in)
}

// inbox --wait --json on timeout keeps stdout one JSON object, with no
// plain-text banner before it, and says the timeout in both ways: woke=false in
// the payload and the human line on stderr (stella-89ad0fb7b7c4, the one-value
// rule). The plain rendering says it on stdout.
func TestInboxWaitWithJSONOnTimeoutEmitsOnlyValidJSON(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	for _, cmd := range []string{
		"inbox --wait --timeout 50ms --json",
		"inbox --wait --json",
	} {
		code, out, errs := ta.do(cmd)
		require.Zero(t, code, "%s: %s", cmd, errs)
		require.NotContains(t, out, "inbox --wait:", "%s: plain-text banner printed before JSON", cmd)
		var in map[string]any
		require.NoError(t, json.Unmarshal([]byte(out), &in), "%s: stdout is not valid JSON:\n%s", cmd, out)
		require.Contains(t, in, "woke", "%s: the timeout is in the JSON too", cmd)
		require.Equal(t, false, in["woke"], "%s: no tick end arrived", cmd)
		require.Contains(t, errs, "inbox --wait: nothing new in", "%s: the human timeout line is on stderr", cmd)
	}
	code, out, errs := ta.do("inbox --wait --timeout 50ms")
	require.Zero(t, code, errs)
	require.Contains(t, out, "inbox --wait: nothing new in 50ms")
	require.Empty(t, errs)
}
