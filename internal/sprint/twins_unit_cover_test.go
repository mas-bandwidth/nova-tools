package sprint

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Relink is a pure function over a Snapshot (twins.go:188): the repair of the
// drop and add pair, re-pointing every waiting card's need of an old id to its
// twin and answering the blocked judgments that named only the olds. These are
// the core package's own tests of its main path and of every refusal tier, on
// the world harness (sim_test.go), with no store, clock or subprocess.

// relinkCoverWorld is old, first and twin in s1, dep1 needing old and dep2
// needing first,old in s2: the drop-and-add pair already made.
func relinkCoverWorld(t *testing.T) *world {
	t.Helper()
	w := newWorld(t, "reader-a")
	w.must(Add(w.s, AddReq{Stream: "s1", IDs: []string{"old", "first", "twin"}, Brief: proBrief, Who: "coordinator"}))
	w.must(Add(w.s, AddReq{Stream: "s2", IDs: []string{"dep1"}, Needs: []string{"old"}, Brief: proBrief, Who: "coordinator"}))
	w.must(Add(w.s, AddReq{Stream: "s2", IDs: []string{"dep2"}, Needs: []string{"first", "old"}, Brief: proBrief, Who: "coordinator"}))
	w.clean("relink cover world")
	return w
}

// TestSprintTwinsCoverRelinkMainPath pins the main path of Relink: every waiting
// card that needs old needs twin instead, in the same place of its needs, each
// carries its relink record, and the blocked judgment on old is closed with the
// reason carried into its decided answer.
func TestSprintTwinsCoverRelinkMainPath(t *testing.T) {
	t.Parallel()
	w := relinkCoverWorld(t)
	n := judgment(NBlocked, "s2", w.s.Now, 0, "dep1")
	n.Needs = []string{"old"}
	w.note(n)
	require.Len(t, w.openOn("dep1"), 1, "the blocked judgment is open")

	p := w.must(Relink(w.s, RelinkReq{Old: []string{"old"}, New: "twin", Reason: "re-cut", Who: "coordinator"}))
	w.clean("relinked")

	require.NotEmpty(t, p.Units, "the relink writes the dependents and the weights")
	assert.Equal(t, "twin", w.s.Work.Card("dep1").F("needs"), "dep1 needs the twin alone")
	assert.Equal(t, "first,twin", w.s.Work.Card("dep2").F("needs"), "dep2 keeps first, in place")
	want := "old -> twin " + stamp(t0) + " by coordinator"
	assert.Equal(t, want, w.s.Work.Card("dep1").F(FieldRelinked), "dep1 records its relink")
	assert.Equal(t, want, w.s.Work.Card("dep2").F(FieldRelinked), "dep2 records its relink")
	assert.Empty(t, w.openOn("dep1"), "the blocked judgment is closed")

	answered := false
	for _, nt := range w.notes {
		if nt.Kind == Decided && strings.Contains(nt.What, "replaced by twin: re-cut") {
			answered = true
		}
	}
	assert.True(t, answered, "the decided note carries \"replaced by twin: re-cut\"")
	assert.Equal(t, "2", w.s.Work.Card("twin").F(FieldBehind), "the twin carries the weight of what waits on it")
}

// TestSprintTwinsCoverRelinkRefusals pins every refusal tier of Relink: each
// refuses the whole step with the words the reader acts on, and writes nothing.
func TestSprintTwinsCoverRelinkRefusals(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		build func(t *testing.T) (*world, RelinkReq)
		why   string
	}{
		{
			"not the coordinator",
			func(t *testing.T) (*world, RelinkReq) {
				return relinkCoverWorld(t), RelinkReq{Old: []string{"old"}, New: "twin", Who: "intruder"}
			},
			"coordinator",
		},
		{
			"no olds",
			func(t *testing.T) (*world, RelinkReq) {
				return relinkCoverWorld(t), RelinkReq{New: "twin", Who: "coordinator"}
			},
			"relink wants",
		},
		{
			"no new",
			func(t *testing.T) (*world, RelinkReq) {
				return relinkCoverWorld(t), RelinkReq{Old: []string{"old"}, Who: "coordinator"}
			},
			"relink wants",
		},
		{
			"a new not on the table",
			func(t *testing.T) (*world, RelinkReq) {
				return relinkCoverWorld(t), RelinkReq{Old: []string{"old"}, New: "ghost", Who: "coordinator"}
			},
			"is not on the table",
		},
		{
			"a new that is a sentinel",
			func(t *testing.T) (*world, RelinkReq) {
				w := relinkCoverWorld(t)
				w.must(Add(w.s, AddReq{Stream: "s1", IDs: []string{"stop"}, Sentinel: true, Who: "coordinator"}))
				return w, RelinkReq{Old: []string{"old"}, New: "stop", Who: "coordinator"}
			},
			"is a sentinel",
		},
		{
			"an old named twice",
			func(t *testing.T) (*world, RelinkReq) {
				return relinkCoverWorld(t), RelinkReq{Old: []string{"old", "old"}, New: "twin", Who: "coordinator"}
			},
			"named twice",
		},
		{
			"an old equal to the new",
			func(t *testing.T) (*world, RelinkReq) {
				return relinkCoverWorld(t), RelinkReq{Old: []string{"twin"}, New: "twin", Who: "coordinator"}
			},
			"its own twin",
		},
		{
			"a landed old",
			func(t *testing.T) (*world, RelinkReq) {
				w := relinkCoverWorld(t)
				w.place(w.s.Work, "old", "s1", Landed)
				return w, RelinkReq{Old: []string{"old"}, New: "twin", Who: "coordinator"}
			},
			"landed: what needed it went on",
		},
		{
			"an old nothing waits on",
			func(t *testing.T) (*world, RelinkReq) {
				w := relinkCoverWorld(t)
				w.must(Add(w.s, AddReq{Stream: "s1", IDs: []string{"lonely"}, Brief: proBrief, Who: "coordinator"}))
				return w, RelinkReq{Old: []string{"lonely"}, New: "twin", Who: "coordinator"}
			},
			"nothing waits on",
		},
		{
			"an old only waived",
			func(t *testing.T) (*world, RelinkReq) {
				w := relinkCoverWorld(t)
				w.must(Add(w.s, AddReq{Stream: "s1", IDs: []string{"solo"}, Brief: proBrief, Who: "coordinator"}))
				w.must(Add(w.s, AddReq{Stream: "s2", IDs: []string{"wv"}, Needs: []string{"solo"}, Brief: proBrief, Who: "coordinator"}))
				c := w.s.Work.Card("wv")
				c.Fields["waived"] = "solo"
				c.Rev++
				return w, RelinkReq{Old: []string{"solo"}, New: "twin", Who: "coordinator"}
			},
			"nothing waits on",
		},
		{
			"a cycle",
			func(t *testing.T) (*world, RelinkReq) {
				w := newWorld(t, "reader-a")
				w.must(Add(w.s, AddReq{Stream: "s1", IDs: []string{"old", "first"}, Brief: proBrief, Who: "coordinator"}))
				w.must(Add(w.s, AddReq{Stream: "s2", IDs: []string{"dep1"}, Needs: []string{"old"}, Brief: proBrief, Who: "coordinator"}))
				w.must(Add(w.s, AddReq{Stream: "s2", IDs: []string{"dep2"}, Needs: []string{"first", "old"}, Brief: proBrief, Who: "coordinator"}))
				w.must(Add(w.s, AddReq{Stream: "s1", IDs: []string{"twin"}, Needs: []string{"dep2"}, Brief: proBrief, Who: "coordinator"}))
				return w, RelinkReq{Old: []string{"old"}, New: "twin", Who: "coordinator"}
			},
			"cycle",
		},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w, req := tc.build(t)
			p := Relink(w.s, req)
			require.NotEmpty(t, p.Refused, tc.name)
			assert.Contains(t, p.Refused[0].Why, tc.why, tc.name)
			assert.Empty(t, p.Units, "%s: nothing is written", tc.name)
		})
	}
}

// TestSprintTwinsCoverMergeCardChanges pins the one-change-per-card fold: two
// changes that both create one card refuse the step, and set-only changes fold
// into the first, the later set winning and a later unset removing the field.
func TestSprintTwinsCoverMergeCardChanges(t *testing.T) {
	t.Parallel()
	t.Run("two creates of one card refuse", func(t *testing.T) {
		t.Parallel()
		e := createEntry("c", "s1", Ready, 0, nil)
		out, bad := mergeCardChanges([]Unit{
			{Key: "c", Changes: []Change{change(Work, e)}},
			{Key: "c", Changes: []Change{change(Work, e)}},
		})
		assert.Contains(t, bad, "two changes of", "a second create of one card refuses")
		assert.Empty(t, out, "nothing is written")
	})
	t.Run("set-only changes fold, later winning", func(t *testing.T) {
		t.Parallel()
		c := &Card{ID: "c", Fields: map[string]string{"b": "old"}}
		out, bad := mergeCardChanges([]Unit{
			{Key: "c", Changes: []Change{change(Work, setEntry(c, map[string]string{"a": "1"}))}},
			{Key: "c", Changes: []Change{change(Work, setEntry(c, map[string]string{"a": "2", "b": "kept"}))}},
			{Key: "c", Changes: []Change{change(Work, setEntry(c, nil, "b"))}},
		})
		require.Empty(t, bad)
		require.Len(t, out, 1, "the three units fold into one")
		got := out[0].Changes[0].Entry
		assert.Equal(t, "2", got.Set["a"], "the later set wins")
		assert.NotContains(t, got.Set, "b", "the later unset removes the field")
		assert.Contains(t, got.Unset, "b", "the unset is carried")
	})
}
