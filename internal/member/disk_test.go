//go:build !windows

package member

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The member's beat carries the free space and inode headroom of the volume
// its working directory lives on (--disk, one sprint.DiskReading as JSON),
// measured here because the sprint server cannot read this machine's files;
// the load and queue words are unchanged beside it.
func TestTheBeatCarriesTheVolumesReading(t *testing.T) {
	t.Parallel()
	g := newRig(Config{As: "m", Width: 1})
	g.s.set("queue", 0, queueJSON(t, 7))
	require.NoError(t, g.m.Beat())
	lines := g.s.lines("beat")
	require.Len(t, lines, 1, "one beat line: %v", lines)
	words := strings.Fields(lines[0])
	i := slices.Index(words, "--disk")
	require.GreaterOrEqual(t, i, 0, "the beat carries the volume's reading: %v", words)
	require.Less(t, i+1, len(words), "the reading follows the flag: %v", words)
	var d struct {
		Volume      string `json:"volume"`
		Free        uint64 `json:"free"`
		Total       uint64 `json:"total"`
		InodesFree  uint64 `json:"inodes_free"`
		InodesTotal uint64 `json:"inodes_total"`
	}
	require.NoError(t, json.Unmarshal([]byte(words[i+1]), &d), "the flag is one JSON reading: %v", words[i+1])
	assert.NotEmpty(t, d.Volume, "the reading names the volume")
	assert.Greater(t, d.Total, uint64(0), "the reading names the volume's size")
	assert.LessOrEqual(t, d.Free, d.Total, "the free space is within the volume")
	assert.LessOrEqual(t, d.InodesFree, d.InodesTotal, "the free inodes are within the volume")
}
