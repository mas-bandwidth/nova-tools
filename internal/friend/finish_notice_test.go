package friend

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/mas-bandwidth/nova-tools/internal/bus/bustest"
	"github.com/stretchr/testify/require"
)

func TestFinishNoteRetrySurvivesLostResponseAndRestart(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	dir, stateDir := t.TempDir(), t.TempDir()
	card := Card{ID: "c1", Outbox: filepath.Join(dir, "outbox", "c1~15")}
	require.NoError(t, os.MkdirAll(card.Outbox, 0o755))
	require.NoError(t, os.WriteFile(card.Report(), []byte("Verdict: LAND\n"), 0o644))
	started := LaneState{Started: map[string]Started{"c1~15": {Lane: 1, Card: card, At: now}}}
	require.NoError(t, WriteLanes(stateDir, started))
	store := bustest.NewFake(now, "coordinator", "worker")
	store.Friends = []string{"coordinator", "worker"}
	records := []string{}
	d := &Daemon{Friend: "worker", Dir: dir, StateDir: stateDir, Coordinator: "coordinator", Record: func(s string) { records = append(records, s) }, Rules: func() LaneRules { return LaneRules{} }}
	d.m = Start(now)
	l := &loop{d: d, ctx: context.Background(), b: &bus.Bus{Store: store}, lanes: &laneSet{state: started}}
	require.NoError(t, l.finishNote(card, time.Minute, now))
	path := finishNoticePath(d, "c1~15")
	var saved finishNotice
	found, err := read(path, &saved)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, finishNoticeToken("worker", "c1~15"), saved.Message.Token)
	require.Contains(t, saved.Message.Body, "LAND")

	store.Fail = errors.New("store unavailable")
	l.sendFinishNotes(now)
	require.Equal(t, 0, store.Len(bus.StreamOf("coordinator")))
	require.FileExists(t, path)
	found, err = read(path, &saved)
	require.NoError(t, err)
	require.True(t, found)
	require.True(t, saved.AttemptedAt.IsZero(), "a read-only store failure does not start the uncertain-send window")
	store.Fail = nil
	store.Lose = errors.New("committed response lost")
	l.sendFinishNotes(now.Add(time.Second))
	require.Equal(t, 1, store.Len(bus.StreamOf("coordinator")))
	found, err = read(path, &saved)
	require.NoError(t, err)
	require.True(t, found)
	require.False(t, saved.Sent)
	require.Equal(t, now.Add(time.Second), saved.AttemptedAt)

	// A new daemon reads the same journal and a replay answers the original
	// message ID; the recipient receives exactly one entry.
	restarted := &loop{d: d, ctx: context.Background(), b: &bus.Bus{Store: store}, lanes: &laneSet{state: started}}
	restarted.sendFinishNotes(now.Add(2 * time.Second))
	require.Equal(t, 1, store.Len(bus.StreamOf("coordinator")))
	found, err = read(path, &saved)
	require.NoError(t, err)
	require.True(t, found)
	require.True(t, saved.Sent)
	require.NotEmpty(t, saved.ID)
	require.FileExists(t, path, "keep the receipt while Started is durable")
	require.NoError(t, WriteLanes(stateDir, LaneState{}))
	restarted.lanes.state.Started = nil
	restarted.sendFinishNotes(now.Add(3 * time.Second))
	require.NoFileExists(t, path)
	require.Equal(t, 1, store.Len(bus.StreamOf("coordinator")))
	require.NotEmpty(t, records)
}

func TestFinishNoteStopsAutomaticRetryAfterTokenWindow(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	dir, stateDir := t.TempDir(), t.TempDir()
	store := bustest.NewFake(now, "coordinator", "worker")
	store.Friends = []string{"coordinator", "worker"}
	d := &Daemon{Friend: "worker", Dir: dir, StateDir: stateDir, Record: func(string) {}}
	l := &loop{d: d, ctx: context.Background(), b: &bus.Bus{Store: store}}
	note := finishNotice{Job: "c4~15", Message: bus.Message{From: "worker", To: []string{"coordinator"}, Subject: "completed", Body: "completed", Token: finishNoticeToken("worker", "c4~15")}}
	path := finishNoticePath(d, note.Job)
	require.NoError(t, write(path, note))
	store.Lose = errors.New("committed response lost")
	l.sendFinishNotes(now)
	require.Equal(t, 1, store.Len(bus.StreamOf("coordinator")))
	store.Advance(8 * 24 * time.Hour) // the store's token record has expired.
	l.sendFinishNotes(now.Add(8 * 24 * time.Hour))
	require.Equal(t, 1, store.Len(bus.StreamOf("coordinator")), "an uncertain old send must not be duplicated")
	var saved finishNotice
	found, err := read(path, &saved)
	require.NoError(t, err)
	require.True(t, found)
	require.False(t, saved.Sent)
	require.FileExists(t, path)
}

func TestEndCardKeepsStartedWhenFinishNoteCannotBeJournaled(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	stateDir := filepath.Join(dir, "blocked-state")
	require.NoError(t, os.WriteFile(stateDir, []byte("not a directory"), 0o644))
	card := Card{ID: "c2", Outbox: filepath.Join(dir, "outbox", "c2~15")}
	require.NoError(t, os.MkdirAll(card.Outbox, 0o755))
	require.NoError(t, os.WriteFile(card.Report(), []byte("Verdict: LAND\n"), 0o644))
	s := LaneState{Started: map[string]Started{"c2~15": {Lane: 1, Card: card, At: now}}}
	records := []string{}
	d := &Daemon{Friend: "worker", Dir: dir, StateDir: stateDir, Coordinator: "coordinator", Record: func(s string) { records = append(records, s) }, Rules: func() LaneRules { return LaneRules{} }, SaveLanes: func(s LaneState) error { return WriteLanes(stateDir, s) }}
	d.m = Start(now)
	l := &loop{d: d, ctx: context.Background(), lanes: &laneSet{state: s}}
	words := l.endCard(1, card, LaneEnd{Wall: time.Minute}, now)
	require.Contains(t, words, "notice=owed")
	require.Contains(t, l.lanes.state.Started, "c2~15")
	require.True(t, l.lanes.recoveryPending)
	require.True(t, strings.Contains(strings.Join(records, "\n"), "finish note remains owed"))

	require.NoError(t, os.Remove(stateDir))
	require.NoError(t, WriteLanes(stateDir, s))
	words = l.endCard(1, card, LaneEnd{Wall: time.Minute}, now.Add(time.Second))
	require.NotContains(t, words, "notice=owed")
	require.NotContains(t, l.lanes.state.Started, "c2~15")
	require.FileExists(t, finishNoticePath(d, "c2~15"))
	got, err := ReadLanes(stateDir)
	require.NoError(t, err)
	require.NotContains(t, got.Started, "c2~15")
}

func TestStopRecoveryPreservesCompletedFinishNote(t *testing.T) {
	t.Parallel()
	if !ProcessIdentitySupported {
		t.Skip("run identity proof is needed for stopped recovery")
	}
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	dir, stateDir := t.TempDir(), t.TempDir()
	card := Card{ID: "c3", Outbox: filepath.Join(dir, "outbox", "c3~15")}
	require.NoError(t, os.MkdirAll(card.Outbox, 0o755))
	require.NoError(t, os.WriteFile(card.Report(), []byte("Verdict: LAND\n"), 0o644))
	s := LaneState{Started: map[string]Started{"c3~15": {Lane: 1, Card: card, At: now, RunID: "run-3"}}}
	require.NoError(t, WriteLanes(stateDir, s))
	d := &Daemon{Friend: "worker", Dir: dir, StateDir: stateDir, Coordinator: "coordinator", Record: func(string) {}, Rules: func() LaneRules { return LaneRules{} }, SaveLanes: func(s LaneState) error { return WriteLanes(stateDir, s) }}
	d.m = Start(now)
	l := &loop{d: d, ctx: context.Background(), lanes: &laneSet{state: s}}
	l.stopPersistedStarted(now.Add(time.Second))
	require.Empty(t, l.lanes.state.StopReturns)
	require.NotContains(t, l.lanes.state.Started, "c3~15")
	require.FileExists(t, finishNoticePath(d, "c3~15"))
}
