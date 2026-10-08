package sprint

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// landOn lands one accepted card through the record by name, staged on ref at commit,
// with parent the first parent of that commit (empty when the commit is a root).
func landOn(w *world, id, ref, commit, parent string) {
	w.t.Helper()
	accepted(w, id)
	head := w.s.Work.Card(id).F("head")
	p := MergeStep(w.s, MergeReq{Stream: "s1", Landed: []LandedPin{{ID: id, Head: head, InBase: true}}})
	p = WithStaged(w.s, p, "s1", []string{id}, &Milestone{Repo: "mas-bandwidth/nova", Ref: ref, Commit: commit, Parent: parent, Evidence: "land: gate green, pushed " + commit})
	w.must(p)
	require.Equal(w.t, Landed, w.state(id))
}

// The three delivery milestones are records apart (docs/SPEC-SPRINT.md section 7, delivery
// milestones). A card the lander pushed to a stream branch (sprint/s1) is staged there and
// nothing more: a promotion of the sprint branch into dev verifies only the cards staged on
// the branch it promoted, so the stream-branch push never counts as delivered to dev. The
// merged result on dev is recorded as the promotion names it (a squash or rebase gives dev
// another commit than the sprint tip, and both are kept). An install receipt marks the cards
// a promotion verified on that target. A failed promotion is a record that stands until a
// promotion succeeds after it. A promotion that names its branch and no cards verifies what
// its frozen tip carried: a staged commit that is the tip or an ancestor of it, never a card
// on the branch whose commit is not, and never a card the lander pushed after the tip was cut.
func TestAStreamBranchPushIsStagedNotDevDelivered(t *testing.T) {
	t.Parallel()
	w := setup(t, 5)
	landOn(w, "s1-1", "sprint/s1", "aaaa111", "") // a stream-branch push
	w.tick(time.Second)
	landOn(w, "s1-2", "sprint/main", "bbbb222", "") // on the sprint branch: an ancestor of the tip
	w.tick(time.Second)
	landOn(w, "s1-5", "sprint/main", "side999", "dead000") // same branch, earlier than the tip, not an ancestor
	w.tick(time.Second)
	landOn(w, "s1-3", "sprint/main", "cccc333", "bbbb222") // the tip promote freezes; its parent is bbbb222, not side999
	w.tick(time.Second)
	landOn(w, "s1-4", "sprint/main", "abab444", "cccc333") // pushed after the tip was frozen: a descendant
	c := w.s.Work.Card("s1-1")
	assert.Equal(t, "sprint/s1", c.F(FieldStagedRef), "the staged record names the ref the lander pushed")
	assert.Equal(t, "aaaa111", c.F(FieldStagedCommit))
	assert.Equal(t, "mas-bandwidth/nova", c.F(FieldStagedRepo))
	assert.NotEmpty(t, c.F(FieldStagedEvidence))
	d := Delivery(w.s)
	assert.Equal(t, "bbbb222", w.s.Work.Card("s1-3").F(FieldStagedParent), "the tip's staged record names its parent")
	assert.Equal(t, "dead000", w.s.Work.Card("s1-5").F(FieldStagedParent))
	assert.Equal(t, "sprint/main", w.s.Work.Card("s1-5").F(FieldStagedRef), "the non-ancestor is staged on the sprint branch")
	assert.Equal(t, DeliveryCounts{Staged: 5}, d.Counts(), "landed is staged, nothing is in dev yet")
	ctl := w.s.StreamCtl("s1")
	assert.Equal(t, "5", ctl.F(FieldStreamStaged), "the stream's record counts its staged cards")
	assert.Equal(t, "sprint/main", ctl.F(FieldStagedRef), "and names its last staging")

	// a failed promotion stays visible, and verifies nothing
	w.tick(time.Minute)
	w.must(Promoted(w.s, PromotedReq{Failed: "merge-group run red: functional tier", Branch: "sprint/main", Tip: "cccc333", Evidence: "pr=812", Who: "coordinator"}))
	f, ok := FailedPromotion(w.s)
	require.True(t, ok, "the failed promotion is on the record")
	assert.Equal(t, "merge-group run red: functional tier", f.Why)
	assert.Equal(t, "sprint/main@cccc333", f.From)
	assert.Equal(t, 0, Delivery(w.s).Verified)

	// the promotion of the sprint branch, squash-merged: dev's commit is not the tip
	w.tick(time.Minute)
	p := w.must(Promoted(w.s, PromotedReq{Sha: "dddd444", Branch: "sprint/main", Tip: "cccc333", Repo: "mas-bandwidth/nova", Evidence: "pr=813 entry=MQE_1", Who: "coordinator"}))
	assert.Contains(t, p.Units[0].Moved, "2 verified in dev")
	_, ok = FailedPromotion(w.s)
	assert.False(t, ok, "a later promotion that merged closes the failure")
	assert.Empty(t, w.s.Work.Card("s1-1").F(FieldVerified), "the stream-branch push is not delivered to dev")
	assert.Empty(t, w.s.Work.Card("s1-5").F(FieldVerified), "a card on the branch whose staged commit is not an ancestor of the tip is not verified in dev")
	assert.Empty(t, w.s.Work.Card("s1-4").F(FieldVerified), "a landing after the frozen tip is not in what the tip carried")
	v := w.s.Work.Card("s1-2")
	assert.Equal(t, "dev", v.F(FieldVerifiedRef))
	assert.Equal(t, "dddd444", v.F(FieldVerifiedCommit), "the merged result on dev, as the promotion names it")
	assert.Equal(t, "sprint/main@cccc333", v.F(FieldVerifiedFrom), "and the sprint tip it was promoted from")
	assert.Equal(t, "pr=813 entry=MQE_1", v.F(FieldVerifiedEvidence))
	assert.Equal(t, DeliveryCounts{Staged: 5, Verified: 2}, Delivery(w.s).Counts())
	assert.Equal(t, "2", w.s.StreamCtl("s1").F(FieldStreamVerified))

	// a branch with no tip, or a tip no landing staged, does not say which landings reached dev
	w.tick(time.Minute)
	p = w.must(Promoted(w.s, PromotedReq{Sha: "eeee551", Branch: "sprint/main", Who: "coordinator"}))
	assert.Contains(t, p.Units[0].Moved, "0 verified in dev")
	assert.Contains(t, p.Units[0].Moved, "--branch wants --tip")
	w.tick(time.Minute)
	p = w.must(Promoted(w.s, PromotedReq{Sha: "eeee552", Branch: "sprint/main", Tip: "9999999", Who: "coordinator"}))
	assert.Contains(t, p.Units[0].Moved, "0 verified in dev")
	assert.Empty(t, w.s.Work.Card("s1-4").F(FieldVerified))

	// a promotion naming no branch and no cards verifies nothing: it cannot say what reached dev
	w.tick(time.Minute)
	p = w.must(Promoted(w.s, PromotedReq{Sha: "eeee555", Who: "coordinator"}))
	assert.Contains(t, p.Units[0].Moved, "0 verified in dev")
	assert.Equal(t, 2, Delivery(w.s).Verified)

	// an install receipt: the cards a promotion verified, on that target
	refused := Installed(w.s, InstalledReq{Target: "target-a", Commit: "ffff999", Receipt: "nova-tools 1.2.3 installed", Who: "coordinator"})
	require.NotEmpty(t, refused.Refused, "an install names a commit a promotion recorded")
	refused = Installed(w.s, InstalledReq{Target: "target-a", Commit: "dddd444", Who: "coordinator"})
	require.NotEmpty(t, refused.Refused, "an install wants its receipt")
	w.must(Installed(w.s, InstalledReq{Target: "target-a", Commit: "dddd444", Receipt: "nova-tools 1.2.3 installed, sha256 ok", Who: "coordinator"}))
	got := Delivery(w.s)
	assert.Equal(t, DeliveryCounts{Staged: 5, Verified: 2, Installed: 2}, got.Counts())
	assert.Equal(t, map[string]int{"target-a": 2}, got.Targets)
	assert.Equal(t, "2", w.s.StreamCtl("s1").F(FieldStreamInstalled))
	assert.Empty(t, InstallsOf(w.s.Work.Card("s1-1")), "never installed what never reached dev")
}
