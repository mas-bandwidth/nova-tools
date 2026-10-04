package main

import (
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
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

// drop of a needed card is refused unless --cascade is given.
func TestDropRefusesNeededCard(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("add --stream s1 --count 1")
	ta.ok("add --stream s2 b --needs s1-1")
	// drop s1-1 without cascade should be refused
	code, out, errs := ta.do("drop s1-1 --reason obsolete")
	require.Equal(t, 1, code, "drop of needed card should be refused: exit=%d out=%s err=%s", code, out, errs)
	require.Contains(t, out+errs, "s1-1 is needed by b", "refusal should name dependant b")
	require.Contains(t, out+errs, "drop them too with --cascade", "refusal should suggest cascade")
	// drop with cascade should succeed
	ta.ok("drop s1-1 --reason obsolete --cascade")
	// both s1-1 and b should be dropped
	require.Contains(t, ta.ok("card --fields s1-1"), "dropped", "s1-1 should be dropped")
	require.Contains(t, ta.ok("card --fields b"), "dropped", "b should be dropped")
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

// Dropping needed cards is refused unless --cascade is used.
func TestDropRefusesNeededCardsForInbox(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("add --stream s1 --count 12")
	var ids []string
	for i := 1; i <= 12; i++ {
		ids = append(ids, "s1-"+strconv.Itoa(i))
	}
	ta.ok("add --stream s2 b --needs " + strings.Join(ids, ","))
	// dropping all needed cards without cascade should be refused
	code, out, errs := ta.do("drop " + strings.Join(ids, " ") + " --reason obsolete")
	require.Equal(t, 1, code, "drop of needed cards should be refused: exit=%d out=%s err=%s", code, out, errs)
	for _, id := range ids {
		require.Contains(t, out+errs, id+" is needed by b", "refusal should name dependant for %s", id)
		require.Contains(t, out+errs, "--cascade", "refusal should suggest cascade for %s", id)
	}
	// dropping with cascade should succeed and drop b too
	ta.ok("drop " + strings.Join(ids, " ") + " --reason obsolete --cascade")
	require.Contains(t, ta.ok("card --fields b"), "dropped", "b should be dropped")
	ta.clean()
}
