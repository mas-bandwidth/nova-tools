package sprint

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestStreamReleaseAndWhereReleaseCountCardsLeft tests:
//  1. stream set <s> --release <name> records the release on the stream's control card
//     (docs/SPEC-SPRINT.md section 11, stream set).
//  2. where --release prints and counts cards left per release from the stream rows
//     (docs/SPEC-SPRINT.md section 11, where).
func TestStreamReleaseAndWhereReleaseCountCardsLeft(t *testing.T) {
	t.Parallel()

	s := &Snapshot{Coordinator: "coordinator", Work: NewTable(Work), Merge: NewTable(Merge)}
	s.Work.SetRows([]string{"s1", "s2", "s3"})
	for _, st := range []string{"s1", "s2", "s3"} {
		s.Merge.Put(&Card{ID: CtlID(st), Row: st, Col: Ctl, Rev: 1, Fields: map[string]string{}})
	}

	// 1. Setting release on streams s1 and s2 to v1.0.0.
	p := Set(s, SetReq{Streams: []string{"s1", "s2"}, Release: "v1.0.0", Who: "coordinator"})
	require.Empty(t, p.Refused, "coordinator setting release is accepted")
	require.Len(t, p.Units, 2)
	assert.Equal(t, "stream s1 release v1.0.0", p.Units[0].Moved)
	assert.Equal(t, "stream s2 release v1.0.0", p.Units[1].Moved)
	for _, u := range p.Units {
		for _, ch := range u.Changes {
			if ch.Table == Merge {
				c := s.Merge.Card(ch.Entry.ID)
				require.NotNil(t, c)
				for k, v := range ch.Entry.Set {
					c.Fields[k] = v
				}
				for _, k := range ch.Entry.Unset {
					delete(c.Fields, k)
				}
			}
		}
	}
	assert.Equal(t, "v1.0.0", s.StreamCtl("s1").F(FieldRelease))
	assert.Equal(t, "v1.0.0", s.StreamCtl("s2").F(FieldRelease))
	assert.Equal(t, "", s.StreamCtl("s3").F(FieldRelease))

	// Set s3 to v1.2.0.
	p = Set(s, SetReq{Streams: []string{"s3"}, Release: "v1.2.0", Who: "coordinator"})
	require.Empty(t, p.Refused)
	require.Len(t, p.Units, 1)
	assert.Equal(t, "stream s3 release v1.2.0", p.Units[0].Moved)
	for _, u := range p.Units {
		for _, ch := range u.Changes {
			if ch.Table == Merge {
				c := s.Merge.Card(ch.Entry.ID)
				require.NotNil(t, c)
				for k, v := range ch.Entry.Set {
					c.Fields[k] = v
				}
			}
		}
	}
	assert.Equal(t, "v1.2.0", s.StreamCtl("s3").F(FieldRelease))

	// Clear s2's release with default.
	p = Set(s, SetReq{Streams: []string{"s2"}, Release: "default", Who: "coordinator"})
	require.Empty(t, p.Refused)
	require.Len(t, p.Units, 1)
	assert.Equal(t, "stream s2 release none", p.Units[0].Moved)
	for _, u := range p.Units {
		for _, ch := range u.Changes {
			if ch.Table == Merge {
				c := s.Merge.Card(ch.Entry.ID)
				require.NotNil(t, c)
				for _, k := range ch.Entry.Unset {
					delete(c.Fields, k)
				}
			}
		}
	}
	assert.Equal(t, "", s.StreamCtl("s2").F(FieldRelease), "default clears the stream's release")

	// Clear s1's release with none.
	p = Set(s, SetReq{Streams: []string{"s1"}, Release: "none", Who: "coordinator"})
	require.Empty(t, p.Refused)
	require.Len(t, p.Units, 1)
	assert.Equal(t, "stream s1 release none", p.Units[0].Moved)
	for _, u := range p.Units {
		for _, ch := range u.Changes {
			if ch.Table == Merge {
				c := s.Merge.Card(ch.Entry.ID)
				require.NotNil(t, c)
				for _, k := range ch.Entry.Unset {
					delete(c.Fields, k)
				}
			}
		}
	}
	assert.Equal(t, "", s.StreamCtl("s1").F(FieldRelease), "none clears the stream's release")

	// 2. Test cards left per release folded from stream clocks:
	clocks := []StreamClock{
		{Stream: "s1", Release: "v1.0.0"},
		{Stream: "s2", Release: ""},
		{Stream: "s3", Release: "v1.2.0"},
	}
	streamLeft := map[string]int64{
		"s1": 3,
		"s2": 1,
		"s3": 2,
	}

	allCounts := WhereReleasesCountCardsLeft(clocks, streamLeft)
	assert.Equal(t, int64(3), allCounts["v1.0.0"])
	assert.Equal(t, int64(2), allCounts["v1.2.0"])
	assert.Equal(t, int64(0), allCounts["v9.9.9"])
	assert.NotContains(t, allCounts, "")

	// 3. Test refusals:
	// A non-coordinator cannot set stream release.
	p = Set(s, SetReq{Streams: []string{"s1"}, Release: "v1.0.0", Who: "worker"})
	require.Len(t, p.Refused, 1)
	assert.Contains(t, p.Refused[0].Why, "is the coordinator's alone")

	// Setting release on a nonexistent stream is refused.
	p = Set(s, SetReq{Streams: []string{"no-such-stream"}, Release: "v1.0.0", Who: "coordinator"})
	require.Len(t, p.Refused, 1)
	assert.Contains(t, p.Refused[0].Why, "no stream no-such-stream")

	// --release is a stream's, not the sprint's.
	p = Set(s, SetReq{Release: "v1.0.0", Who: "coordinator"})
	require.Len(t, p.Refused, 1)
	assert.Contains(t, p.Refused[0].Why, "--release is a stream's, not the sprint's")
}
