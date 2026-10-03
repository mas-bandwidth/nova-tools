package main

import (
	"context"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// where --json carries one attention object for a program: waiting_on_coordinator
// (every open judgment subject plus every merge stream stopped) versus takeable
// (every ready work card plus every asked read card), with the raw components
// kept separate and one_person_bound as a heuristic flag.
func TestWhereJSONCarriesAttention(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1,m2")
	for _, s := range []string{"s1", "s2"} {
		ta.ok("add --stream " + s + " --count 2")
	}
	ta.ok("start")
	ta.ok("tick")
	var w whereView
	ta.json("where", &w)

	// The counts are the tables' own, re-derived here to hold the view to its source.
	open, err := ta.m.OpenNotes(context.Background())
	require.NoError(t, err)
	stoppedMerge := 0
	for _, row := range w.Tables[sprint.Merge] {
		if row[sprint.StateCol] == sprint.StreamStopped {
			stoppedMerge++
		}
	}
	readyWork, askedReaders := 0, 0
	for _, row := range w.Tables[sprint.Work] {
		if n, err := strconv.Atoi(row[sprint.Ready]); err == nil {
			readyWork += n
		}
	}
	for _, row := range w.Tables[sprint.Readers] {
		if n, err := strconv.Atoi(row[sprint.Asked]); err == nil {
			askedReaders += n
		}
	}
	require.Equal(t, attention{
		OpenJudgmentSubjects: len(open),
		StoppedMergeCards:    stoppedMerge,
		Ready:                readyWork,
		Asked:                askedReaders,
		WaitingOnCoordinator: len(open) + stoppedMerge,
		Takeable:             readyWork + askedReaders,
		OnePersonBound:       len(open)+stoppedMerge > readyWork+askedReaders,
	}, w.Attention, "attention: %+v", w.Attention)
}
