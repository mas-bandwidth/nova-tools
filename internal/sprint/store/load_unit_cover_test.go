package store

import (
	"errors"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The card log index on the in-memory twin: keepLogIndex indexes the log's
// lines under every name a card read finds them by and stays idempotent,
// CardLog falls back to the whole log until a tick has indexed it and answers
// the same lines once it has, a store that keeps no index reads its log whole,
// a lost reply at the "logindex" or "logat" point is named, and an unreadable
// index record is the JSON error. The Redis methods of the index (load.go:445)
// talk to a live store, so they stay with the functional tier
// (redis_functional_test.go).

// loadCoverAbout is the log's lines about one card, in order (sprint.Line.About):
// what CardLog answers, whether it reads the index or the whole log.
func loadCoverAbout(h *harness, id string) []sprint.Line {
	h.t.Helper()
	whole, err := h.st.Log(h.ctx)
	require.NoError(h.t, err)
	var out []sprint.Line
	for _, l := range whole {
		if l.About(id) {
			out = append(out, l)
		}
	}
	return out
}

// keepLogIndex indexes the log's lines and sets the cursor to the last line
// indexed; a second call with no new lines reads the cursor alone and writes
// nothing.
func TestStoreLoadCoverKeepLogIndexIndexesTheLogOnce(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(3)
	st, err := h.st.pin(h.ctx)
	require.NoError(t, err)
	ix, ok := st.B.(logIndex)
	require.True(t, ok, "the in-memory store keeps the card log index")

	cur, ids, err := ix.cardLogIDs(h.ctx, "s1-1")
	require.NoError(t, err)
	assert.Empty(t, cur, "nothing is indexed before the tick")
	assert.Empty(t, ids, "no id is indexed before the tick")

	before := h.m.Calls["logindex"]
	require.NoError(t, st.keepLogIndex(h.ctx))
	assert.Equal(t, before+2, h.m.Calls["logindex"], "the first index is the cursor read and the write")

	cur, ids, err = ix.cardLogIDs(h.ctx, "s1-1")
	require.NoError(t, err)
	require.NotEmpty(t, ids, "the index holds no id under s1-1")
	last, _, err := h.m.Tails(h.ctx)
	require.NoError(t, err)
	assert.Equal(t, last, cur, "the cursor is the last line indexed")

	before = h.m.Calls["logindex"]
	require.NoError(t, st.keepLogIndex(h.ctx))
	assert.Equal(t, before+1, h.m.Calls["logindex"], "with no new lines the index reads the cursor alone")
}

// CardLog reads the whole log's lines about a card while nothing is indexed
// (the cursor "" fallback) and the same lines once keepLogIndex has indexed it.
func TestStoreLoadCoverCardLogFallsBackToTheWholeLog(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(3)
	st, err := h.st.pin(h.ctx)
	require.NoError(t, err)
	about := loadCoverAbout(h, "s1-1")
	require.NotEmpty(t, about, "the log has no line about s1-1")

	got, err := st.CardLog(h.ctx, "s1-1")
	require.NoError(t, err)
	assert.Equal(t, about, got, "with no index CardLog reads the whole log")

	require.NoError(t, st.keepLogIndex(h.ctx))
	got, err = st.CardLog(h.ctx, "s1-1")
	require.NoError(t, err)
	assert.Equal(t, about, got, "the indexed read gives the same lines")
}

// A store that keeps no index (kvless) reads its log whole: CardLog answers
// the about-lines and keepLogIndex returns nil, touching no index.
func TestStoreLoadCoverNoIndexReadsTheWholeLog(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(3)
	_, err := h.st.pin(h.ctx)
	require.NoError(t, err)
	about := loadCoverAbout(h, "s1-1")
	require.NotEmpty(t, about, "the log has no line about s1-1")

	none := &Store{B: kvless{h.m}, Names: h.st.Names, Now: h.st.Now, NewID: h.st.NewID, Sleep: h.st.Sleep}
	_, indexed := none.B.(logIndex)
	require.False(t, indexed, "kvless hides the index")

	before := h.m.Calls["logindex"]
	got, err := none.CardLog(h.ctx, "s1-1")
	require.NoError(t, err)
	assert.Equal(t, about, got, "a store with no index reads the whole log")
	require.NoError(t, none.keepLogIndex(h.ctx))
	assert.Equal(t, before, h.m.Calls["logindex"], "a store with no index touches no index")
}

// A lost reply at "logindex" fails keepLogIndex and CardLog, naming the point;
// a lost reply at "logat" fails CardLog once the index is there, naming logat.
func TestStoreLoadCoverLostReplyNamesItsPoint(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(3)
	st, err := h.st.pin(h.ctx)
	require.NoError(t, err)

	h.m.Fail = func(point string) error {
		if point == "logindex" {
			return errors.New("lost reply")
		}
		return nil
	}
	err = st.keepLogIndex(h.ctx)
	assert.ErrorContains(t, err, "logindex", "the lost reply at logindex is named")
	_, err = st.CardLog(h.ctx, "s1-1")
	assert.ErrorContains(t, err, "logindex", "the lost reply at logindex is named")
	h.m.Fail = nil

	require.NoError(t, st.keepLogIndex(h.ctx))
	h.m.Fail = func(point string) error {
		if point == "logat" {
			return errors.New("lost reply")
		}
		return nil
	}
	_, err = st.CardLog(h.ctx, "s1-1")
	assert.ErrorContains(t, err, "logat", "the lost reply at logat is named")
	h.m.Fail = nil
}

// An index record this build cannot read ("{") makes cardLogIDs and indexLog
// return the JSON error.
func TestStoreLoadCoverUnreadableIndexIsTheJSONError(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(3)
	st, err := h.st.pin(h.ctx)
	require.NoError(t, err)
	mb, ok := st.B.(*Mem)
	require.True(t, ok, "the pinned store is the in-memory twin")
	ixKey, _ := mb.logIndexKeys()
	require.NoError(t, h.m.SetKey(h.ctx, ixKey, "{"))

	ix := st.B.(logIndex)
	_, _, err = ix.cardLogIDs(h.ctx, "s1-1")
	assert.ErrorContains(t, err, "JSON input", "an unreadable index record is the JSON error")
	err = ix.indexLog(h.ctx, map[string][]string{"s1-1": {"x"}}, "x")
	assert.ErrorContains(t, err, "JSON input", "an unreadable index record is the JSON error")
}
