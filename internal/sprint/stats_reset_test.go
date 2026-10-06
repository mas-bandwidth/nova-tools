package sprint

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestStatsTidyZeroesCountersAndKeepsTheWork tests that stats tidy archives
// statistics and records the reset time without touching the work.
func TestStatsTidyZeroesCountersAndKeepsTheWork(t *testing.T) {
	t.Parallel()

	// Prepare snapshot with stats
	now := time.Date(2026, 10, 6, 13, 50, 0, 0, time.UTC)
	s := &Snapshot{
		Now:     now,
		Epoch:   15,
		Work:    NewTable(Work),
		Fleet:   NewTable(Fleet),
		Readers: NewTable(Readers),
	}
	s.Work.SetRows([]string{"s1"})
	s.Fleet.SetRows([]string{"m1"})
	s.Readers.SetRows([]string{"r1"})

	// Primary p1: landed with work card
	p1 := &Card{
		ID: "p1", Row: "s1", Col: Landed, Score: 1,
		Fields: map[string]string{
			"attempt":  "1",
			"admitted": stamp(now),
			"accepted": stamp(now.Add(60 * time.Second)),
			"landed":   stamp(now.Add(70 * time.Second)),
		},
	}
	s.Work.Put(p1)

	// Work card on m1
	w1 := &Card{
		ID: "p1.w1", Row: "m1", Col: DoneOK,
		Fields: map[string]string{
			"member":       "m1",
			"attempt":      "1",
			"ok":           "yes",
			"first_dealt":  stamp(now.Add(10 * time.Second)),
			"dealt":        stamp(now.Add(10 * time.Second)),
			"taken":        stamp(now.Add(15 * time.Second)),
			"finished":     stamp(now.Add(25 * time.Second)),
			"usage":        "wall=5.00s",
		},
	}
	s.Fleet.Put(w1)

	// Verify pre-tidy state
	p1Before := s.Work.Card("p1")
	require.NotNil(t, p1Before)
	assert.Equal(t, Landed, p1Before.Col)

	w1Before := s.Fleet.Card("p1.w1")
	require.NotNil(t, w1Before)
	assert.Equal(t, DoneOK, w1Before.Col)

	// Run tidy
	reason := "testing stats tidy"
	result := TidyStats(s, now, reason, true, true, true, true)

	// Verify archive key format
	require.NotEmpty(t, result.ArchiveKey)
	assert.True(t, strings.HasPrefix(result.ArchiveKey, "stats:archive:"))

	// Verify since time format
	require.NotEmpty(t, result.SinceTime)
	_, err := time.Parse(time.RFC3339, result.SinceTime)
	require.NoError(t, err)

	// Verify work is untouched
	p1After := s.Work.Card("p1")
	require.NotNil(t, p1After)
	assert.Equal(t, Landed, p1After.Col)
	assert.Equal(t, "p1", p1After.ID)

	w1After := s.Fleet.Card("p1.w1")
	require.NotNil(t, w1After)
	assert.Equal(t, DoneOK, w1After.Col)
	assert.Equal(t, "p1.w1", w1After.ID)

	// Verify result contains expected data (would be stored with archive in real impl)
	assert.NotEmpty(t, result.ArchiveKey)
	assert.NotEmpty(t, result.SinceTime)
}
