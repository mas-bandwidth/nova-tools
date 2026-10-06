package sprint_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// A removed stream's open judgments retire with it (the coordinator view of 2026-10-06 00:11:
// six "stream stopped" judgments of streams stream remove had taken off the table, each
// whose printed next step, resume --stream <s>, was refused "no such stream", and each
// counted in the view's j).

// stoppedStream adds the stream with no card and stops it on its base, the judgment
// NBaseRed open on the stream as a whole, and stops the machine, as stream remove wants.
func (r *conflictRig) stoppedStream(stream string) sprint.Open {
	r.t.Helper()
	r.must(store.AddStep(sprint.AddReq{Stream: stream}))
	r.must(store.MergeStep(sprint.MergeReq{Stream: stream, BaseRed: "go vet ./...: exit status 1"}))
	open := r.open(sprint.NBaseRed)
	require.Len(r.t, open, 1, "the stream stopped with its judgment")
	require.Equal(r.t, sprint.StreamSubject(stream), open[0].Subject())
	require.Contains(r.t, r.inboxTypes(), sprint.NBaseRed, "the view shows it")
	_, _, _, err := r.st.SetMachine(r.ctx, false)
	require.NoError(r.t, err)
	return open[0]
}

// removeRows is stream remove's write (cmd/nova-sprint cmdStreamRemove): its rule, then the
// rows of both tables deleted.
func (r *conflictRig) removeRows(streams ...string) {
	r.t.Helper()
	require.Empty(r.t, sprint.StreamRemove(r.snap(), false, streams))
	pinned, err := r.st.Pinned(r.ctx)
	require.NoError(r.t, err)
	for _, tb := range []string{sprint.Work, sprint.Merge} {
		require.NoError(r.t, pinned.B.RowsDel(r.ctx, pinned.Names.Table(tb), streams))
	}
}

// inboxTypes is the type of each group the coordinator's inbox shows.
func (r *conflictRig) inboxTypes() []string {
	r.t.Helper()
	open, err := r.m.OpenNotes(r.ctx)
	require.NoError(r.t, err)
	judgments, _ := sprint.SplitOpen(open)
	var out []string
	for _, g := range sprint.Inbox(sprint.InboxReq{Now: r.now, Open: judgments}) {
		out = append(out, g.Type)
	}
	return out
}

// notesOf is every note of the type written, oldest first.
func (r *conflictRig) notesOf(typ string) []sprint.Note {
	r.t.Helper()
	all, _, err := r.m.NotesSince(r.ctx, "", 100000)
	require.NoError(r.t, err)
	var out []sprint.Note
	for _, n := range all {
		if n.Type == typ {
			out = append(out, n)
		}
	}
	return out
}

// openOn is every open note, judgment or acknowledgement, naming the stream.
func (r *conflictRig) openOn(stream string) []sprint.Open {
	r.t.Helper()
	open, err := r.m.OpenNotes(r.ctx)
	require.NoError(r.t, err)
	var out []sprint.Open
	for _, o := range open {
		if o.Note.Stream == stream || o.Subject() == sprint.StreamSubject(stream) {
			out = append(out, o)
		}
	}
	return out
}

func TestRemovingAStreamRetiresItsOpenJudgments(t *testing.T) {
	t.Parallel()
	r := newConflictRig(t)
	j := r.stoppedStream("s2")
	r.removeRows("s2")
	said, err := r.st.RetireStreams(r.ctx, "stream remove", []string{"s2"}, "removed")
	require.NoError(t, err)
	require.Len(t, said, 1, "one NOTE line a stream: %v", said)
	assert.Contains(t, said[0], "stream s2 removed: 1 open note retired")
	assert.Contains(t, said[0], j.Note.ID)

	assert.Empty(t, r.openOn("s2"), "nothing stays open on the removed stream")
	assert.NotContains(t, r.inboxTypes(), sprint.NBaseRed, "the view is without it")
	var decided []string
	for _, n := range r.notesOf(sprint.NBaseRed) {
		if n.Kind == sprint.Decided && n.Answers == j.Note.ID {
			decided = append(decided, n.What)
		}
	}
	assert.Equal(t, []string{"retired: stream s2 removed"}, decided, "the judgment is decided as retired")
	notes := r.notesOf(sprint.NStreamRetired)
	require.Len(t, notes, 1)
	assert.Equal(t, sprint.Happened, notes[0].Kind)
	assert.Equal(t, "s2", notes[0].Stream)

	// the next ticks, STOPPED and RUNNING, find nothing to retire and raise nothing on it
	r.tick()
	_, _, _, err = r.st.SetMachine(r.ctx, true)
	require.NoError(t, err)
	r.tick()
	r.tick()
	assert.Empty(t, r.openOn("s2"))
	assert.Len(t, r.notesOf(sprint.NStreamRetired), 1, "retired once")

	// retired again, nothing is open: no line, nothing written
	said, err = r.st.RetireStreams(r.ctx, "stream remove", []string{"s2"}, "removed")
	require.NoError(t, err)
	assert.Empty(t, said)
	r.clean("retired")
}

// A judgment of a stream removed before its notes retired with it (a remove before this
// change, or one whose retire failed) retires on the next tick, STOPPED or RUNNING, with a
// note; the other streams' judgments stay.
func TestATickRetiresTheJudgmentsOfAStreamTheTablesLack(t *testing.T) {
	t.Parallel()
	for _, running := range []bool{false, true} {
		t.Run(map[bool]string{false: "stopped", true: "running"}[running], func(t *testing.T) {
			t.Parallel()
			r := newConflictRig(t)
			j := r.stoppedStream("s2")
			r.must(store.AddStep(sprint.AddReq{Stream: "s3"}))
			r.must(store.MergeStep(sprint.MergeReq{Stream: "s3", BaseRed: "go vet ./...: exit status 1"}))
			require.Len(t, r.open(sprint.NBaseRed), 2)
			r.removeRows("s2")
			require.Len(t, r.openOn("s2"), 1, "the remove alone leaves it open")
			if running {
				_, _, _, err := r.st.SetMachine(r.ctx, true)
				require.NoError(t, err)
			}
			r.tick()
			assert.Empty(t, r.openOn("s2"), "the tick retired it")
			left := r.open(sprint.NBaseRed)
			require.Len(t, left, 1, "the stream on the table keeps its judgment")
			assert.Equal(t, sprint.StreamSubject("s3"), left[0].Subject())
			notes := r.notesOf(sprint.NStreamRetired)
			require.Len(t, notes, 1)
			assert.Equal(t, sprint.MachineActor, notes[0].Who)
			assert.Contains(t, notes[0].What, "stream s2 is off the tables: 1 open note retired ("+j.Note.ID+")")
			r.tick()
			assert.Len(t, r.notesOf(sprint.NStreamRetired), 1, "retired once")
			r.clean("retired by the tick")
		})
	}
}

// The tick raises no judgment on a stream the tables lack: its next step would be refused.
func TestNoJudgmentIsRaisedOnAStreamTheTablesLack(t *testing.T) {
	t.Parallel()
	s := &sprint.Snapshot{Work: sprint.NewTable(sprint.Work), Merge: sprint.NewTable(sprint.Merge), Now: holdT0}
	s.Work.SetRows([]string{"s1"})
	s.Merge.SetRows([]string{"s1"})
	gone := sprint.Note{Kind: sprint.Judgment, Type: sprint.NMergeLate, Stream: "s2", StreamLevel: true}
	kept := sprint.Note{Kind: sprint.Judgment, Type: sprint.NMergeLate, Stream: "s1", StreamLevel: true}
	happened := sprint.Note{Kind: sprint.Happened, Type: sprint.NBatchLanded, Stream: "s2"}
	provider := sprint.Note{Kind: sprint.Judgment, Type: sprint.NProviderFunds, Stream: sprint.ProviderSubject("p"), StreamLevel: true}
	p := sprint.WithoutStreamsOff(s, sprint.Plan{Notes: []sprint.Note{gone, kept, happened, provider},
		Units: []sprint.Unit{{Key: "x", Notes: []sprint.Note{gone, kept}}}})
	assert.Equal(t, []sprint.Note{kept, happened, provider}, p.Notes)
	assert.Equal(t, []sprint.Note{kept}, p.Units[0].Notes)

	// a primary's subject is no stream: only a stream's name or its stream subject is read
	s.Open = []sprint.Open{
		{Key: sprint.OpenKey("n.1", "s1-9"), Note: sprint.Note{ID: "n.1", Kind: sprint.Judgment, Stream: "s1"}},
		{Key: sprint.OpenKey("n.2", sprint.StreamSubject("s2")), Note: sprint.Note{ID: "n.2", Kind: sprint.Judgment, StreamLevel: true}},
		{Key: sprint.OpenKey("n.3", sprint.ProviderSubject("p")), Note: sprint.Note{ID: "n.3", Kind: sprint.Judgment, Stream: sprint.ProviderSubject("p"), StreamLevel: true}},
	}
	s.Acked = []sprint.Open{{Key: sprint.OpenKey("n.4", "s4-1"), Note: sprint.Note{ID: "n.4", Kind: sprint.Acknowledged, Stream: "s4"}}}
	assert.Equal(t, []string{"s2", "s4"}, sprint.StreamsOff(s))
	plan, _ := sprint.TickRetire(s, sprint.TickReq{})
	var closed []string
	for _, o := range plan.Closes {
		closed = append(closed, o.Key)
	}
	assert.Equal(t, []string{sprint.OpenKey("n.2", sprint.StreamSubject("s2")), sprint.OpenKey("n.4", "s4-1")}, closed)
	require.Len(t, plan.Said, 2)
	assert.True(t, strings.HasPrefix(plan.Said[0], "stream s2 is off the tables: 1 open note retired (n.2)"), plan.Said[0])
}

// stream archive retires the stream's open notes as stream remove does, but the judgment of
// a stream archived stopped, which a stopped stream keeps (check rule 9); the tick, finding
// the archived streams' rows kept, retires nothing more.
func TestArchivingAStreamRetiresItsOpenJudgments(t *testing.T) {
	t.Parallel()
	r := newConflictRig(t)
	stopped := r.stoppedStream("s3")
	r.must(store.AddStep(sprint.AddReq{Stream: "s2"}))
	raise := r.must(store.Step{Verb: "test raise", Plan: func(s *sprint.Snapshot) sprint.Plan {
		n := sprint.Note{Kind: sprint.Judgment, Type: sprint.NRaiseReadTier, Stream: "s2", StreamLevel: true, Tier: "pro",
			What: "raise the read tier of s2 to pro?", Decisions: []string{"raise", "keep"}, At: s.Now}
		return sprint.Plan{Notes: []sprint.Note{n}}
	}})
	require.Equal(t, 1, raise.Notes)
	j := r.open(sprint.NRaiseReadTier)
	require.Len(t, j, 1)
	refused, err := r.st.ArchiveStreams(r.ctx, []string{"s2", "s3"})
	require.NoError(t, err)
	require.Empty(t, refused)
	said, err := r.st.RetireStreams(r.ctx, "stream archive", []string{"s2", "s3"}, "archived")
	require.NoError(t, err)
	assert.Equal(t, []string{"stream s2 archived: 1 open note retired (" + j[0].Note.ID + ")"}, said)
	assert.Empty(t, r.openOn("s2"))
	assert.NotContains(t, r.inboxTypes(), sprint.NRaiseReadTier)
	left := r.openOn("s3")
	require.Len(t, left, 1, "a stopped stream keeps its judgment")
	assert.Equal(t, stopped.Key, left[0].Key)
	r.tick()
	assert.Len(t, r.notesOf(sprint.NStreamRetired), 1, "the tick retires nothing more")
	r.clean("archived")
}
