package main

import (
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/stretchr/testify/assert"
)

// where --all: a tier is named once in the readers row's tiers cell, however the rows spell it.
func TestReaderTiersSummaryNamesEachTierOnce(t *testing.T) {
	t.Parallel()
	tb := ntable.Table{Rows: []ntable.Row{
		{Key: "a", Texts: map[string]string{sprint.ReaderTiers: "flash,pro,heavy"}},
		{Key: "b", Texts: map[string]string{sprint.ReaderTiers: "flash,pro,heavy,frontier"}},
	}}
	assert.Equal(t, "flash,pro,heavy,frontier", readerTiersSummary(tb))
}

// A hold's until in the past is not printed.
func TestStatusCellOmitsAnExpiredUntil(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	a := &app{}
	f := store.FriendRow{Status: sprint.Up, Until: now.Add(-time.Hour)}
	assert.Equal(t, sprint.Up, a.statusCell(f, now))
	f.Reason = "resting"
	assert.Equal(t, sprint.Up+" (resting)", a.statusCell(f, now))
}
