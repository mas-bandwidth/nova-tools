package main

import (
	"encoding/json"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUnpinVerb(t *testing.T) {
	t.Parallel()
	ta, _ := friendCardApp(t, "only friend amy", "amy")
	before := ta.primary("s1-1").F("brief")
	ta.ok("unpin s1-1 --reason shared --dry-run")
	assert.Equal(t, "only.friend.amy", ta.primary("s1-1").F("who"))
	ta.ok("unpin s1-1 --reason shared")

	assert.Equal(t, before, ta.primary("s1-1").F("brief"))
	assert.Contains(t, ta.ok("log --card s1-1"), "shared")
	assert.Contains(t, ta.ok("card s1-1"), "WHO: only friend amy")
	code, _, errs := ta.do("unpin s1-1")
	assert.NotZero(t, code)
	assert.Contains(t, errs, "--reason")
	assert.Contains(t, ta.ok("unpin --stream s1 --reason again"), "already unpinned")
	code, out, errs := ta.do("unpin s1-1 missing --reason again")
	assert.Equal(t, 1, code)
	assert.Contains(t, out+errs, "already unpinned")
	assert.Contains(t, errs, "missing")
	ta.ok("tick")
	assert.Empty(t, ta.primary("s1-1").F("who"))
	assert.Equal(t, before, ta.primary("s1-1").F("brief"))

}

func TestUnpinKeepsTheInputBriefBytes(t *testing.T) {
	t.Parallel()
	ta, _ := friendApp(t, "amy")
	ta.ok("friend sync --root " + t.TempDir())
	dir := t.TempDir()
	path := filepath.Join(dir, "job.md")
	before := []byte(passingBrief("job: work\nWHO: only friend amy"))
	require.NoError(t, os.WriteFile(path, before, 0o600))
	ta.ok("add --stream s1 --brief-dir " + dir)
	ta.ok("start")
	var preview map[string]any
	ta.json("unpin job --reason shared --dry-run", &preview)
	assert.Equal(t, true, preview["dry_run"])
	assert.Equal(t, "only.friend.amy", ta.primary("job").F("who"))
	ta.ok("unpin job --reason shared")
	after, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, before, after)
	ta.ok("tick")
	assert.Empty(t, ta.primary("job").F("who"))
}

func TestUnpinPreviewReportsItsActualOutcome(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, ids, status string
		code, changes     int
	}{
		{"success", "s1-1", "ok", 0, 1},
		{"wholly refused", "missing", "refused", 1, 0},
		{"mixed", "s1-1 missing", "refused", 1, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ta, _ := friendCardApp(t, "only friend amy", "amy")
			args := "unpin " + tc.ids + " --reason share --dry-run"
			code, out, _ := ta.do(args + " --json")
			require.Equal(t, tc.code, code)
			var got map[string]any
			require.NoError(t, json.Unmarshal([]byte(out), &got))
			assert.Equal(t, float64(code), got["exit"])
			assert.Equal(t, tc.status, got["status"])
			assert.Equal(t, float64(tc.changes), got["changes"])
			code, out, _ = ta.do(args)
			assert.Equal(t, tc.code, code)
			assert.Contains(t, out, "UNPIN "+strings.ToUpper(tc.status)+" DRY-RUN")
			assert.Equal(t, "only.friend.amy", ta.primary("s1-1").F("who"))
			assert.NotContains(t, ta.ok("log --card s1-1"), "WHO unpinned")
		})
	}
}
