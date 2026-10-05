package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRecutFromTheCommandLine tests recut <card> --tier <t> or --brief-file <f>
// (docs/SPEC-SPRINT.md section 2, "A card replaced by its twin"; item 21).
func TestRecutFromTheCommandLine(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --members m1,m2 --readers reader-a,reader-b,reader-c")

	dir := t.TempDir()
	brief1 := filepath.Join(dir, "task-1.md")
	require.NoError(t, os.WriteFile(brief1, []byte(passingBrief("task 1 tier: flash")), 0o600))
	ta.ok("add --stream s1 task-1 --one --brief-file " + brief1)
	ta.ok("add --stream s1 dep1 --one --needs task-1")

	// 1. Recut with --tier pro
	out := ta.ok("recut task-1 --tier pro")
	assert.Contains(t, out, "dep1 needs task-1 -> task-1-pro")
	assert.Contains(t, ta.ok("card dep1"), "needs task-1-pro (ready)")
	assert.Contains(t, ta.ok("card task-1"), "replaced by task-1-pro")
	assert.Contains(t, ta.ok("card --fields task-1-pro"), "replaces")
	assert.Contains(t, ta.ok("card task-1-pro"), "tier: pro")
	assert.NotRegexp(t, `(?m)^JUDGMENT .*blocked on something dropped`, ta.ok("inbox"))

	// 2. Recut with --brief-file
	brief2 := filepath.Join(dir, "task-1-v2.md")
	require.NoError(t, os.WriteFile(brief2, []byte(passingBrief("task 1 updated scope tier: pro")), 0o600))
	ta.ok("add --stream s1 dep2 --one --needs task-1-pro")
	out = ta.ok("recut task-1-pro --brief-file " + brief2)
	assert.Contains(t, out, "dep1 needs task-1-pro -> task-1-v2")
	assert.Contains(t, out, "dep2 needs task-1-pro -> task-1-v2")
	assert.Contains(t, ta.ok("card dep1"), "needs task-1-v2 (ready)")
	assert.Contains(t, ta.ok("card dep2"), "needs task-1-v2 (ready)")
	assert.Contains(t, ta.ok("card --fields task-1-v2"), "replaces")
	assert.Contains(t, ta.ok("card task-1-v2"), "task 1 updated scope")

	// 3. Recut with --tier and explicit --id
	out = ta.ok("recut task-1-v2 --tier flash --id task-1-custom")
	assert.Contains(t, out, "dep1 needs task-1-v2 -> task-1-custom")
	assert.Contains(t, ta.ok("card --fields task-1-custom"), "replaces")
	assert.Contains(t, ta.ok("card task-1-custom"), "tier: flash")

	// 4. Refusals
	code, _, errs := ta.do("recut task-1-custom")
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "recut wants --tier <t> or --brief-file <f>")

	code, _, errs = ta.do("recut task-1-custom --tier invalidtier")
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "--tier wants")

	code, _, errs = ta.do("recut nosuchcard --tier pro")
	assert.Equal(t, 1, code)
	assert.Contains(t, errs, "no card nosuchcard")
}
