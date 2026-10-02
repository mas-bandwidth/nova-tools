package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A verb run before init on a twin says there is no sprint here yet and names init, never
// the table layer's words (docs/SPEC-SPRINT.md section 11): the first thing a cold
// coordinator types is answered with the fix.
func TestAVerbBeforeInitSaysRunInitFirst(t *testing.T) {
	t.Parallel()
	file := filepath.Join(t.TempDir(), "sprint.twin")
	for _, line := range []string{"tick", "where", "inbox", "queue --as m1", "take --as m1 --epoch 0", "card s1-1", "check"} {
		code, out, errs := twinProcess(t, file, line)
		assert.NotEqual(t, 0, code, "%s before init: exit %d", line, code)
		assert.Contains(t, errs, "no sprint here yet", "%s before init: %s%s", line, out, errs)
		assert.Contains(t, errs, "run: nova-sprint init", "%s before init: %s%s", line, out, errs)
		assert.NotContains(t, out+errs, "NOTABLE", "%s before init: %s%s", line, out, errs)
	}
}

// land --dry-run on a no-git twin, the card admitted with --count and finished with no
// --head: each cause on its own line with its one next command, and the line that says
// merge stands in for land on a twin (docs/SPEC-SPRINT.md section 7).
func TestALandRefusalNamesEachCauseOnItsLineAndTheTwinsMerge(t *testing.T) {
	t.Parallel()
	file := filepath.Join(t.TempDir(), "sprint.twin")
	for _, line := range []string{"init --readers reader-a,reader-b --members m1", "add --stream s1 --count 1", "start", "tick", "tick",
		"take --as m1 --epoch 0", "finish --as m1 s1-1.w1@1 --epoch 0 --report done", "tick",
		"read --as reader-a --ok s1-1.r1.reader-a --epoch 0", "read --as reader-b --ok s1-1.r1.reader-b --epoch 0", "tick"} {
		code, out, errs := twinProcess(t, file, line)
		require.Equal(t, 0, code, "%s: %s%s", line, out, errs)
	}
	code, out, errs := twinProcess(t, file, "land --stream s1 --dry-run")
	assert.Equal(t, 1, code, "%s%s", out, errs)
	var refused, notes []string
	for _, l := range strings.Split(errs, "\n") {
		switch {
		case strings.HasPrefix(l, "LAND REFUSED"):
			refused = append(refused, l)
		case strings.HasPrefix(l, "NOTE "):
			notes = append(notes, l)
		}
	}
	require.Len(t, refused, 1, errs)
	assert.Contains(t, refused[0], "names no BASE: line", errs)
	assert.NotContains(t, refused[0], "is not a commit id", "one cause on the refusal line: %s", refused[0])
	assert.Equal(t, 1, strings.Count(refused[0], "run: "), "one next command: %s", refused[0])
	joined := strings.Join(notes, "\n")
	assert.Contains(t, joined, "NOTE the head s1-1.w1 of s1-1 is not a commit id", errs)
	assert.Contains(t, joined, "merge --stream s1 --batch 1", "the twin's stand-in for land: %s", errs)
	for _, n := range notes {
		assert.LessOrEqual(t, strings.Count(n, "run: "), 1, "one next command a line: %s", n)
	}
}

// The log's default timeline prints a cost record as one short line, never the record's
// blob; log --card <id> and log --json keep the blob (docs/SPEC-SPRINT.md section 17).
func TestTheLogPrintsACostRecordAsOneShortLine(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.inReview(1)
	ta.ok("ask")
	ta.ok("read --as reader-a --ok s1-1.r1.reader-a --usage 'wall=6s budget=100/400000 input=5 output=7'")
	out := ta.ok("log")
	assert.NotContains(t, out, "cost_record:", out)
	assert.NotContains(t, out, "cost_total=", out)
	assert.Contains(t, out, "s1-1 cost: read s1-1.r1.reader-a by reader-a, ok", out)
	for _, l := range strings.Split(out, "\n") {
		assert.LessOrEqual(t, len(l), 200, "a long line: %s", l)
	}
	assert.Contains(t, ta.ok("log --card s1-1"), "cost_record:")
	assert.Contains(t, ta.ok("log --json"), "cost_record:")
}
