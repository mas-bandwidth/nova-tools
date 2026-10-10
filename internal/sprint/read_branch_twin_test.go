package sprint_test

import (
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A read names the branch the work pushed (docs/SPEC-SPRINT.md section 6). On 2026-10-06
// between 7:46 and 8:27 PM ET most broken reads said the branch the read named was not on
// origin: the work had pushed sprint/<card>.g1, a hold took the started card back and it was
// dealt again, its generation bumped with no new push, and the read was handed the branch of
// the generation the card had reached.

// pushedSha is the head the first generation pushed.
const pushedSha = "1111111111111111111111111111111111111111"

// carriedRig is the hold rig with f1-1 worked by amy at its first generation and pushed, held
// back from her (the hold carries the push), dealt again and finished by bob at a later
// generation at the head amy pushed, in review: the branch amy pushed and bob's.
func carriedRig(t *testing.T) (r *holdRig, pushed, later string) {
	t.Helper()
	r = newHoldRig(t, 0, 1)
	r.tick()
	r.startFriends()
	s := r.snap()
	amy := onRow(s, sprint.FriendRow("amy"), "f1", sprint.Working)
	require.Len(t, amy, 1, "the fixture: amy works f1-1")
	wc := s.Fleet.Card(amy[0])
	pushed = sprint.BranchOf("t-", s.Epoch, wc.ID, wc.Int("gen"))
	r.hold(sprint.HoldReq{Names: []string{"amy"}, Reason: "out of credits", Who: "coordinator",
		Started: map[string]string{wc.ID: "a push on its branch " + pushed + " at " + pushedSha}})
	r.tick()
	r.startFriends()
	s = r.snap()
	bob := onRow(s, sprint.FriendRow("bob"), "f1", sprint.Working)
	require.Len(t, bob, 1, "the fixture: f1-1 is dealt again, to bob")
	wc = s.Fleet.Card(bob[0])
	require.Greater(t, wc.Int("gen"), 2, "the fixture: its generation moved on with no push")
	later = sprint.BranchOf("t-", s.Epoch, wc.ID, wc.Int("gen"))
	// bob found the work done at amy's head and pushed nothing: his finish names the branch
	// of the generation he held, as the friend daemon's does
	r.must(store.FinishStep(sprint.FinishReq{As: sprint.FriendRow("bob"), Sel: sprint.Sel{IDs: []string{wc.ID}},
		Gens: map[string]int{wc.ID: wc.Int("gen")}, Head: pushedSha, Branch: later, Who: sprint.FriendRow("bob")}))
	r.hold(sprint.HoldReq{Names: []string{"amy", "bob"}, Reason: "the reads go to the readers", Who: "coordinator"})
	r.tick()
	r.tick()
	require.Equal(t, sprint.Review, r.snap().StateOf("f1-1"))
	return r, pushed, later
}

func TestAReadNamesTheBranchTheWorkPushed(t *testing.T) {
	t.Parallel()
	r, pushed, later := carriedRig(t)
	require.NotEqual(t, pushed, later)
	s := r.snap()
	reads := s.Readers.Of("f1-1")
	require.NotEmpty(t, reads, "the fixture: f1-1 is asked of the readers")
	var cards []*sprint.Card
	cards = append(cards, reads...)
	packets, err := r.st.Packets(r.ctx, cards)
	require.NoError(t, err)
	for _, p := range packets {
		assert.Equal(t, pushed, p.WorkBranch, "%s names the branch the work pushed, never the generation the card reached", p.Card)
		assert.Equal(t, pushedSha, p.Head, "%s names the head pushed", p.Card)
		assert.False(t, strings.Contains(sprint.ReadCardBrief(p.Worker, p.Card, p, "", r.now, ""), later), "%s's brief never names the later generation's branch", p.Card)
	}
}

// openOf is the open judgments of the type on the primary.
func openOf(s *sprint.Snapshot, typ, primary string) []sprint.Open {
	var out []sprint.Open
	for _, o := range s.Open {
		if o.Note.Type == typ && (o.Subject() == primary || o.Note.Card == primary) {
			out = append(out, o)
		}
	}
	return out
}

func TestABrokenReadOnAMissingBranchIsReaskedNotReworked(t *testing.T) {
	t.Parallel()
	r := newHoldRig(t, 1, 0)
	r.hold(sprint.HoldReq{Names: []string{"amy", "bob"}, Reason: "the readers table alone"})
	r.tick()
	wc := r.takeOne("m1")
	r.must(store.FinishStep(sprint.FinishReq{As: "m1", Sel: sprint.Sel{IDs: []string{wc}}, Gens: map[string]int{wc: r.snap().Fleet.Card(wc).Int("gen")}, Head: pushedSha, Who: "m1"}))
	r.tick()
	reads := placedReads(r.snap())
	require.Len(t, reads, 1, "the fixture: one read asked")
	first := r.snap().Readers.Placed(reads[0])
	require.NotNil(t, first)
	packets, err := r.st.Packets(r.ctx, []*sprint.Card{first})
	require.NoError(t, err)
	named := packets[0].WorkBranch
	holds := "sprint/t-s1-1.w1.g9.e0"

	// the reader finds no commit on the branch it was handed; the server's check at the close
	// finds the branch not on origin, and the work's head on another
	finding := "RULE: the branch this read names, " + named + ", is not on origin. There is no commit to judge."
	r.must(store.ReadStep(sprint.ReadReq{As: first.Row, Sel: sprint.Sel{IDs: []string{first.ID}}, Verdict: "broken", Finding: finding,
		Missing: map[string]sprint.MissingBranch{first.ID: {Named: named, Holds: holds}}, Who: first.Row}))
	s, err := r.st.Load(r.ctx, store.All, func(*sprint.Snapshot) map[string][]string { return map[string][]string{sprint.Readers: {first.ID}} })
	require.NoError(t, err)
	old := s.Readers.Card(first.ID)
	require.NotNil(t, old)
	assert.False(t, old.Placed(), "the read is retired")
	assert.Equal(t, sprint.RetiredByMissingBranch, old.F("retired_by"))
	assert.Empty(t, old.F("verdict"), "with no verdict")
	assert.Contains(t, old.F("reason"), sprint.ReaskedMissingBranch)
	assert.Empty(t, openOf(s, sprint.NReadBroken, "s1-1"), "no broken judgment: nothing reworks the card")

	for range 3 {
		r.tick()
	}
	s = r.snap()
	pr := s.Work.Card("s1-1")
	assert.Equal(t, sprint.Review, s.StateOf("s1-1"), "the card is not reworked")
	assert.Equal(t, 1, pr.Int("attempt"), "its attempt is not spent")
	assert.Zero(t, pr.Int("broken_reads"))
	assert.Empty(t, openOf(s, sprint.NReadBroken, "s1-1"))
	again := placedReads(s)
	require.Len(t, again, 1, "the read is asked again")
	rc := s.Readers.Placed(again[0])
	require.NotNil(t, rc)
	packets, err = r.st.Packets(r.ctx, []*sprint.Card{rc})
	require.NoError(t, err)
	assert.Equal(t, holds, packets[0].WorkBranch, "asked again on the branch origin holds the head on")
	assert.Equal(t, pushedSha, packets[0].Head)

	lines, err := r.st.Log(r.ctx)
	require.NoError(t, err)
	var said []string
	for _, l := range lines {
		if l.Note != nil && l.Note.Type == sprint.NReadMissingBranch {
			said = append(said, l.Note.What)
		}
	}
	require.Len(t, said, 1, "one note says so")
	assert.Contains(t, said[0], "re-asked: the read named a branch not on origin")
	assert.Contains(t, said[0], holds)

	// the same broken verdict without the server's finding is a verdict, judged as before
	r.must(store.ReadStep(sprint.ReadReq{As: rc.Row, Sel: sprint.Sel{IDs: []string{rc.ID}}, Verdict: "broken", Finding: "internal/sprint/x.go:1 breaks STEP 2: put the line back", Who: rc.Row}))
	assert.NotEmpty(t, openOf(r.snap(), sprint.NReadBroken, "s1-1"), "a broken read on a branch origin holds is a finding against the work")
}
