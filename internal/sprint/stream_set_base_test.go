package sprint

import (
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/cardhdr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// baseBrief is a card brief cut on base, the BASE: line stream set --base reads.
func baseBrief(base string) string {
	return "c: a card tier: flash\nREPO: example.invalid/owner/name\nBASE: " + base + "\nPATHS: cmd/test.go\n\nDo the task.\n"
}

// briefBase is the branch a brief's BASE: line names, for the test's own reads.
func briefBase(brief string) string {
	v, _ := cardhdr.Value(brief, "BASE")
	ref, _, _ := cardhdr.ParseBase(v)
	return ref
}

// TestStreamSetBaseRepointsQueuedCards: stream set --base rewrites the BASE: line
// of every card of the stream not yet dealt and of every card queued to merge, as
// a change of the work table the store writes; a card dealt and working keeps its
// base and is listed. Set mutates nothing in place: the plan carries the change,
// so the store is what writes it.
func TestStreamSetBaseRepointsQueuedCards(t *testing.T) {
	t.Parallel()
	w := newWorld(t, "reader-a")
	w.must(Add(w.s, AddReq{Stream: "s1", IDs: []string{"s1-ready"}, Brief: baseBrief("old/branch"), Who: "coordinator"}))
	w.must(Add(w.s, AddReq{Stream: "s1", IDs: []string{"s1-waiting"}, Brief: baseBrief("old/branch"), Needs: []string{"s1-ready"}, Who: "coordinator"}))
	w.must(Add(w.s, AddReq{Stream: "s1", IDs: []string{"s1-dealt"}, Brief: baseBrief("old/branch"), Who: "coordinator"}))
	w.must(Add(w.s, AddReq{Stream: "s1", IDs: []string{"s1-merging"}, Brief: baseBrief("old/branch"), Who: "coordinator"}))

	require.Equal(t, Ready, w.s.Work.Placed("s1-ready").Col, "s1-ready is ready")
	require.Equal(t, Waiting, w.s.Work.Placed("s1-waiting").Col, "s1-waiting is waiting")

	// a card dealt and working: its attempt is past 0 and it keeps its base
	dealt := w.s.Work.Placed("s1-dealt")
	dealt.Fields["attempt"] = "1"
	dealt.Col = Working

	// a card queued to merge: its merge card is in the merge table's queue
	merging := w.s.Work.Placed("s1-merging")
	merging.Fields["attempt"] = "1"
	merging.Col = Merging
	w.s.Merge.Put(&Card{ID: "s1-merging", Row: "s1", Col: Queued, Rev: 1,
		Fields: map[string]string{"kind": "merge", "primary": "s1-merging", "stream": "s1"}})

	before := map[string]string{}
	for _, c := range w.s.Work.Cards() {
		if c.Placed() && c.Row == "s1" {
			before[c.ID] = c.F("brief")
		}
	}

	// the candidates the verb's check would hold to the base, bound to the step
	checked := StreamSetBaseChecks(w.s, []string{"s1"}, "sprint/new-branch")
	require.Len(t, checked, 3, "the ready, waiting and queued-to-merge cards are checked")

	p := Set(w.s, SetReq{Streams: []string{"s1"}, Base: "sprint/new-branch", BaseChecked: checked, Who: "coordinator"})
	require.Empty(t, p.Refused)

	// Set mutates nothing: the rewrite is a change of the work table, not a field
	// written on the snapshot the plan was built over.
	for _, c := range w.s.Work.Cards() {
		if c.Placed() && c.Row == "s1" {
			assert.Equal(t, before[c.ID], c.F("brief"), "%s: Set mutated the snapshot in place", c.ID)
		}
	}

	// the plan carries one change of the work table per re-pointed card, each with
	// the brief revision the replacement is, and lists the dealt card.
	changes, moved := map[string]string{}, ""
	for _, u := range p.Units {
		if u.Key == CtlID("s1") {
			moved = u.Moved
			continue
		}
		for _, ch := range u.Changes {
			if ch.Table == Work {
				changes[u.Key] = ch.Entry.Set["brief"]
			}
		}
	}
	for _, id := range []string{"s1-ready", "s1-waiting", "s1-merging"} {
		require.Contains(t, changes, id, "%s is re-pointed by a change of the work table", id)
		assert.Equal(t, "sprint/new-branch", briefBase(changes[id]), "%s carries the new base", id)
	}
	assert.NotContains(t, changes, "s1-dealt", "a dealt and working card keeps its base")
	assert.Contains(t, moved, "s1-dealt keeps its base", "the dealt card is listed")

	w.must(p)
	assert.Equal(t, "sprint/new-branch", briefBase(w.s.Work.Placed("s1-ready").F("brief")))
	assert.Equal(t, "sprint/new-branch", briefBase(w.s.Work.Placed("s1-waiting").F("brief")))
	assert.Equal(t, "sprint/new-branch", briefBase(w.s.Work.Placed("s1-merging").F("brief")))
	assert.Equal(t, "old/branch", briefBase(w.s.Work.Placed("s1-dealt").F("brief")))
}

// TestStreamSetBaseChecksTheRewrittenBriefs: the check add runs is held over each
// brief the rewrite would carry, in work order, and not over a dealt card's.
func TestStreamSetBaseChecksTheRewrittenBriefs(t *testing.T) {
	t.Parallel()
	w := newWorld(t, "reader-a")
	w.must(Add(w.s, AddReq{Stream: "s1", IDs: []string{"s1-1"}, Brief: baseBrief("old/branch"), Who: "coordinator"}))
	w.must(Add(w.s, AddReq{Stream: "s1", IDs: []string{"s1-2"}, Brief: baseBrief("old/branch"), Who: "coordinator"}))
	w.s.Work.Placed("s1-2").Fields["attempt"] = "1"
	w.s.Work.Placed("s1-2").Col = Working

	checks := StreamSetBaseChecks(w.s, []string{"s1"}, "sprint/new-branch")
	require.Len(t, checks, 1, "only the card not yet dealt is checked")
	assert.Equal(t, "s1", checks[0].Stream)
	assert.Equal(t, "s1-1", checks[0].ID)
	assert.Equal(t, baseBrief("old/branch"), checks[0].Was, "the check records the brief the card carried")
	assert.Equal(t, "sprint/new-branch", briefBase(checks[0].Brief))
	assert.Contains(t, checks[0].Brief, "c: a card tier: flash", "every other byte is kept")
}

// TestStreamSetBaseRefusesWhenTheCheckedCardsMoved: the candidates the check add
// runs read at the base are bound to the write. A card added to the stream, or a
// brief revised, or a card dealt after the check, refuses the step whole, nothing
// written: no brief is rewritten without its PATHS held to the base
// (sprint.baseChecksHeld; docs/SPEC-SPRINT.md section 11, stream set --base).
func TestStreamSetBaseRefusesWhenTheCheckedCardsMoved(t *testing.T) {
	t.Parallel()
	const base = "sprint/new-branch"
	req := func(checked []StreamSetBaseCheck) SetReq {
		return SetReq{Streams: []string{"s1"}, Base: base, BaseChecked: checked, Who: "coordinator"}
	}

	// A card the check did not see, added to the stream while it fetched.
	t.Run("a card added", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t, "reader-a")
		w.must(Add(w.s, AddReq{Stream: "s1", IDs: []string{"s1-1"}, Brief: baseBrief("old/branch"), Who: "coordinator"}))
		checked := StreamSetBaseChecks(w.s, []string{"s1"}, base)
		w.must(Add(w.s, AddReq{Stream: "s1", IDs: []string{"s1-2"}, Brief: baseBrief("old/branch"), Who: "coordinator"}))
		p := Set(w.s, req(checked))
		require.Len(t, p.Refused, 1)
		assert.Contains(t, p.Refused[0].Why, "the stream moved while its briefs were held")
		assert.Empty(t, p.Units, "a refusal writes nothing")
	})

	// A brief revised while the check fetched: its PATHS are not the ones checked.
	t.Run("a brief revised", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t, "reader-a")
		w.must(Add(w.s, AddReq{Stream: "s1", IDs: []string{"s1-1"}, Brief: baseBrief("old/branch"), Who: "coordinator"}))
		checked := StreamSetBaseChecks(w.s, []string{"s1"}, base)
		c := w.s.Work.Placed("s1-1")
		c.Fields["brief"] = strings.Replace(c.F("brief"), "PATHS: cmd/test.go", "PATHS: cmd/other.go", 1)
		p := Set(w.s, req(checked))
		require.Len(t, p.Refused, 1)
		assert.Contains(t, p.Refused[0].Why, "changed while its brief was held")
		assert.Empty(t, p.Units, "a refusal writes nothing")
	})

	// A card dealt while the check fetched: it is no card the check re-points.
	t.Run("a card dealt", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t, "reader-a")
		w.must(Add(w.s, AddReq{Stream: "s1", IDs: []string{"s1-1"}, Brief: baseBrief("old/branch"), Who: "coordinator"}))
		checked := StreamSetBaseChecks(w.s, []string{"s1"}, base)
		c := w.s.Work.Placed("s1-1")
		c.Fields["attempt"] = "1"
		c.Col = Working
		p := Set(w.s, req(checked))
		require.Len(t, p.Refused, 1)
		assert.Contains(t, p.Refused[0].Why, "the stream moved while its briefs were held")
		assert.Empty(t, p.Units, "a refusal writes nothing")
	})
}

// TestStreamSetBaseRefusesAStreamThatIsNoRow: a stream that is no row of either
// table refuses the whole call, nothing written.
func TestStreamSetBaseRefusesAStreamThatIsNoRow(t *testing.T) {
	t.Parallel()
	w := newWorld(t, "reader-a")
	p := Set(w.s, SetReq{Streams: []string{"nope"}, Base: "sprint/new-branch", Who: "coordinator"})
	require.Len(t, p.Refused, 1)
	assert.Contains(t, p.Refused[0].Why, "no stream nope")
	assert.Empty(t, p.Units, "a refusal writes nothing")
	assert.Empty(t, p.Props, "a refusal writes nothing")
}
