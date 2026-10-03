package main

import (
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// inboxGroups is the inbox as a program reads it.
func (ta *testApp) inboxGroups() []sprint.Group {
	ta.t.Helper()
	var in struct{ Groups []sprint.Group }
	ta.json("inbox", &in)
	return in.Groups
}

// group is the inbox group of a type and stream.
func (ta *testApp) group(typ, stream string) sprint.Group {
	ta.t.Helper()
	for _, g := range ta.inboxGroups() {
		if g.Type == typ && g.Stream == stream {
			return g
		}
	}
	ta.t.Fatalf("no group %s %s in %+v", typ, stream, ta.inboxGroups())
	return sprint.Group{}
}

// failOnce takes and finishes the member's ready work card of a primary as
// failed.
func (ta *testApp) failOnce(member, card, report string) {
	ta.t.Helper()
	ta.a.sleep(time.Second) // each notification at its own clock reading
	ta.ok("take --as " + member + " " + card)
	ta.ok("finish --as " + member + " " + card + " --failed --report '" + report + "'")
}

// I1: a group is named by an id that does not move when a newer group sorts
// ahead of it; a number is refused and nothing changes.
func TestAGroupIsNamedByItsIDNeverItsPosition(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("add --stream s1 --count 1")
	ta.ok("add --stream s2 --count 1")
	ta.deal(2)
	ta.failOnce("m1", "s1-1.w1@1", "tests red")
	first := ta.group(sprint.NWorkFailed, "s1")
	require.NotEmpty(t, first.ID, "the first group: %+v", first)
	require.Equal(t, 1, first.Size, "the first group: %+v", first)
	require.Equal(t, "s1-1", first.Primaries[0], "the first group: %+v", first)
	// s2-1 fails twice for the same cause: a marked group, sorted first.
	ta.failOnce("m1", "s2-1.w1@1", "tests red")
	ta.ok("rework s2-1 --fix 'try again'")
	ta.failOnce("m1", "s2-1.w2@1", "tests red")
	gs := ta.inboxGroups()
	require.Equal(t, "s2", gs[0].Stream, "the repeat is not first: %+v", gs)
	require.True(t, gs[0].Marked, "the repeat is not first: %+v", gs)
	require.Equal(t, first.ID, ta.group(sprint.NWorkFailed, "s1").ID, "the id moved")
	code, out, errs := ta.do("rework --group 1 --fix 'the fix'")
	require.Equal(t, 2, code, "a group number: %d\n%s%s", code, out, errs)
	require.Contains(t, errs, "group numbers are not accepted", "a group number: %d\n%s%s", code, out, errs)
	require.Contains(t, errs, first.ID+" (work came back failed, s1, size 1)", "a group number: %d\n%s%s", code, out, errs)
	require.NotContains(t, out, "MOVED", "a group number: %d\n%s%s", code, out, errs)
	code, _, errs = ta.do("inbox --open 2")
	require.Equal(t, 2, code, "inbox --open 2: %d %s", code, errs)
	require.Contains(t, errs, "group numbers are not accepted", "inbox --open 2: %d %s", code, errs)
	out = ta.ok("rework --group " + first.ID + " --fix 'the fix'")
	require.Contains(t, out, "MOVED s1-1 work review -> working (rework)", "rework by id")
	require.NotContains(t, out, "s2-1", "rework by id")
	code, _, errs = ta.do("rework --group " + first.ID + " --fix 'the fix'")
	require.Equal(t, 1, code, "an answered group: %d %s", code, errs)
	require.Contains(t, errs, "no inbox group "+first.ID+" now", "an answered group: %d %s", code, errs)
	ta.clean()
}

// I2: --expect refuses a group whose size changed, naming what changed, and
// nothing moves; the verb says how many it acted on.
func TestAGroupOfAnotherSizeThanPrintedIsRefused(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("add --stream s1 --count 3")
	ta.deal(3)
	ta.failOnce("m1", "s1-1.w1@1", "tests red")
	g := ta.group(sprint.NWorkFailed, "s1")
	sizePrinted := g.Size
	ta.failOnce("m1", "s1-2.w1@1", "tests red")
	now := ta.group(sprint.NWorkFailed, "s1")
	require.Equal(t, g.ID, now.ID, "the group grew under another id or size: %+v", now)
	require.Equal(t, 2, now.Size, "the group grew under another id or size: %+v", now)
	code, out, errs := ta.do("drop --group " + g.ID + " --expect 1 --reason obsolete --answers " + strings.Join(g.Notes, ","))
	require.Equal(t, 1, code, "a grown group: %d\n%s%s", code, out, errs)
	require.Contains(t, errs, "REFUSED group "+g.ID+": it has 2 now, not 1 as printed; nothing changed", "a grown group: %d\n%s%s", code, out, errs)
	require.Contains(t, errs, "ADDED s1-2", "a grown group: %d\n%s%s", code, out, errs)
	require.NotContains(t, errs, "ADDED s1-1", "a grown group: %d\n%s%s", code, out, errs)
	require.Contains(t, errs, "DROP FAIL moved=0", "a grown group: %d\n%s%s", code, out, errs)
	require.NotContains(t, out, "MOVED", "a grown group: %d\n%s%s", code, out, errs)
	code, _, errs = ta.do("drop --group " + g.ID + " --expect 1 --reason obsolete")
	require.Equal(t, 1, code, "a grown group, no --answers: %d %s", code, errs)
	require.Contains(t, errs, "NOW s1-1", "a grown group, no --answers: %d %s", code, errs)
	require.Contains(t, errs, "NOW s1-2", "a grown group, no --answers: %d %s", code, errs)
	require.Equal(t, 2, ta.group(sprint.NWorkFailed, "s1").Size, "a refused verb changed the group")
	_ = sizePrinted
	out = ta.ok("rework --group " + g.ID + " --expect 2 --fix 'the fix'")
	require.Contains(t, out, "GROUP "+g.ID+" acted on 2, the group had 2 when printed", "rework --expect")
	ta.failOnce("m1", "s1-3.w1@1", "tests red")
	g = ta.group(sprint.NWorkFailed, "s1")
	out = ta.ok("rework --group " + g.ID + " --fix 'the fix'")
	require.Contains(t, out, "GROUP "+g.ID+" acted on 1\n", "rework without --expect")
	code, _, errs = ta.do("rework s1-1 --expect 2 --fix x")
	require.Equal(t, 2, code, "--expect without --group: %d %s", code, errs)
	require.Contains(t, errs, "--expect goes with --group", "--expect without --group: %d %s", code, errs)
	ta.clean()
}

// I4: rework with no --fix takes each primary's own: the finding of its
// broken read, the report of its failed work; a primary with neither is
// refused by name; --fix is for all.
func TestReworkTakesTheFindingOrTheReport(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("add --stream s1 --count 4")
	ta.deal(4)
	ta.ok("take --as m1 --limit 4")
	ta.ok("finish --as m1 s1-1.w1@1 s1-3.w1@1 s1-4.w1@1")
	ta.ok("finish --as m1 s1-2.w1@1 --failed --report 'the tests went red'")
	ta.ok("ask")
	ta.ok("read --as reader-a --broken --finding 'the empty case is not handled' s1-1.r1.reader-a")
	broken := ta.group(sprint.NReadBroken, "s1")
	// all or nothing: s1-3 has no finding, report or --fix, so nothing moves
	code, out, errs := ta.do("rework s1-1 s1-2 s1-3 --answers " + broken.ID)
	require.Equal(t, 1, code, "rework with no --fix for one of three: %d\n%s%s", code, out, errs)
	require.Contains(t, errs, "REFUSED s1-3: no --fix, and no finding of a broken read or report of failed work", "rework with no --fix for one of three: %d\n%s%s", code, out, errs)
	require.Contains(t, errs, "REFUSED s1-1: not written: the verb names several and applies all or none", "rework with no --fix for one of three: %d\n%s%s", code, out, errs)
	require.Contains(t, errs, "REFUSED s1-2: not written: the verb names several and applies all or none", "rework with no --fix for one of three: %d\n%s%s", code, out, errs)
	require.NotContains(t, out, "MOVED", "rework with no --fix for one of three: %d\n%s%s", code, out, errs)
	for _, id := range []string{"s1-1", "s1-2", "s1-3"} {
		require.Contains(t, ta.ok("card --fields "+id), "place=s1:review ", "%s moved by a refused rework", id)
	}
	require.Equal(t, broken.ID, ta.group(sprint.NReadBroken, "s1").ID, "the refused rework answered the judgment")
	out = ta.ok("rework s1-1 s1-2 --answers " + broken.ID)
	require.Contains(t, out, "MOVED s1-1 work review -> working (rework)", "rework with the finding and the report")
	require.Contains(t, out, "MOVED s1-2 work review -> working (rework)", "rework with the finding and the report")
	for id, want := range map[string]string{"s1-1": `fix=the\x20empty\x20case\x20is\x20not\x20handled`, "s1-2": `fix=the\x20tests\x20went\x20red`} {
		require.Contains(t, ta.ok("card --fields "+id), want, "%s: no %s in its card", id, want)
	}
	out = ta.ok("rework s1-3 s1-4 --fix 'handle the empty case'")
	require.Contains(t, out, "moved=2", "rework --fix")
	require.Contains(t, ta.ok("card --fields s1-4"), `fix=handle\x20the\x20empty\x20case`, "--fix for all")
	ta.clean()
}

// toMerging brings every primary of the streams to merging queued.
func (ta *testApp) toMerging(streams ...string) {
	ta.t.Helper()
	ta.ok("init --readers reader-a,reader-b --members m1")
	for _, s := range streams {
		ta.ok("add --stream " + s + " --count 3")
	}
	ta.deal(100)
	ta.ok("take --as m1 --limit 100")
	var q struct{ Cards []queueCard }
	ta.json("queue --as m1", &q)
	var words []string
	for _, c := range q.Cards {
		words = append(words, c.ID+"@"+strconv.Itoa(c.Gen))
	}
	ta.ok("finish --as m1 " + strings.Join(words, " "))
	ta.ok("ask --limit 100")
	ta.ok("read --as reader-a --ok --limit 100")
	ta.ok("read --as reader-b --ok --limit 100")
	ta.ok("accept --read-ok")
}

// I5: a red branch names the suspects given, or says none was and gives the
// batch's first and last card and the command that lists it; a suspect is a
// card of the batch.
func TestRedNamesWhatItKnows(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.toMerging("s1", "s2")
	code, _, errs := ta.do("merge --stream s1 --red --suspect s2-1")
	require.Equal(t, 1, code, "a suspect outside the batch: %d %s", code, errs)
	require.Contains(t, errs, "REFUSED s2-1: a suspect is a card of the batch; the batch of 3 is s1-1 .. s1-3", "a suspect outside the batch: %d %s", code, errs)
	code, _, errs = ta.do("merge --stream s1 --suspect s1-2")
	require.Equal(t, 2, code, "--suspect without --red: %d %s", code, errs)
	require.Contains(t, errs, "--suspect goes with --red", "--suspect without --red: %d %s", code, errs)
	ta.ok("merge --stream s1 --red --suspect s1-2 s1-3")
	g := ta.group(sprint.NRed, "s1")
	require.Equal(t, []string{"s1-2", "s1-3"}, g.Suspects, "named suspects: %+v", g)
	require.Equal(t, "suspects: s1-2, s1-3 (of the batch of 3)", g.What, "named suspects: %+v", g)
	ta.ok("merge --stream s2 --red")
	g = ta.group(sprint.NRed, "s2")
	require.Empty(t, g.Suspects, "no suspect: %+v", g)
	require.Equal(t, "no suspect named; the batch of 3 is s2-1 .. s2-3; list it: nova-sprint queue --stream s2 --max 3", g.What, "no suspect: %+v", g)
	ta.clean()
}

// I3: every judgment prints its decisions as commands, filled in and ready
// to copy; the ones with no placeholder run as printed and answer it.
func TestEveryJudgmentPrintsItsDecisionsAsCommands(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("add --stream s1 --count 3")
	ta.ok("add --stream s2 --count 2")
	ta.deal(100)
	ta.ok("take --as m1 --limit 100")
	ta.ok("finish --as m1 s1-1.w1@1 s1-2.w1@1 s1-3.w1@1 s2-2.w1@1")
	ta.a.sleep(time.Second)
	ta.ok("finish --as m1 s2-1.w1@1 --failed --report 'the tests went red'")
	ta.ok("ask --limit 100")
	ta.a.sleep(time.Second)
	ta.ok("read --as reader-a --broken --finding 'the empty case is not handled' s2-2.r1.reader-a")
	ta.ok("read --as reader-a --ok --limit 100")
	ta.ok("read --as reader-b --ok --limit 100")
	ta.ok("accept --read-ok")
	ta.a.sleep(time.Second)
	ta.ok("merge --stream s1 --red --suspect s1-2")
	ta.a.sleep(time.Minute)
	gs := ta.inboxGroups()
	out := ta.ok("inbox")
	// the golden text, with the notification ids named in the order found
	golden := out
	for i, g := range gs {
		for _, n := range g.Notes {
			golden = strings.ReplaceAll(golden, n, "N"+strconv.Itoa(i+1))
		}
	}
	golden = regexp.MustCompile(`due=\d\d:\d\d:\d\d`).ReplaceAllString(golden, "due=HH:MM:SS") // the local zone
	want := `JUDGMENT N1   work came back failed  stream=s2  size=1  waited=1m2s  due=HH:MM:SS  (s2-1)  the tests went red
  rework with a fix:
    nova-sprint rework --group N1 --expect 1 --answers N1
  drop:
    nova-sprint drop --group N1 --expect 1 --reason '<why>' --answers N1
JUDGMENT N2   a reader found it broken  stream=s2  size=1  waited=1m1s  due=HH:MM:SS  (s2-2)  the empty case is not handled
  rework with the finding:
    nova-sprint rework --group N2 --expect 1 --answers N2
  ask another reader:
    nova-sprint ask --group N2 --expect 1 --another --answers N2
  drop:
    nova-sprint drop --group N2 --expect 1 --reason '<why>' --answers N2
JUDGMENT N3   stream stopped: stream branch red  stream=s1  size=3  waited=1m0s  due=HH:MM:SS  (s1-1,s1-2,s1-3)  suspects: s1-2 (of the batch of 3)
  take the suspect off and resume:
    nova-sprint return s1-2 --reason 'suspect of the red batch' --answers N3
    nova-sprint resume --stream s1 --did 'returned s1-2' --answers N3
  rework the suspect:
    nova-sprint return s1-2 --reason 'suspect of the red batch' --answers N3
    nova-sprint rework s1-2 --fix '<fix>'
    nova-sprint resume --stream s1 --did 'returned s1-2 for rework' --answers N3
  resume with what you did:
    nova-sprint resume --stream s1 --did '<what you did>' --answers N3
`
	require.True(t, strings.HasPrefix(golden, want), "the inbox:\n%s\nwant it to begin:\n%s", golden, want)
	// the commands with nothing to fill in run as printed
	run := func(g sprint.Group, decision string) {
		for _, c := range g.Commands {
			if c.Decision != decision {
				continue
			}
			for _, l := range c.Lines {
				ta.ok(strings.TrimPrefix(l, "nova-sprint "))
			}
			return
		}
		t.Fatalf("no decision %q in %+v", decision, g.Commands)
	}
	run(gs[0], "rework with a fix")
	run(gs[1], "rework with the finding")
	run(gs[2], "take the suspect off and resume")
	out = ta.ok("inbox")
	require.False(t, openBesidesReturned(out), "a judgment is still open after its commands ran:\n%s", out)
	ta.clean()
}

// openBesidesReturned says the inbox shows a judgment other than returned to
// review: a card taken off a stopped stream is back in review, and that
// judgment asks the coordinator to decide it again.
func openBesidesReturned(out string) bool {
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "JUDGMENT") && !strings.Contains(l, sprint.NReturned) {
			return true
		}
	}
	return false
}

// I3: a repeat's "stop and look" lists its cards, cut at MaxLook with the
// command that lists them all.
func TestStopAndLookListsTheCardsThenTheWholeGroup(t *testing.T) {
	t.Parallel()
	g := sprint.Inbox(sprint.InboxReq{Now: t0, Open: func() []sprint.Open {
		var out []sprint.Open
		for i := 1; i <= 7; i++ {
			n := sprint.Note{ID: "n1", Kind: sprint.Judgment, Type: sprint.NWorkFailed, Stream: "s1", At: t0, Marked: true,
				Decisions: []string{"rework with a fix", "drop", sprint.RepeatDecision}}
			out = append(out, sprint.Open{Key: sprint.OpenKey("n1", "s1-"+strconv.Itoa(i)), Note: n})
		}
		return out
	}()})
	look := g[0].Commands[2]
	require.Equal(t, sprint.RepeatDecision, look.Decision, "stop and look: %+v", look)
	require.Len(t, look.Lines, sprint.MaxLook+1, "stop and look: %+v", look)
	require.Equal(t, "nova-sprint card s1-1", look.Lines[0], "stop and look: %+v", look)
	require.Equal(t, "nova-sprint inbox --open n1", look.Lines[sprint.MaxLook], "stop and look: %+v", look)
}

// I3: a stopped stream's decisions run as printed once the placeholders are
// filled, and each one answers the stop.
func TestAStoppedStreamsCommandsRunAndAnswerIt(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ fact, typ, decision string }{
		{"--conflict s1-2", sprint.NConflict, "rework"},
		{"--conflict s1-2", sprint.NConflict, "drop"},
		{"--conflict s1-2", sprint.NConflict, "resolve and resume"},
		{"--cross s1-1=s2-1", sprint.NCross, "return"},
		{"--cross s1-1=s2-1", sprint.NCross, "drop"},
		{"--rejected", sprint.NRejected, "return"},
		{"--rejected", sprint.NRejected, "drop"},
		{"--rejected", sprint.NRejected, "resume"},
		{"--red", sprint.NRed, "rework the suspect"},
		{"--red", sprint.NRed, "resume with what you did"},
	} {
		t.Run(c.typ+"/"+c.decision, func(t *testing.T) {
			t.Parallel()
			ta := newTestApp(t)
			ta.toMerging("s1", "s2")
			ta.ok("merge --stream s1 " + c.fact)
			g := ta.group(c.typ, "s1")
			fill := strings.NewReplacer("'<fix>'", "x", "'<why>'", "x", "'<what you did>'", "x", "'<suspect>'", "s1-3")
			found := false
			for _, cmd := range g.Commands {
				if cmd.Decision != c.decision {
					continue
				}
				found = true
				for _, l := range cmd.Lines {
					if strings.Contains(l, " queue ") {
						continue
					}
					ta.ok(fill.Replace(strings.TrimPrefix(l, "nova-sprint ")))
				}
			}
			require.True(t, found, "no decision %q: %+v", c.decision, g.Commands)
			out := ta.ok("inbox")
			require.False(t, openBesidesReturned(out), "still open:\n%s", out)
			ta.clean()
		})
	}
}

// I7: help and help inbox show the worked example: reading the inbox and an
// answer to each judgment type.
func TestHelpShowsTheWorkedExample(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	for _, line := range []string{"help", "help inbox"} {
		out := ta.ok(line)
		for _, want := range []string{"reading the inbox and answering a judgment:", "$ nova-sprint inbox",
			"nova-sprint rework --group finish-0314a1b2-1.1 --expect 2 --answers finish-0314a1b2-1.1", "a group number is refused",
			"one answer to each judgment"} {
			require.Contains(t, out, want, "%s lacks %q", line, want)
		}
		// the sprint done is no judgment of the tick (errata 3 amendment 6):
		// help shows it as the HAPPENED line it is, above the answers
		want := len(sprint.Decisions) + 1
		if _, ok := sprint.Decisions[sprint.NSprintDone]; ok {
			want--
		}
		answers, _, _ := strings.Cut(out[strings.Index(out, "one answer to each judgment"):], "\n\n")
		require.Equal(t, want, strings.Count(answers, "\n  "), "%s: answers for %d judgment types and the repeat", line, want-1)
		require.Contains(t, out, "  HAPPENED tick-done-0317a1b2-1.1   the sprint is done  x1  for=coordinator", "%s does not show the sprint done", line)
	}
	out := ta.ok("help inbox")
	require.True(t, strings.HasPrefix(out, "usage: nova-sprint inbox [--open <group>]"), "help inbox: %s", out)
	require.Contains(t, out, "--open <string>", "help inbox")
}
