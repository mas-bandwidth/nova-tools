package store

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// StatsArchiveKey is the archive record of the tidy at at, to the nanosecond,
// in UTC, with its nonce last. A non-UTC time pins the conversion: a key that
// dropped .UTC() would keep the zone.
func TestStoreStatsTidyCoverArchiveKey(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 10, 6, 12, 34, 56, 789, time.FixedZone("X", 5*60*60))
	key := StatsArchiveKey(at, "deadbeef")
	assert.Equal(t, "stats:archive:"+at.UTC().Format(time.RFC3339Nano)+"-deadbeef", key)
	assert.Equal(t, "stats:archive:2026-10-06T07:34:56.000000789Z-deadbeef", key)
	assert.True(t, strings.HasSuffix(key, "Z-deadbeef"), "the key ends with the UTC time and the nonce: %s", key)
	assert.NotContains(t, key, "+05:00", "the key is in UTC: %s", key)
}

// archiveNonce is eight hex characters (four random bytes) and two calls
// differ: a constant or a short nonce fails.
func TestStoreStatsTidyCoverArchiveNonce(t *testing.T) {
	t.Parallel()
	nonce := archiveNonce()
	assert.Len(t, nonce, 8)
	b, err := hex.DecodeString(nonce)
	require.NoError(t, err, "the nonce is hex: %s", nonce)
	assert.Len(t, b, 4)
	again := archiveNonce()
	assert.NotEqual(t, nonce, again, "two nonces differ")
}

// count is the result's moved and kept over its rows, and a second count
// resets rather than adds: an accumulating count would read 6 and 2.
func TestStoreStatsTidyCoverTidyResultCount(t *testing.T) {
	t.Parallel()
	res := TidyResult{Record: StatsArchive{Rows: []sprint.TidyRow{
		{Moved: []sprint.TidyCard{{ID: "a"}, {ID: "b"}}, Kept: []sprint.TidyCard{{ID: "c"}}},
		{Moved: []sprint.TidyCard{{ID: "d"}}, Kept: nil},
	}}}
	res.count()
	assert.Equal(t, 3, res.Moved)
	assert.Equal(t, 1, res.Kept)
	res.count()
	assert.Equal(t, 3, res.Moved, "a second count resets rather than adds")
	assert.Equal(t, 1, res.Kept, "a second count resets rather than adds")
}

// statsRecordOf is the zero record for an absent record, an empty string and
// an unreadable one; the unreadable one leaves one note that TidyStats returns
// in Said.
func TestStoreStatsTidyCoverStatsRecordOf(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	// absent: no value, no note
	assert.Equal(t, StatsRecord{}, h.st.statsRecordOf("", false))
	// empty string: the same zero record
	assert.Equal(t, StatsRecord{}, h.st.statsRecordOf("", true))

	// unreadable JSON: the zero record, one note
	h.st.stats().takeNotes()
	assert.Equal(t, StatsRecord{}, h.st.statsRecordOf("not-json", true))
	notes := h.st.stats().takeNotes()
	require.Len(t, notes, 1, "the unreadable record says once")
	assert.Contains(t, notes[0], "unreadable")

	// a readable one comes back as it stands
	valid := StatsRecord{Epoch: 7, Reason: "kept"}
	data, err := json.Marshal(valid)
	require.NoError(t, err)
	assert.Equal(t, valid, h.st.statsRecordOf(string(data), true))

	// TidyStats says it once, in Said
	h2 := newHarness(t)
	require.NoError(t, h2.m.SetKey(h2.ctx, keyStats, "not-json"))
	res, err := h2.st.TidyStats(h2.ctx, TidyReq{Kinds: []string{sprint.TidyRoutes}, DryRun: true})
	require.NoError(t, err)
	require.Len(t, res.Said, 1, "TidyStats returns the unreadable record's note in Said")
	assert.Contains(t, res.Said[0], "unreadable")
}

// StatsTidied and StatsSince on a store over kvless: the zero record and the
// zero time, no error (the store keeps no records).
func TestStoreStatsTidyCoverKvless(t *testing.T) {
	t.Parallel()
	st := &Store{B: kvless{NewMem()}}
	ctx := context.Background()

	rec, err := st.StatsTidied(ctx)
	require.NoError(t, err)
	assert.Equal(t, StatsRecord{}, rec)

	since, err := st.StatsSince(ctx, "")
	require.NoError(t, err)
	assert.True(t, since.IsZero())

	since, err = st.StatsSince(ctx, sprint.TidyRoutes)
	require.NoError(t, err)
	assert.True(t, since.IsZero())
}

// StatsSince is zero with no record and with a record of another epoch, the
// record's Since for any kind, and a named kind's own time.
func TestStoreStatsTidyCoverStatsSince(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ctx := context.Background()

	// no record: the zero time, no error
	since, err := h.st.StatsSince(ctx, "")
	require.NoError(t, err)
	assert.True(t, since.IsZero())
	since, err = h.st.StatsSince(ctx, sprint.TidyRoutes)
	require.NoError(t, err)
	assert.True(t, since.IsZero())

	// the record's own epoch: "" is its Since, a named kind is that kind's time
	at := t0.Add(2 * time.Hour)
	routesAt := t0.Add(3 * time.Hour)
	rec := StatsRecord{Epoch: h.st.epoch, Since: at, Kinds: map[string]time.Time{sprint.TidyRoutes: routesAt}}
	data, err := json.Marshal(rec)
	require.NoError(t, err)
	require.NoError(t, h.m.SetKey(ctx, keyStats, string(data)))

	since, err = h.st.StatsSince(ctx, "")
	require.NoError(t, err)
	assert.Equal(t, at, since)
	since, err = h.st.StatsSince(ctx, sprint.TidyRoutes)
	require.NoError(t, err)
	assert.Equal(t, routesAt, since)

	// a record of another epoch: the zero time
	rec.Epoch = h.st.epoch + 1
	data, err = json.Marshal(rec)
	require.NoError(t, err)
	require.NoError(t, h.m.SetKey(ctx, keyStats, string(data)))
	since, err = h.st.StatsSince(ctx, "")
	require.NoError(t, err)
	assert.True(t, since.IsZero(), "another epoch's record is no tidy here")
}

// TidyStats on the twin: a dry run plans and writes nothing; a routes tidy
// writes the archive done and the record naming it; a second within a minute
// is refused, nothing written; one past the minute runs.
func TestStoreStatsTidyCoverTidyStats(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ctx := context.Background()
	at := h.st.Now().UTC()

	// a dry run of the routes kind: planned, and nothing written
	dry, err := h.st.TidyStats(ctx, TidyReq{Kinds: []string{sprint.TidyRoutes}, Reason: "a dry run", DryRun: true})
	require.NoError(t, err)
	assert.True(t, dry.DryRun)
	assert.NotEmpty(t, dry.Archive)
	assert.Equal(t, ArchivePlanned, dry.Record.State, "a dry run plans")
	_, ok, err := h.m.GetKey(ctx, dry.Archive)
	require.NoError(t, err)
	assert.False(t, ok, "a dry run writes no archive")
	_, ok, err = h.m.GetKey(ctx, keyStats)
	require.NoError(t, err)
	assert.False(t, ok, "a dry run leaves the stats record unchanged")
	rec, err := h.st.StatsTidied(ctx)
	require.NoError(t, err)
	assert.Zero(t, rec.Since, "a dry run records nothing")

	// the routes tidy: the archive done, the record naming it
	res, err := h.st.TidyStats(ctx, TidyReq{Kinds: []string{sprint.TidyRoutes}, Reason: "routes tidy"})
	require.NoError(t, err)
	assert.False(t, res.DryRun)
	assert.Empty(t, res.Refused)
	assert.NotEmpty(t, res.Archive)
	raw, ok, err := h.m.GetKey(ctx, res.Archive)
	require.NoError(t, err)
	require.True(t, ok, "the archive is written")
	var arch StatsArchive
	require.NoError(t, json.Unmarshal([]byte(raw), &arch))
	assert.Equal(t, ArchiveDone, arch.State)
	rec, err = h.st.StatsTidied(ctx)
	require.NoError(t, err)
	assert.Equal(t, at, rec.Since, "the record's Since is the clock")
	assert.Equal(t, "routes tidy", rec.Reason)
	assert.Equal(t, "tester", rec.By)
	assert.Equal(t, at, rec.Kinds[sprint.TidyRoutes], "Kinds[routes] is the clock")
	assert.Equal(t, []string{res.Archive}, rec.Archives, "Archives names the key")

	// a second tidy 59 s later is refused, nothing written
	h.tick(59 * time.Second)
	second, err := h.st.TidyStats(ctx, TidyReq{Kinds: []string{sprint.TidyRoutes}, Reason: "second"})
	require.NoError(t, err)
	assert.Contains(t, second.Refused, "a second within 1m0s is refused, nothing written")
	assert.Empty(t, second.Archive, "a refusal writes no archive")
	rec, err = h.st.StatsTidied(ctx)
	require.NoError(t, err)
	assert.Equal(t, "routes tidy", rec.Reason, "the refusal keeps the first reason")
	assert.Equal(t, []string{res.Archive}, rec.Archives, "the refusal writes no archive")

	// one at 61 s runs
	h.tick(2 * time.Second)
	third, err := h.st.TidyStats(ctx, TidyReq{Kinds: []string{sprint.TidyRoutes}, Reason: "third"})
	require.NoError(t, err)
	assert.Empty(t, third.Refused, "61 s on, it runs")
	rec, err = h.st.StatsTidied(ctx)
	require.NoError(t, err)
	assert.Equal(t, "third", rec.Reason)
	assert.Len(t, rec.Archives, 2)
}
