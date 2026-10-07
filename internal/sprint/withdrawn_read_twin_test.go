package sprint_test

import (
	"strings"
	"testing"
	"time"

	"github.com/nova-tools/internal/sprint"
	"github.com/nova-tools/internal/sprint/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A withdrawn read is not outstanding (docs/SPEC-SPRINT.md, a read asked of any
// unit with room at or above the read tier): a read card taken back from its
// reader, withdrawn on a friend's row or retired on the readers table, is history,
// and the primary in review is asked of another reader in the next tick. On
// 2026-10-06 at 5:45 PM ET friend reads withdrawn when Freddy's reader role was
// removed held 67 flash cards in review: the ask counted each as outstanding
// ("asked already"), --another found none asked, and the check raised rule 2.

// reviewRig is the hold rig with s1-1 worked on m1 and in review, its first read
// asked of friend amy (the friends held while it was worked, so a machine did it).
func reviewRig(t *testing.T) *holdRig {
	t.Helper()
	r := newHoldRig(t, 1, 0)
	r.hold(sprint.HoldReq{Names: []string{"amy", "bob"}, Reason: "the work is a machine's"})
	r.tick()
	wc := r.takeOne("m1")
	r.must(store.FinishStep(sprint.FinishReq{As: "m1", Sel: sprint.Sel{IDs: []string{wc}}, Gens: map[string]int{wc: r.snap().Fleet.Card(wc).Int("gen")}, Head: "abc", Who: "m1"}))
	r.hold(sprint.HoldReq{Names: []string{"amy", "bob"}, Release: true, Reason: "back to read"})
	r.tick()
	s := r.snap()
	require.Equal(t, sprint.Review, s.StateOf("s1-1"))
	rc := s.Fleet.Placed(sprint.ReadCardID("s1-1", 1, "amy"))
	require.NotNil(t, rc, "the fixture: amy is asked the first read")
	require.Equal(t, sprint.FriendRow("amy"), rc.Row)
	return r
}

// withdrawRead holds friend name: her read of s1-1 is withdrawn on her row.
func (r *holdRig) withdrawRead(name string) {
	r.t.Helper()
	r.hold(sprint.HoldReq{Names: []string{name}, Reason: "her reader role is removed"})
	rc := r.snap().Fleet.Placed(sprint.ReadCardID("s1-1", 1, name))
	require.NotNil(r.t, rc)
	require.Equal(r.t, sprint.Withdrawn, rc.Col, "the fixture: %s's read is withdrawn", name)
}

// placedReads is the read cards of s1-1 placed and not withdrawn, on either table.
func placedReads(s *sprint.Snapshot) []string {
	var out []string
	for _, c := range s.Readers.Cards() {
		if c.Placed() && c.F("primary") == "s1-1" {
			out = append(out, c.ID)
		}
	}
	for _, c := range s.Fleet.Cards() {
		if c.Placed() && c.Col != sprint.Withdrawn && c.F("kind") == "read" && c.F("primary") == "s1-1" {
			out = append(out, c.ID)
		}
	}
	return out
}

func TestAWithdrawnFriendReadIsAskedAgain(t *testing.T) {
	t.Parallel()
	r := reviewRig(t)
	r.withdrawRead("amy")

	// amy is no longer a reader and bob is, with room: the tick asks bob
	r.tick()
	s := r.snap()
	assert.Equal(t, []string{sprint.ReadCardID("s1-1", 1, "bob")}, placedReads(s), "the withdrawn read is asked of bob")
	assert.Equal(t, sprint.FriendRow("bob"), s.Fleet.Placed(sprint.ReadCardID("s1-1", 1, "bob")).Row)
	amy := s.Fleet.Placed(sprint.ReadCardID("s1-1", 1, "amy"))
	require.NotNil(t, amy, "the withdrawn record stays, as history")
	assert.Equal(t, sprint.Withdrawn, amy.Col)

	// amy is back as a reader and bob's read is withdrawn in turn: she is not asked the
	// attempt again (her withdrawn read keeps its id); a paid reader is
	r.hold(sprint.HoldReq{Names: []string{"amy"}, Release: true, Reason: "her reader role is back"})
	r.withdrawRead("bob")
	r.tick()
	s = r.snap()
	reads := placedReads(s)
	require.Len(t, reads, 1, "one read is asked: %v", reads)
	assert.NotNil(t, s.Readers.Placed(reads[0]), "a paid reader is asked, not amy: %v", reads)
	assert.Equal(t, sprint.Withdrawn, s.Fleet.Placed(sprint.ReadCardID("s1-1", 1, "amy")).Col)
}

func TestAWithdrawnReaderReadIsAskedAgain(t *testing.T) {
	t.Parallel()
	r := newHoldRig(t, 1, 0)
	r.hold(sprint.HoldReq{Names: []string{"amy", "bob"}, Reason: "the readers table alone"})
	r.tick()
	wc := r.takeOne("m1")
	r.must(store.FinishStep(sprint.FinishReq{As: "m1", Sel: sprint.Sel{IDs: []string{wc}}, Gens: map[string]int{wc: r.snap().Fleet.Card(wc).Int("gen")}, Head: "abc", Who: "m1"}))
	r.tick()
	reads := placedReads(r.snap())
	require.Len(t, reads, 1, "the fixture: one read asked")
	first := r.snap().Readers.Placed(reads[0])
	require.NotNil(t, first)

	// its reader is held: the read is taken back and asked of another reader in the tick
	r.hold(sprint.HoldReq{Names: []string{first.Row}, Reason: "its reader role is removed"})
	r.tick()
	s := r.snap()
	reads = placedReads(s)
	require.Len(t, reads, 1, "asked again: %v", reads)
	again := s.Readers.Placed(reads[0])
	require.NotNil(t, again)
	assert.NotEqual(t, first.Row, again.Row, "another reader is asked")
	s, err := r.st.Load(r.ctx, store.All, func(*sprint.Snapshot) map[string][]string { return map[string][]string{sprint.Readers: {first.ID}} })
	require.NoError(t, err)
	old := s.Readers.Card(first.ID)
	require.NotNil(t, old, "the taken-back record stays, as history")
	assert.False(t, old.Placed())
	assert.Empty(t, ruleTwo(s), "a taken-back read is a normal state")
}

func TestRuleTwoIsQuietForAWithdrawnRead(t *testing.T) {
	t.Parallel()
	r := reviewRig(t)
	r.withdrawRead("amy")
	assert.Empty(t, ruleTwo(r.snap()), "a withdrawn read whose primary is in review is a normal state")

	// ask <id> asks it, by hand, as the tick would
	res := r.must(store.AskStep(sprint.AskReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}, Who: "coordinator"}))
	assert.NotEmpty(t, res.Moved)
	assert.Len(t, placedReads(r.snap()), 1)

	// a day on: the withdrawn read raises nothing, neither rule 2 nor a lateness
	r.mu.Lock()
	r.now = r.now.Add(24 * time.Hour)
	r.mu.Unlock()
	r.tick()
	s := r.snap()
	assert.Empty(t, ruleTwo(s))
	amy := sprint.ReadCardID("s1-1", 1, "amy")
	for _, o := range s.Open {
		assert.NotContains(t, o.Note.What, "rule 2", "no invariant judgment for a withdrawn read")
		assert.NotContains(t, o.Note.What, amy, "no judgment names the withdrawn read")
	}
}

// ruleTwo is the check's rule 2 violations.
func ruleTwo(s *sprint.Snapshot) []string {
	var out []string
	for _, v := range sprint.Check(s, nil) {
		if s := v.String(); strings.HasPrefix(s, "rule 2") {
			out = append(out, s)
		}
	}
	return out
}
