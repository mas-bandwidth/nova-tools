package sprint_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// The fsck check friend-queue (docs/SPEC-SPRINT.md, "The fsck checks",
// fsck-friend-queue-agreement-b.w5): the cards the store has dealt to a friend's row are
// the ids her own inbox/QUEUE.json names, and every REPORT.md in her outbox belongs to a
// dealt or finished card. Her files live on her own machine, so her daemon carries the
// queue ids and the outbox ids on its beat, and the check compares them with the store's
// cards through the comparison friend reconcile uses (internal/sprint/friend_reconcile.go).
// The tick's reconcile repairs ordinary drift every tick, so the check reports only a
// disagreement that is the same on the two reconcile passes it is handed, one violation per
// card, and names friend reconcile <friend> as the fix; an agreeing friend is clean.
func TestFsckFindsAFriendQueueThatDisagreesWithTheStore(t *testing.T) {
	t.Parallel()
	taken := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
	card := func(id string) *sprint.Card {
		return &sprint.Card{ID: id, Row: sprint.FriendRow("amy"), Col: sprint.Working,
			Fields: map[string]string{"taken": taken.Format(time.RFC3339)}}
	}
	// the cards the store has dealt to amy's row: three working, each with its job.
	cards := []sprint.FriendQueueCard{
		{Card: card("s1-1.w1"), Job: "s1-1.w1"},
		{Card: card("s1-2.w1"), Job: "s1-2.w1"},
		{Card: card("s1-3.w1"), Job: "s1-3.w1"},
	}
	// what her daemon carries in both passes: her QUEUE.json says s1-2 working and s1-3 done,
	// holds an id of no card of her row, and her outbox holds s1-1's report and a report of
	// no dealt or finished card.
	disagree := sprint.FriendQueueReport{
		Account: sprint.FriendAccount{Written: taken.Add(time.Hour), Tasks: map[string]string{
			"s1-2.w1": "working", "s1-3.w1": "done", "zz-9.w1": "working"}},
		Outbox: []string{"s1-1.w1", "orphan-7.w1"},
	}

	find := sprint.FsckFriendQueue("amy", cards, disagree, disagree)
	byName := map[string]sprint.FriendQueueFinding{}
	for _, f := range find {
		byName[f.Name] = f
	}
	assert.Equal(t, 4, len(find), "one violation per disagreement: %v", find)
	// one violation per card the store's dealt set and her account disagree on
	assert.Equal(t, sprint.FriendQueueCardKind, byName["s1-1.w1"].Kind, "her report is there and the card is still working after both passes: the collect is broken")
	assert.Equal(t, sprint.FriendQueueCardKind, byName["s1-3.w1"].Kind, "she says done with no report: the card should have been returned")
	assert.NotContains(t, byName, "s1-2.w1", "she still says working: they agree")
	assert.Equal(t, sprint.FriendQueueStrayKind, byName["zz-9.w1"].Kind, "an id of her account that is no card of her row is named, never acted on")
	assert.Equal(t, sprint.FriendQueueReportKind, byName["orphan-7.w1"].Kind, "a report that belongs to no dealt or finished card")
	for _, f := range find {
		assert.Equal(t, sprint.FsckFriendQueue, f.Check)
		assert.Equal(t, "friend reconcile amy", f.Fix, "%s: the check names the repair and runs none", f.Name)
		assert.NotEmpty(t, f.Line(), "%s: every finding prints", f.Name)
	}

	// a disagreement the first pass has and the second does not is dropped: the tick's
	// reconcile repaired it between the two passes, so fsck never names it.
	repaired := sprint.FriendQueueReport{Account: sprint.FriendAccount{Written: disagree.Account.Written,
		Tasks: map[string]string{"s1-1.w1": "working", "s1-2.w1": "working", "s1-3.w1": "working"}}}
	assert.Empty(t, sprint.FsckFriendQueue("amy", cards, disagree, repaired), "a disagreement that does not survive the second pass is no finding")

	// an agreeing friend is clean: every dealt card is in her queue working, no strays, no
	// report outside a dealt or finished card.
	agree := sprint.FriendQueueReport{Account: sprint.FriendAccount{Written: disagree.Account.Written,
		Tasks: map[string]string{"s1-1.w1": "working", "s1-2.w1": "working", "s1-3.w1": "working"}}}
	assert.Empty(t, sprint.FsckFriendQueue("amy", cards, agree, agree), "an agreeing friend is clean")
}
