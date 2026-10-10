package store

import (
	"encoding/json"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The stream archive and unarchive verbs on the package's own in-memory store
// (ArchiveStreams, UnarchiveStreams, archive and putArchive, archive.go): the
// rows of the work and merge tables hidden and drawn, and the archive record
// (keyArchive) the coordinator's kept streams as it is stored.

// archiveSeam is the archive record as it is stored, read through the KV seam
// (GetKey of keyArchive, then JSON), never from a printed line.
func (h *harness) archiveSeam() archiveRecord {
	h.t.Helper()
	raw, ok, err := h.m.GetKey(h.ctx, keyArchive)
	require.NoError(h.t, err)
	require.True(h.t, ok, "the archive record was not written")
	var rec archiveRecord
	require.NoError(h.t, json.Unmarshal([]byte(raw), &rec))
	return rec
}

// hidden is whether the row of the named table's stored shape reads Hidden.
func (h *harness) hidden(table, row string) bool {
	h.t.Helper()
	shapes, err := h.m.Shapes(h.ctx, []string{h.st.Names.Table(table)})
	require.NoError(h.t, err)
	for _, r := range shapes[0].Rows {
		if r.Key == row {
			return r.Hidden
		}
	}
	require.Failf(h.t, "no such row", "%s has no row %s", table, row)
	return false
}

// TestStoreArchiveCoverArchiveLandedStream pins ArchiveStreams on a stream whose
// every card has landed: no refusal, both rows hidden, and s1 out of the kept
// record.
func TestStoreArchiveCoverArchiveLandedStream(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(2)
	h.landThrough("s1", "s1-1", "s1-2")

	refused, err := h.st.ArchiveStreams(h.ctx, []string{"s1"})
	require.NoError(t, err)
	require.Empty(t, refused)

	assert.True(t, h.hidden(sprint.Work, "s1"), "the work row is hidden")
	assert.True(t, h.hidden(sprint.Merge, "s1"), "the merge row is hidden")

	rec, err := h.st.archiveRecord(h.ctx)
	require.NoError(t, err)
	assert.NotContains(t, rec.Kept, "s1", "an archived stream is out of the kept record")
	assert.NotContains(t, h.archiveSeam().Kept, "s1", "the stored record keeps no s1")
	h.clean("archived")
}

// TestStoreArchiveCoverRefusesUnlandedCard pins ArchiveStreams on a stream that
// holds a card not landed: one refusal naming the card and "not landed", and
// nothing hidden.
func TestStoreArchiveCoverRefusesUnlandedCard(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(2) // s1-1 and s1-2 stay ready: not landed

	refused, err := h.st.ArchiveStreams(h.ctx, []string{"s1"})
	require.NoError(t, err)
	require.Len(t, refused, 1)
	assert.Equal(t, "s1", refused[0].Key)
	assert.Contains(t, refused[0].Why, "s1-1 ready")
	assert.Contains(t, refused[0].Why, "not landed")

	assert.False(t, h.hidden(sprint.Work, "s1"), "a refusal hides no row")
	assert.False(t, h.hidden(sprint.Merge, "s1"), "a refusal hides no row")
	_, ok, err := h.m.GetKey(h.ctx, keyArchive)
	require.NoError(t, err)
	assert.False(t, ok, "a refusal writes no record")
}

// TestStoreArchiveCoverUnknownAndGoodStream pins ArchiveStreams on a list of a
// good and an unknown stream: all or none, so both are refused and the good
// stream is not hidden either.
func TestStoreArchiveCoverUnknownAndGoodStream(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(1)
	h.landThrough("s1", "s1-1")

	refused, err := h.st.ArchiveStreams(h.ctx, []string{"s1", "zz"})
	require.NoError(t, err)
	require.Len(t, refused, 2, "all or none: every name is refused")
	byKey := map[string]string{}
	for _, r := range refused {
		byKey[r.Key] = r.Why
	}
	assert.Contains(t, byKey["zz"], "no stream zz")
	assert.Contains(t, byKey["s1"], "all or none")

	assert.False(t, h.hidden(sprint.Work, "s1"), "the good stream is not hidden either")
	_, ok, err := h.m.GetKey(h.ctx, keyArchive)
	require.NoError(t, err)
	assert.False(t, ok, "nothing was written")
}

// TestStoreArchiveCoverUnarchiveArchivedStream pins UnarchiveStreams on archived
// streams: the rows are drawn again and Kept holds them, sorted and without
// duplicates as putArchive writes it.
func TestStoreArchiveCoverUnarchiveArchivedStream(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(1)
	h.landThrough("s1", "s1-1")
	h.must(AddStep(sprint.AddReq{Brief: proBrief, Stream: "s2", Count: 1}))
	h.landThrough("s2", "s2-1")

	refused, err := h.st.ArchiveStreams(h.ctx, []string{"s1", "s2"})
	require.NoError(t, err)
	require.Empty(t, refused)
	require.True(t, h.hidden(sprint.Work, "s1"))

	// a duplicate and a reversed order: putArchive sorts and compacts (archive.go:113-114)
	refused, err = h.st.UnarchiveStreams(h.ctx, []string{"s2", "s1", "s2"})
	require.NoError(t, err)
	require.Empty(t, refused)

	assert.False(t, h.hidden(sprint.Work, "s1"), "the work row is drawn again")
	assert.False(t, h.hidden(sprint.Merge, "s1"), "the merge row is drawn again")
	assert.False(t, h.hidden(sprint.Work, "s2"), "the work row is drawn again")
	assert.Equal(t, []string{"s1", "s2"}, h.archiveSeam().Kept, "sorted and without duplicates")
	h.clean("unarchived")
}

// TestStoreArchiveCoverUnarchiveNotArchived pins UnarchiveStreams on a stream
// that is not archived: the rule's refusal.
func TestStoreArchiveCoverUnarchiveNotArchived(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(1)
	h.landThrough("s1", "s1-1")

	refused, err := h.st.UnarchiveStreams(h.ctx, []string{"s1"})
	require.NoError(t, err)
	require.Len(t, refused, 1)
	assert.Equal(t, "s1", refused[0].Key)
	assert.Contains(t, refused[0].Why, "is not archived; nothing was changed")
}

// TestStoreArchiveCoverArchiveAgainAfterUnarchive pins that archiving again
// after an unarchive takes the stream out of the kept record (archive.go:63).
func TestStoreArchiveCoverArchiveAgainAfterUnarchive(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(1)
	h.landThrough("s1", "s1-1")

	refused, err := h.st.ArchiveStreams(h.ctx, []string{"s1"})
	require.NoError(t, err)
	require.Empty(t, refused)
	refused, err = h.st.UnarchiveStreams(h.ctx, []string{"s1"})
	require.NoError(t, err)
	require.Empty(t, refused)
	require.Equal(t, []string{"s1"}, h.archiveSeam().Kept, "the unarchive kept s1")

	refused, err = h.st.ArchiveStreams(h.ctx, []string{"s1"})
	require.NoError(t, err)
	require.Empty(t, refused)
	assert.True(t, h.hidden(sprint.Work, "s1"), "the stream is hidden again")
	assert.NotContains(t, h.archiveSeam().Kept, "s1", "and out of the kept record again")
}
