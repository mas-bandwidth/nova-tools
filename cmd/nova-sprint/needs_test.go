package main

import (
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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
	require.Contains(t, out, "NEEDS s1-1 ready\n", "card b")
	require.Contains(t, out, "NEEDS s1-2 ready\n", "card b")
	require.Contains(t, ta.ok("card --fields s1-1"), "NEEDED-BY b\n", "card s1-1")
	require.Contains(t, ta.ok("queue --stream s2 --col waiting"), "b work:s2:waiting waits for: s1-1,s1-2", "queue")
	code, _, errs := ta.do("queue --as m1 --col waiting")
	require.Equal(t, 2, code, "--col without --stream: %d %s", code, errs)
	require.Contains(t, errs, "--col takes waiting, with --stream", "--col without --stream: %d %s", code, errs)
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
	require.Contains(t, out, "NEEDS s1-1 off the table (dropped) waived by ", "card b")
	require.Contains(t, out, " at 20", "card b")
	ta.clean()
}

func TestAddDoesNotAdmitAWaiterOfAnInvalidID(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	code, out, errs := ta.do("add --stream s1 --needs bad.id bad.id waiter")
	require.Equal(t, 1, code, "refusal: code=%d out=%s errors=%s", code, out, errs)
	require.Contains(t, out+errs, "bad.id", "refusal: code=%d out=%s errors=%s", code, out, errs)
	require.Contains(t, out+errs, "waiter", "refusal: code=%d out=%s errors=%s", code, out, errs)
	require.NotContains(t, ta.ok("queue --stream s1 --col waiting"), "waiter work:", "admitted waiter")
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
	require.Contains(t, ta.ok("card --fields s1-1"), "HELD (e) the machine is STOPPED; the next tick: s1-1", "card s1-1")
	require.Contains(t, ta.ok("card --fields b"), "HELD (d) needs s1-1 (e)", "card b")
	require.Contains(t, ta.ok("card b --json"), `"held":{"id":"b","by":"d"`, "card b --json")
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
		assert.Contains(t, list, "\n  NEEDS "+id+"\n", "inbox --open %s does not list the need %s", g.ID, id)
	}
	assert.Contains(t, list, "... and 4 more", "the judgment's line no longer previews the needs")
	assert.NotContains(t, ta.ok("inbox"), "NEEDS ", "inbox without --open lists needs")
	var open struct {
		Needs []string `json:"needs"`
	}
	ta.json("inbox --open "+g.ID, &open)
	assert.Len(t, open.Needs, 12, "inbox --open --json needs: %v", open.Needs)
	card := ta.ok("card --fields b")
	for _, id := range ids {
		assert.Contains(t, card, "NEEDS "+id+" ", "card --fields b lacks %s", id)
	}
	ta.clean()
}
