package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// A red card in the middle of one stream is bisected out on the worker.
// land/<stream> gets the green prefix, the store gets one verdict a card,
// and the base and the lander's land root are left as they were.
func TestLandNodeLandsOneStreamOffTheLanderMachine(t *testing.T) {
	t.Parallel()
	r := newLandRig(t)
	land := filepath.Join(r.dir, "land")
	require.NoError(t, os.MkdirAll(land, 0o755))
	sentinel := filepath.Join(land, "LANDER")
	require.NoError(t, os.WriteFile(sentinel, []byte("untouched\n"), 0o600))
	r.ok("add --stream s1 --count 3")
	heads := map[string]string{
		"s1-1": r.head("s1-1", "main", "s1-1.txt", "one\n"),
		"s1-2": r.head("s1-2", "main", "RED", "red\n"),
		"s1-3": r.head("s1-3", "main", "s1-3.txt", "three\n"),
	}
	r.queued(heads, "s1-1", "s1-2", "s1-3")
	out := r.ok("land-node --as m1 --stream s1 --repo-dir " + r.clone + " --base main --check 'test ! -f RED'")
	tip := r.git(r.remote, "rev-parse", "refs/heads/land/s1")
	assert.Contains(t, out, "LAND-NODE stream=s1 ref=land/s1 head="+tip+" by=m1")
	assert.Contains(t, out, "LAND-NODE card=s1-1 verdict=landed-in-node")
	assert.Contains(t, out, "LAND-NODE card=s1-2 verdict=red why=")
	assert.Contains(t, out, "test ! -f RED")
	assert.Contains(t, out, "LAND-NODE card=s1-3 verdict=ready")
	assert.NotContains(t, out, "verdict=landed-in-node\nLAND-NODE card=s1-2 verdict=landed-in-node")
	files := r.git(r.remote, "ls-tree", "-r", "--name-only", "refs/heads/land/s1")
	assert.Contains(t, files, "s1-1.txt")
	assert.NotContains(t, files, "RED")
	assert.NotContains(t, files, "s1-3.txt")
	assert.Equal(t, []string{"base"}, r.mainLog())
	assert.Equal(t, map[string]string{"s1-1": "merging/queued", "s1-2": "merging/queued", "s1-3": "merging/queued"}, r.places("s1-1", "s1-2", "s1-3"))
	assert.Equal(t, "merging", r.streamState("s1"))
	st, err := r.a.store(common{redis: "mem:0", actor: "tester"})
	require.NoError(t, err)
	s, err := st.Load(context.Background(), []string{sprint.Merge}, nil)
	require.NoError(t, err)
	assert.Equal(t, "landed-in-node", s.Merge.Placed("s1-1").F("node_verdict"))
	assert.Empty(t, s.Merge.Placed("s1-1").F("node_why"))
	assert.Equal(t, "red", s.Merge.Placed("s1-2").F("node_verdict"))
	assert.Contains(t, s.Merge.Placed("s1-2").F("node_why"), "test ! -f RED")
	assert.Equal(t, "ready", s.Merge.Placed("s1-3").F("node_verdict"))
	ctl := s.StreamCtl("s1")
	assert.Equal(t, tip, ctl.F("node_head"))
	assert.Equal(t, "land/s1", ctl.F("node_ref"))
	open, err := st.B.OpenNotes(context.Background())
	require.NoError(t, err)
	var kinds []string
	for _, o := range open {
		if o.Note.Card == "s1-2" {
			kinds = append(kinds, o.Note.Kind+"/"+o.Note.Type)
		}
	}
	assert.Equal(t, []string{"judgment/card-red"}, kinds)
	kept, err := os.ReadFile(sentinel)
	require.NoError(t, err)
	assert.Equal(t, "untouched\n", string(kept))
	ents, err := os.ReadDir(land)
	require.NoError(t, err)
	require.Len(t, ents, 1)
	assert.Equal(t, "LANDER", ents[0].Name())
	r.clean()
}
