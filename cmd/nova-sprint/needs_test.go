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
	ta.ok("add --stream s2 b --one --needs s1-1,s1-2")
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

// Drop refuses a card a waiting card still needs (docs/SPEC-SPRINT.md section
// 11): the refusal names the card and its dependants, nothing is written, and
// the need stays on the table. The waiver of a dropped need is pinned at the
// store, where a stored dropped record still opens the blocked judgment (store
// TestAddOnDroppedNeedAndWaive); the verbs refuse to make that state.
func TestCardShowsTheWaivedNeeds(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("add --stream s1 --count 1 --one")
	ta.ok("add --stream s2 b --one --needs s1-1")
	before := ta.applies()
	code, out, errs := ta.do("drop s1-1 --reason obsolete")
	require.Equal(t, 1, code, "drop of a needed card: %s%s", out, errs)
	require.Contains(t, errs, "s1-1 is needed by b; drop them too with --cascade", "drop of a needed card: %s", errs)
	require.Equal(t, before, ta.applies(), "a refused drop wrote")
	require.Contains(t, ta.ok("card --fields s1-1"), "place=s1:ready", "a refused drop moved s1-1")
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
	ta.ok("add --stream s1 --count 1 --one")
	ta.ok("add --stream s2 b --one --needs s1-1")
	require.Contains(t, ta.ok("card --fields s1-1"), "HELD (e) the machine is STOPPED; the next tick: s1-1", "card s1-1")
	require.Contains(t, ta.ok("card --fields b"), "HELD (d) needs s1-1 (e)", "card b")
	require.Contains(t, ta.ok("card b --json"), `"held":{"id":"b","by":"d"`, "card b --json")
}

// Drop refuses every card a waiting card still needs (docs/SPEC-SPRINT.md
// section 11): dropping the twelve needs of b names each one with its
// dependant, writes nothing, and opens no blocked judgment for the inbox.
// The inbox listing of every dropped need of one judgment is pinned at the
// store, where a stored dropped record still opens it.
func TestInboxOpenListsEveryDroppedNeed(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("add --stream s1 --count 12")
	var ids []string
	for i := 1; i <= 12; i++ {
		ids = append(ids, "s1-"+strconv.Itoa(i))
	}
	ta.ok("add --stream s2 b --one --needs " + strings.Join(ids, ","))
	before := ta.applies()
	code, out, errs := ta.do("drop " + strings.Join(ids, " ") + " --reason obsolete")
	require.Equal(t, 1, code, "drop of needed cards: %s%s", out, errs)
	for _, id := range ids {
		assert.Contains(t, errs, id, "the refusal names %s: %s", id, errs)
	}
	assert.Contains(t, errs, "--cascade", "the refusal names the remedy: %s", errs)
	require.Equal(t, before, ta.applies(), "a refused drop wrote")
	for _, g := range ta.inboxGroups() {
		assert.False(t, g.Kind == sprint.Judgment && g.Type == sprint.NBlocked, "a refused drop opened a blocked judgment: %+v", g)
	}
	ta.clean()
}
