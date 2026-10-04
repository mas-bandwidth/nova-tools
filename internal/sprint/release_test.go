package sprint

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
)

// releaseShape is the work table as where reads it (its cells, never a card):
// each stream's row with the counts given, in the table's columns
// (docs/SPEC-SPRINT.md section 1).
func releaseShape(t *testing.T, rows map[string]map[string]int64, order ...string) ntable.Table {
	t.Helper()
	w := Names{}.Definitions()[0]
	require.Equal(t, Work, w.Name)
	for _, st := range order {
		r := ntable.Row{Key: st, Cells: make([]ntable.Cell, len(w.Columns)), Texts: map[string]string{Cost: "-"}}
		for col, n := range rows[st] {
			j := w.Column(col)
			require.GreaterOrEqual(t, j, 0, "no column %s", col)
			r.Cells[j].Count = n
		}
		w.Rows = append(w.Rows, r)
	}
	return w
}

// A stream's release is its control card's field, written by stream set <s>
// --release <name> and taken off by --release none; where --release <name>
// counts the cards left in each stream of the release, every card on its work
// row not landed, from the table's cells and the control cards (the
// coordinator's list of 2026-10-04, item 20: which streams are v1.2.0 and which
// nova-sprint v1.0.0 lived in the coordinator's head; docs/SPEC-SPRINT.md
// section 11, stream set and where).
func TestStreamReleaseAndWhereReleaseCountCardsLeft(t *testing.T) {
	t.Parallel()
	t.Run("stream set records the release on each stream's control card", func(t *testing.T) {
		t.Parallel()
		p := Set(settingsSnapshot(nil, "s1", "s2"), SetReq{Streams: []string{"s1", "s2"}, Release: "v1.2.0", Who: "coord"})
		require.Empty(t, p.Refused)
		assert.Empty(t, p.Props, "a release is a stream's, no property of the sprint's")
		require.Len(t, p.Units, 2)
		for i, st := range []string{"s1", "s2"} {
			u := p.Units[i]
			assert.Equal(t, CtlID(st), u.Key)
			assert.Equal(t, "stream "+st+" release v1.2.0", u.Moved)
			require.Len(t, u.Changes, 1)
			assert.Equal(t, Merge, u.Changes[0].Table)
			assert.Equal(t, map[string]string{FieldRelease: "v1.2.0"}, u.Changes[0].Entry.Set)
			assert.Empty(t, u.Changes[0].Entry.Unset)
		}
	})
	t.Run("a read tier and a release in one stream set are one change", func(t *testing.T) {
		t.Parallel()
		p := Set(settingsSnapshot(nil, "s1"), SetReq{Streams: []string{"s1"}, ReadTier: "flash", Release: "nova-sprint-v1.0.0", Who: "coord"})
		require.Empty(t, p.Refused)
		require.Len(t, p.Units, 1)
		assert.Equal(t, "stream s1 read-tier flash, release nova-sprint-v1.0.0", p.Units[0].Moved)
		require.Len(t, p.Units[0].Changes, 1)
		assert.Equal(t, map[string]string{FieldReadTier: "flash", FieldRelease: "nova-sprint-v1.0.0"}, p.Units[0].Changes[0].Entry.Set)
	})
	t.Run("none takes a stream out of its release", func(t *testing.T) {
		t.Parallel()
		s := settingsSnapshot(nil, "s1")
		s.Merge.Card(CtlID("s1")).Fields[FieldRelease] = "v1.2.0"
		p := Set(s, SetReq{Streams: []string{"s1"}, Release: ReleaseNone, Who: "coord"})
		require.Empty(t, p.Refused)
		require.Len(t, p.Units, 1)
		assert.Equal(t, "stream s1 release none", p.Units[0].Moved)
		require.Len(t, p.Units[0].Changes, 1)
		assert.Nil(t, p.Units[0].Changes[0].Entry.Set)
		assert.Equal(t, []string{FieldRelease}, p.Units[0].Changes[0].Entry.Unset, "the field is unset, not written over")
	})
	t.Run("a refusal names the problem and writes nothing", func(t *testing.T) {
		t.Parallel()
		for _, c := range []struct {
			name string
			req  SetReq
			why  string
		}{
			{"a release on the sprint", SetReq{Release: "v1.2.0", Who: "coord"}, "--release is a stream's"},
			{"a release name that is not one word", SetReq{Streams: []string{"s1"}, Release: "v1 2", Who: "coord"}, "--release wants one word"},
			{"a release of a stream that is not a stream", SetReq{Streams: []string{"nope"}, Release: "v1.2.0", Who: "coord"}, "no stream nope"},
			{"a release by another actor", SetReq{Streams: []string{"s1"}, Release: "v1.2.0", Who: "someone"}, "coordinator's alone"},
			{"a stream set with nothing to set", SetReq{Streams: []string{"s1"}, Who: "coord"}, "nothing to set: --read-tier or --release"},
		} {
			t.Run(c.name, func(t *testing.T) {
				t.Parallel()
				p := Set(settingsSnapshot(nil, "s1"), c.req)
				require.Len(t, p.Refused, 1, "refused whole: %+v", p)
				assert.Contains(t, p.Refused[0].Why, c.why)
				assert.Empty(t, p.Units, "a refusal writes nothing")
				assert.Empty(t, p.Props, "a refusal writes nothing")
			})
		}
	})
	t.Run("where --release counts the cards left in each stream of the release", func(t *testing.T) {
		t.Parallel()
		work := releaseShape(t, map[string]map[string]int64{
			"s1": {Waiting: 3, Ready: 2, Working: 1, Landed: 4}, // 6 left
			"s2": {Review: 1, Merging: 2, Landed: 9},            // 3 left
			"s3": {Waiting: 5, Landed: 1},                       // 5 left, another release
			"s4": {Ready: 7},                                    // in no release
			"s5": {Landed: 6},                                   // every card landed: 0 left
		}, "s1", "s2", "s3", "s4", "s5")
		clocks := []StreamClock{
			{Stream: "s1", Release: "v1.2.0"},
			{Stream: "s2", Release: "v1.2.0"},
			{Stream: "s3", Release: "nova-sprint-v1.0.0"},
			{Stream: "s4"},
			{Stream: "s5", Release: "v1.2.0"},
		}
		got, ok := ReleaseOf(work, clocks, "v1.2.0")
		require.True(t, ok)
		assert.Equal(t, ReleaseCount{Release: "v1.2.0", Left: 9, Streams: []ReleaseStream{
			{Stream: "s1", Left: 6}, {Stream: "s2", Left: 3}, {Stream: "s5", Left: 0},
		}}, got)
		other, ok := ReleaseOf(work, clocks, "nova-sprint-v1.0.0")
		require.True(t, ok)
		assert.Equal(t, int64(5), other.Left)
		_, ok = ReleaseOf(work, clocks, "v9")
		assert.False(t, ok, "a release no stream is in is none")
		assert.Equal(t, []string{"nova-sprint-v1.0.0", "v1.2.0"}, ReleaseNames(clocks), "every release named, sorted, once")
	})
}
