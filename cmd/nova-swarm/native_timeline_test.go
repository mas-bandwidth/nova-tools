package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// TestNativeRunWritesTimeline: a native run timestamps the harness's own per-turn and
// per-tool report lines into <job>/timeline.tsv -- one row per model turn and per tool
// call, in the order the harness reported them, in the six columns swarm.TimelineColumns
// names. A tool row carries no tokens (the harness gave none there), a turn row does.
func TestNativeRunWritesTimeline(t *testing.T) {
	t.Parallel()

	bin := nativeHarness(t)
	root, slot := aSlot(t)
	label := "timeline"

	var errOut bytes.Buffer
	_, code := nativeRun(nativeRunConfig{
		binary: bin, model: "fake/fake-model", label: label,
		card: []byte("FAKE-TIMELINE\n"), slotDir: slot, root: root,
		deadline: 30 * time.Second, noWall: true,
	}, &errOut)
	require.Equal(t, 0, code, "native run exits 0, got %d:\n%s", code, errOut.String())

	path := filepath.Join(slot, "jobs", label, swarm.TimelineFileName)
	raw, err := os.ReadFile(path)
	require.NoError(t, err, "the native run wrote no timeline at %s", path)
	head := strings.SplitN(string(raw), "\n", 2)[0]
	require.Equal(t, strings.Join(swarm.TimelineColumns, "\t"), head, "timeline header = %q, want %q", head, strings.Join(swarm.TimelineColumns, "\t"))

	rows := timelineRows(t, raw)
	require.Len(t, rows, 6, "%d timeline rows, want 6 (one turn, five tool calls):\n%s", len(rows), raw)
	assert.Equal(t, "model", rows[0].Tool, "turn row = %+v, want tool=model in=1200 out=340", rows[0])
	assert.Equal(t, "1200", rows[0].InputTokens, "turn row = %+v, want tool=model in=1200 out=340", rows[0])
	assert.Equal(t, "340", rows[0].OutputTokens, "turn row = %+v, want tool=model in=1200 out=340", rows[0])
	// The tool calls, in report order, each with no tokens of its own.
	wants := []string{"git clone", "cat ", "go test", "go test", "result.md"}
	for i, want := range wants {
		r := rows[i+1]
		assert.Contains(t, strings.ToLower(r.Tool), want, "tool row %d = %q, want it to name %q", i, r.Tool, want)
		assert.Equal(t, "", r.InputTokens, "tool row %d carries tokens %q/%q, want both empty (the harness gave none)", i, r.InputTokens, r.OutputTokens)
		assert.Equal(t, "", r.OutputTokens, "tool row %d carries tokens %q/%q, want both empty (the harness gave none)", i, r.InputTokens, r.OutputTokens)
	}
	// Every span is a real, non-negative duration.
	for i, r := range rows {
		assert.False(t, r.End.Before(r.Start), "row %d ends before it starts: %s < %s", i, r.End, r.Start)
	}
}

// timelineRows reads back the rows of a timeline.tsv whose header the test has already
// held to swarm.TimelineColumns, so each cell is read by its column's position.
func timelineRows(t *testing.T, raw []byte) []swarm.TimelineRow {
	t.Helper()
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	var rows []swarm.TimelineRow
	for _, line := range lines[1:] {
		cells := strings.Split(line, "\t")
		require.Len(t, cells, len(swarm.TimelineColumns), "timeline row %q", line)
		start, err := time.Parse(time.RFC3339Nano, cells[0])
		require.NoError(t, err, "t_start of %q", line)
		end, err := time.Parse(time.RFC3339Nano, cells[1])
		require.NoError(t, err, "t_end of %q", line)
		rows = append(rows, swarm.TimelineRow{Start: start, End: end, Tool: cells[2], InputTokens: cells[4], OutputTokens: cells[5]})
	}
	return rows
}
