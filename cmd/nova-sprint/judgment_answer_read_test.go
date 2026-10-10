package main

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// countingNotes is the twin with the notes its inbox reads hand back counted:
// what one where frame costs to show the answer waits. The reader's finding
// (judgment-answer-latencyb-t-bb.w3): AnswerWaits read every page from an empty
// cursor, so the work grew with the store's whole history though the result
// uses only the last 24 hours.
type countingNotes struct {
	*store.Mem
	read *int
}

// notesBack is the backend's tail read, when it has one.
type notesBack interface {
	NotesBack(ctx context.Context, before string, since time.Time, max int) ([]sprint.Note, []string, error)
}

func (c *countingNotes) AtEpoch(epoch uint64, old bool) store.Backend {
	m, _ := c.Mem.AtEpoch(epoch, old).(*store.Mem)
	return &countingNotes{Mem: m, read: c.read}
}

func (c *countingNotes) NotesSince(ctx context.Context, after string, max int) ([]sprint.Note, []string, error) {
	notes, ids, err := c.Mem.NotesSince(ctx, after, max)
	*c.read += len(notes)
	return notes, ids, err
}

func (c *countingNotes) NotesBack(ctx context.Context, before string, since time.Time, max int) ([]sprint.Note, []string, error) {
	b, ok := any(c.Mem).(notesBack)
	if !ok {
		return nil, nil, errors.New("the backend cannot read its notes from the tail")
	}
	notes, ids, err := b.NotesBack(ctx, before, since, max)
	*c.read += len(notes)
	return notes, ids, err
}

// TestWhereReadsOnlyTheAnswerWaitWindow pins the reader's finding on the where
// frame: with a day of old notes and the answers of the window, AnswerWaits
// reads the window's notes, not every page of the store's history
// (docs/SPEC-SPRINT.md, judgment-answer-latencyb-t-bb.w3).
func TestWhereReadsOnlyTheAnswerWaitWindow(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	read := new(int)
	counted := &countingNotes{Mem: ta.m, read: read}
	ta.a.backend = func(context.Context, string, sprint.Names) (store.Backend, error) { return counted, nil }

	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("add --stream s1 --count 10")
	ta.ok("start")
	ta.ok("tick")
	ta.ok("take --as m1 --limit 10")
	// the old history: eight failed finishes the rules answered, over a day before now
	for i := 1; i <= 8; i++ {
		ta.ok(fmt.Sprintf("finish --as m1 s1-%d.w1@1 --failed --report 'old %d'", i, i))
	}
	ta.ok("tick --answer-rules")
	ta.ok("tick") // the reworks are the next pump's
	ta.now = ta.now.Add(25 * time.Hour)
	// the window: two failed finishes the rules answered
	for i := 9; i <= 10; i++ {
		ta.ok(fmt.Sprintf("finish --as m1 s1-%d.w1@1 --failed --report 'new %d'", i, i))
	}
	ta.ok("tick --answer-rules")

	// the window's notes, counted on the same log the store reads: what a
	// bounded read returns, and how many the whole history holds
	now := ta.a.now()
	all, _, err := ta.m.NotesSince(context.Background(), "", 100000)
	require.NoError(t, err)
	inWindow := 0
	for _, n := range all {
		if !n.At.Before(now.Add(-24*time.Hour)) && !n.At.After(now) {
			inWindow++
		}
	}
	require.Greater(t, len(all), inWindow, "the history holds notes before the window")

	st := &store.Store{B: counted, Names: sprint.Names{}, Now: ta.a.now}
	*read = 0
	got, err := st.AnswerWaits(context.Background(), 24*time.Hour)
	require.NoError(t, err)
	require.GreaterOrEqual(t, got.N, 1, "the window's answer is counted")
	assert.LessOrEqual(t, *read, inWindow, "where reads the window's notes (%d), not the store's whole history (%d)", inWindow, len(all))
}
