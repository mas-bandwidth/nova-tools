package main

import (
	"strconv"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// H11: card shows each need with its state and what needs the card; queue
// --stream <s> --col waiting shows what each waiting card still waits for.
func TestTheReadsShowTheNeeds(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("add --stream s1 --count 2")
	ta.ok("add --stream s2 b --needs s1-1,s1-2")
	out := ta.ok("card --fields b")
	if !strings.Contains(out, "NEEDS s1-1 ready\n") || !strings.Contains(out, "NEEDS s1-2 ready\n") {
		t.Fatalf("card b: %s", out)
	}
	if out := ta.ok("card --fields s1-1"); !strings.Contains(out, "NEEDED-BY b\n") {
		t.Fatalf("card s1-1: %s", out)
	}
	if out := ta.ok("queue --stream s2 --col waiting"); !strings.Contains(out, "b work:s2:waiting waits for: s1-1,s1-2") {
		t.Fatalf("queue: %s", out)
	}
	if code, _, errs := ta.do("queue --as m1 --col waiting"); code != 2 || !strings.Contains(errs, "--col takes waiting, with --stream") {
		t.Fatalf("--col without --stream: %d %s", code, errs)
	}
	ta.clean()
}

// card shows each waived need, by whom and when it was waived.
func TestCardShowsTheWaivedNeeds(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("add --stream s1 --count 1")
	ta.ok("add --stream s2 b --needs s1-1")
	ta.ok("drop s1-1 --reason obsolete")
	g := ta.group(sprint.NBlocked, "s2")
	ta.ok("ack " + g.Notes[0] + " --reason fine")
	out := ta.ok("card --fields b")
	if !strings.Contains(out, "NEEDS s1-1 off the table (dropped) waived by ") || !strings.Contains(out, " at 20") {
		t.Fatalf("card b: %s", out)
	}
	ta.clean()
}

func TestAddDoesNotAdmitAWaiterOfAnInvalidID(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	code, out, errs := ta.do("add --stream s1 --needs bad.id bad.id waiter")
	if code != 1 || !strings.Contains(out+errs, "bad.id") || !strings.Contains(out+errs, "waiter") {
		t.Fatalf("refusal: code=%d out=%s errors=%s", code, out, errs)
	}
	if out := ta.ok("queue --stream s1 --col waiting"); strings.Contains(out, "waiter work:") {
		t.Fatalf("admitted waiter: %s", out)
	}
	ta.clean()
}

// card prints what holds the primary now (check rule 12): with the machine
// STOPPED, the next tick's deal, and behind it the need that tick moves.
func TestTheCardSaysWhatHoldsIt(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("add --stream s1 --count 1")
	ta.ok("add --stream s2 b --needs s1-1")
	if out := ta.ok("card --fields s1-1"); !strings.Contains(out, "HELD (e) the machine is STOPPED; the next tick: s1-1") {
		t.Fatalf("card s1-1: %s", out)
	}
	if out := ta.ok("card --fields b"); !strings.Contains(out, "HELD (d) needs s1-1 (e)") {
		t.Fatalf("card b: %s", out)
	}
	if out := ta.ok("card b --json"); !strings.Contains(out, `"held":{"id":"b","by":"d"`) {
		t.Fatalf("card b --json: %s", out)
	}
}

// inbox --open lists the whole needs of a blocked judgment, one per line, as
// card --fields does; the judgment's own line previews them.
func TestInboxOpenListsEveryDroppedNeed(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("add --stream s1 --count 12")
	var ids []string
	for i := 1; i <= 12; i++ {
		ids = append(ids, "s1-"+strconv.Itoa(i))
	}
	ta.ok("add --stream s2 b --needs " + strings.Join(ids, ","))
	ta.ok("drop " + strings.Join(ids, " ") + " --reason obsolete")
	g := ta.group(sprint.NBlocked, "s2")
	list := ta.ok("inbox --open " + g.ID)
	for _, id := range ids {
		if !strings.Contains(list, "\n  NEEDS "+id+"\n") {
			t.Errorf("inbox --open %s does not list the need %s:\n%s", g.ID, id, list)
		}
	}
	if !strings.Contains(list, "... and 4 more") {
		t.Errorf("the judgment's line no longer previews the needs:\n%s", list)
	}
	if plain := ta.ok("inbox"); strings.Contains(plain, "NEEDS ") {
		t.Errorf("inbox without --open lists needs:\n%s", plain)
	}
	var open struct {
		Needs []string `json:"needs"`
	}
	ta.json("inbox --open "+g.ID, &open)
	if len(open.Needs) != 12 {
		t.Errorf("inbox --open --json needs: %v", open.Needs)
	}
	card := ta.ok("card --fields b")
	for _, id := range ids {
		if !strings.Contains(card, "NEEDS "+id+" ") {
			t.Errorf("card --fields b lacks %s", id)
		}
	}
	ta.clean()
}
