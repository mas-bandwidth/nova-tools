package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// The streams command on the twin (docs/SPEC-SPRINT.md section 11, streams),
// called as cmdStreams: the verb table does not name streams (verbs.go).
func TestCmdStreamsListsReposAndTheMix(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	st, err := ta.a.store(common{redis: "mem:0", actor: "coordinator", verb: "init"})
	require.NoError(t, err)
	ctx := context.Background()
	require.NoError(t, st.Init(ctx))
	require.NoError(t, ta.m.SetCoordinator(ctx, "coordinator"))
	_, err = st.Run(ctx, store.AddStep(sprint.AddReq{Stream: "alpha", Cards: []sprint.CardAdd{
		{ID: "alpha-1", Brief: "tier: flash\nREPO: mas-bandwidth/nova\nBASE: sprint/nova\n\nTHE TASK. List nova.\n"},
	}}))
	require.NoError(t, err)
	_, err = st.Run(ctx, store.AddStep(sprint.AddReq{Stream: "both", Cards: []sprint.CardAdd{
		{ID: "both-1", Brief: "tier: flash\nREPO: mas-bandwidth/nova\nBASE: sprint/nova\n\nTHE TASK. Share nova.\n"},
		{ID: "both-2", Brief: "tier: flash\nREPO: mas-bandwidth/nova-tools\nBASE: sprint/tools\n\nTHE TASK. Share tools.\n"},
	}}))
	require.NoError(t, err)
	_, err = st.Run(ctx, store.SetStep(sprint.SetReq{Streams: []string{"alpha"}, Release: "v11", Who: "coordinator"}))
	require.NoError(t, err)

	var out, errb bytes.Buffer
	code := ta.a.cmdStreams([]string{"--cards"}, &out, &errb)
	require.Equal(t, 0, code, errb.String())
	text := out.String()
	assert.Contains(t, text, "STREAMS alpha")
	assert.Contains(t, text, "repos=mas-bandwidth/nova")
	assert.Contains(t, text, "FINDING")
	assert.Contains(t, text, "CARD both-1")
	assert.Contains(t, text, "title=Share nova.")
	assert.Contains(t, text, "STREAMS OK streams=2")

	out.Reset()
	errb.Reset()
	code = ta.a.cmdStreams([]string{"--json", "--repo", "mas-bandwidth/nova", "--release", "v11"}, &out, &errb)
	require.Equal(t, 0, code, errb.String())
	var got struct {
		Streams []sprint.StreamView `json:"streams"`
	}
	require.NoError(t, json.Unmarshal(out.Bytes(), &got))
	require.Len(t, got.Streams, 1)
	assert.Equal(t, "alpha", got.Streams[0].Stream)
	assert.Equal(t, "v11", got.Streams[0].Release)

	out.Reset()
	errb.Reset()
	code = ta.a.cmdDropByRepo([]string{"--repo", "mas-bandwidth/nova", "--reason", "left"}, &out, &errb)
	assert.Equal(t, 2, code, errb.String())
	assert.Contains(t, errb.String(), "--expect")
	require.NotNil(t, mustPlaced(t, st, "alpha-1"))

	out.Reset()
	errb.Reset()
	code = ta.a.cmdDropByRepo([]string{"--repo", "mas-bandwidth/nova", "--expect", "1", "--reason", "left"}, &out, &errb)
	assert.Equal(t, 1, code, out.String()+errb.String())
	assert.Contains(t, errb.String(), "2")
	require.NotNil(t, mustPlaced(t, st, "alpha-1"))

	out.Reset()
	errb.Reset()
	code = ta.a.cmdHoldByRepo([]string{"--repo", "mas-bandwidth/nova-tools", "--expect", "1", "--reason", "pause"}, &out, &errb)
	require.Equal(t, 0, code, out.String()+errb.String())
	assert.True(t, sprint.StreamHeld(mustSnap(t, st), "both"))
	assert.False(t, sprint.StreamHeld(mustSnap(t, st), "alpha"))
}

func mustSnap(t *testing.T, st *store.Store) *sprint.Snapshot {
	t.Helper()
	s, err := st.Load(context.Background(), []string{sprint.Work, sprint.Merge}, nil)
	require.NoError(t, err)
	return s
}

func mustPlaced(t *testing.T, st *store.Store, id string) *sprint.Card {
	t.Helper()
	return mustSnap(t, st).Work.Placed(id)
}

func TestCmdStreamsRefusesAWord(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	var out, errb bytes.Buffer
	code := ta.a.cmdStreams([]string{"extra"}, &out, &errb)
	assert.Equal(t, 2, code)
	assert.True(t, strings.Contains(errb.String(), "REFUSED"), errb.String())
}
