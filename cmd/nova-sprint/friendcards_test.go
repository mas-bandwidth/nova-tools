package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/friend"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// A reworked brief keeps the tier line the finish checks: its second line is THE ONE THING
// LEFT (friend.ReworkedBrief), so the tier line follows it and the finish must read the model
// wherever the line stands in the prelude, never only on the second line (docs/SPEC-FRIEND.md,
// a friend's models; the check reads the delivered brief's tier line).
func TestTheFinishReadsTheTierLineOfAReworkedBrief(t *testing.T) {
	t.Parallel()
	p := sprint.Packet{Card: "s1-1.w2", Epoch: 0, Attempt: 2, Gen: 1, Branch: "sprint/s1-1.w2.g1.e0", Tier: "pro", Model: "prov/m",
		Brief: "s1-1: a friend's card\nREPO: mas-bandwidth/nova-tools\nBASE: sprint/s1\nWHO: friend amy\n\nThe task.", Fix: "assert the bound"}
	text := friendBrief("amy", p)
	lines := strings.Split(text, "\n")
	require.Equal(t, friend.OneThingLeft+"assert the bound", lines[1], "a reworked brief opens with the fix")
	path := filepath.Join(t.TempDir(), "BRIEF.md")
	require.NoError(t, os.WriteFile(path, []byte(text), 0o644))
	model, ok := friendBriefModel(path)
	require.True(t, ok)
	assert.Equal(t, "prov/m", model, "the tier line after the fix is read")
}
