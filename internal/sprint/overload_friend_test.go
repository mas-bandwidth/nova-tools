package sprint

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The overload alarm covers friends (docs/SPEC-SPRINT.md, "a member is overloaded"; the
// owner: "trust but VERIFY", "Are they actually doing the work that is shown in the friend
// table? Really?"): a friend up whose cards end on a timeout raises the same judgment as a
// machine, with the same numbers (three within OverloadWindow, half her width), read from
// her finishes on her own row friend.<name>, and its remedy is her width in the roster.

// openOverloads is the overload judgments open after the tick's deal part with these
// friends, applied.
func openOverloads(w *world, seats ...FriendSeat) []Note {
	w.t.Helper()
	dealWith(w, seats...)
	var out []Note
	for _, o := range w.s.Open {
		if o.Note.Type == NOverloaded {
			out = append(out, o.Note)
		}
	}
	return out
}

func TestOverloadAlarmCoversFriends(t *testing.T) {
	t.Parallel()
	w := friendWorld(t, friendBrief("friend amy"), friendBrief("friend amy"), friendBrief("friend amy"), friendBrief("friend amy"))
	up := FriendSeat{Name: "amy", Width: 4, Status: Up}
	dealWith(w, up)
	for _, id := range []string{"s1-1.w1", "s1-2.w1", "s1-3.w1", "s1-4.w1"} {
		require.Equal(t, FriendRow("amy"), w.s.Fleet.Card(id).Row)
	}
	fail := func(card, report string) {
		w.t.Helper()
		w.must(Finish(w.s, FinishReq{Sel: Sel{IDs: []string{card}}, As: FriendRow("amy"), Gens: gensOf(w.s, card), Failed: true, Report: report}))
	}
	// a friend's report that names no timeout is no timeout
	fail("s1-4.w1", "friend amy HOLD: the gate is red")
	w.tick(time.Minute)
	fail("s1-1.w1", "friend amy HOLD: deadline: the run passed its deadline")
	w.tick(time.Minute)
	fail("s1-2.w1", "friend amy FAIL: budget: unverifiable: the usage source stopped answering, tokens 12,345")
	assert.Empty(t, openOverloads(w, up), "two timeouts are not an overload")
	w.tick(time.Minute)
	fail("s1-3.w1", "friend amy HOLD: stage-timeout")
	assert.Len(t, MemberTimeouts(w.s, FriendRow("amy")), 3, "counted from her finishes on her row")

	// the same numbers as a machine's: a held or down friend raises none, as a member not up
	assert.Empty(t, openOverloads(w, FriendSeat{Name: "amy", Width: 4, Status: Held}))
	assert.Empty(t, openOverloads(w, FriendSeat{Name: "amy", Width: 4, Status: Down}))

	open := openOverloads(w, up)
	require.Len(t, open, 1, "three timeouts within the window")
	n := open[0]
	assert.Equal(t, MemberSubject(FriendRow("amy")), n.Stream)
	assert.Equal(t, "friend.amy is overloaded: 3 cards ended on a timeout in the last 15m0s: s1-1.w1 (deadline), s1-2.w1 (usage source), s1-3.w1 (stage-timeout); halve its width: nova-config friend set amy --width 2, then nova-sprint friend sync, or wait 15m", n.What)
	assert.Equal(t, []string{"friend set amy --width 2", "wait 15m"}, n.Decisions)
	cmds := NoteCommands(n, nil)
	require.Len(t, cmds, 2)
	assert.Equal(t, []string{"nova-config friend set amy --width 2", "nova-sprint friend sync"}, cmds[0].Lines)
	assert.Len(t, openOverloads(w, up), 1, "raised once while it holds")

	// the window moves past the first timeout: two remain, and the judgment closes
	w.tick(OverloadWindow - time.Minute)
	assert.Empty(t, openOverloads(w, up), "the window holds two")
}
