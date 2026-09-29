package main

import (
	"strings"
	"testing"
	"time"

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
	ta.ok("start --limit 2")
	ta.failOnce("m1", "s1-1.w1@1", "tests red")
	first := ta.group(sprint.NWorkFailed, "s1")
	if first.ID == "" || first.Size != 1 || first.Primaries[0] != "s1-1" {
		t.Fatalf("the first group: %+v", first)
	}
	// s2-1 fails twice for the same cause: a marked group, sorted first.
	ta.failOnce("m1", "s2-1.w1@1", "tests red")
	ta.ok("rework s2-1 --fix 'try again'")
	ta.failOnce("m1", "s2-1.w2@1", "tests red")
	gs := ta.inboxGroups()
	if gs[0].Stream != "s2" || !gs[0].Marked {
		t.Fatalf("the repeat is not first: %+v", gs)
	}
	if again := ta.group(sprint.NWorkFailed, "s1"); again.ID != first.ID {
		t.Fatalf("the id moved: %s then %s", first.ID, again.ID)
	}
	code, out, errs := ta.do("rework --group 1 --fix 'the fix'")
	if code != 2 || !strings.Contains(errs, "group numbers are not accepted") || !strings.Contains(errs, first.ID+" (work came back failed, s1, size 1)") || strings.Contains(out, "MOVED") {
		t.Fatalf("a group number: %d\n%s%s", code, out, errs)
	}
	if code, _, errs := ta.do("inbox --open 2"); code != 2 || !strings.Contains(errs, "group numbers are not accepted") {
		t.Fatalf("inbox --open 2: %d %s", code, errs)
	}
	out = ta.ok("rework --group " + first.ID + " --fix 'the fix'")
	if !strings.Contains(out, "MOVED s1-1 review -> working (rework)") || strings.Contains(out, "s2-1") {
		t.Fatalf("rework by id: %s", out)
	}
	if code, _, errs := ta.do("rework --group " + first.ID + " --fix 'the fix'"); code != 1 || !strings.Contains(errs, "no inbox group "+first.ID+" now") {
		t.Fatalf("an answered group: %d %s", code, errs)
	}
	ta.clean()
}

// I2: --expect refuses a group whose size changed, naming what changed, and
// nothing moves; the verb says how many it acted on.
func TestAGroupOfAnotherSizeThanPrintedIsRefused(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("add --stream s1 --count 3")
	ta.ok("start --limit 3")
	ta.failOnce("m1", "s1-1.w1@1", "tests red")
	g := ta.group(sprint.NWorkFailed, "s1")
	printed := g.Size
	ta.failOnce("m1", "s1-2.w1@1", "tests red")
	now := ta.group(sprint.NWorkFailed, "s1")
	if now.ID != g.ID || now.Size != 2 {
		t.Fatalf("the group grew under another id or size: %+v", now)
	}
	code, out, errs := ta.do("drop --group " + g.ID + " --expect 1 --reason obsolete --answers " + strings.Join(g.Notes, ","))
	if code != 1 || !strings.Contains(errs, "REFUSED group "+g.ID+": it has 2 now, not 1 as printed; nothing changed") ||
		!strings.Contains(errs, "ADDED s1-2") || strings.Contains(errs, "ADDED s1-1") || !strings.Contains(errs, "DROP FAIL moved=0") || strings.Contains(out, "MOVED") {
		t.Fatalf("a grown group: %d\n%s%s", code, out, errs)
	}
	if code, _, errs := ta.do("drop --group " + g.ID + " --expect 1 --reason obsolete"); code != 1 || !strings.Contains(errs, "NOW s1-1") || !strings.Contains(errs, "NOW s1-2") {
		t.Fatalf("a grown group, no --answers: %d %s", code, errs)
	}
	if ta.group(sprint.NWorkFailed, "s1").Size != 2 {
		t.Fatalf("a refused verb changed the group")
	}
	_ = printed
	out = ta.ok("rework --group " + g.ID + " --expect 2 --fix 'the fix'")
	if !strings.Contains(out, "GROUP "+g.ID+" acted on 2, the group had 2 when printed") {
		t.Fatalf("rework --expect: %s", out)
	}
	ta.failOnce("m1", "s1-3.w1@1", "tests red")
	g = ta.group(sprint.NWorkFailed, "s1")
	out = ta.ok("rework --group " + g.ID + " --fix 'the fix'")
	if !strings.Contains(out, "GROUP "+g.ID+" acted on 1\n") {
		t.Fatalf("rework without --expect: %s", out)
	}
	if code, _, errs := ta.do("rework s1-1 --expect 2 --fix x"); code != 2 || !strings.Contains(errs, "--expect goes with --group") {
		t.Fatalf("--expect without --group: %d %s", code, errs)
	}
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
	ta.ok("start --limit 4")
	ta.ok("take --as m1 --limit 4")
	ta.ok("finish --as m1 s1-1.w1@1 s1-3.w1@1 s1-4.w1@1")
	ta.ok("finish --as m1 s1-2.w1@1 --failed --report 'the tests went red'")
	ta.ok("ask")
	ta.ok("read --as reader-a --broken --finding 'the empty case is not handled' s1-1.r1.reader-a")
	broken := ta.group(sprint.NReadBroken, "s1")
	code, out, errs := ta.do("rework s1-1 s1-2 s1-3 --answers " + broken.ID)
	if code != 1 || !strings.Contains(errs, "REFUSED s1-3: no --fix, and no finding of a broken read or report of failed work") ||
		!strings.Contains(out, "MOVED s1-1 review -> working (rework)") || !strings.Contains(out, "MOVED s1-2 review -> working (rework)") {
		t.Fatalf("rework with no --fix: %d\n%s%s", code, out, errs)
	}
	for id, want := range map[string]string{"s1-1": `fix=the\x20empty\x20case\x20is\x20not\x20handled`, "s1-2": `fix=the\x20tests\x20went\x20red`} {
		if out := ta.ok("card " + id); !strings.Contains(out, want) {
			t.Fatalf("%s: no %s in\n%s", id, want, out)
		}
	}
	out = ta.ok("rework s1-3 s1-4 --fix 'handle the empty case'")
	if !strings.Contains(out, "moved=2") {
		t.Fatalf("rework --fix: %s", out)
	}
	if out := ta.ok("card s1-4"); !strings.Contains(out, `fix=handle\x20the\x20empty\x20case`) {
		t.Fatalf("--fix for all: %s", out)
	}
	ta.clean()
}
