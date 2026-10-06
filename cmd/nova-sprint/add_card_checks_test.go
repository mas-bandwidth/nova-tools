package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// add holds every brief to the card checks nova-card generate holds it to before it
// leaves (cardgen.CardChecks): a card brief with no tier on line 1, a TEST outside its
// PATHS, the name of the sprint's coordinator or owner outside double-quoted words, or a
// card dropped off the table named, is refused with the finding and nothing is written;
// the clean brief, and the owner quoted by name, are admitted.
func TestAddHoldsABriefToTheCardChecks(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1 --coordinator lead --owner ada")
	ta.ok("add --stream s0 --count 2 --actor lead")
	ta.ok("drop s0-1 --reason obsolete --actor lead")
	header := "RESULT: c sha=0123456789ab tier: pro\nPATHS: internal/x/*.go, internal/x/*_test.go\nTEST: internal/x TestY\n"
	write := func(name, text string) string {
		t.Helper()
		dir := t.TempDir()
		path := filepath.Join(dir, name+".md")
		require.NoError(t, os.WriteFile(path, []byte(passingBrief(text)), 0o600))
		return path
	}
	for _, tc := range []struct{ name, brief, check string }{
		{"no-tier", "RESULT: c sha=0123456789ab\nPATHS: internal/x/*.go\nTEST: internal/x TestY\n\nTHE TASK. Fix x.", "check=tier-line line=1"},
		{"test-out", "RESULT: c sha=0123456789ab tier: pro\nPATHS: internal/x/*.go\nTEST: internal/other TestY\n\nTHE TASK. Fix x.", "check=test-outside-paths line=3"},
		{"named", header + "\nTHE TASK. Fix x as ada asked.", "check=personal-name line=5"},
		{"dropped", header + "\nTHE TASK. Finish what s0-1 began.", "check=dropped-card line=5"},
	} {
		code, out, stderr := ta.do("add --stream s1 --one --actor lead --brief-file " + write(tc.name, tc.brief))
		assert.Equal(t, 2, code, "%s: %s%s", tc.name, out, stderr)
		assert.Contains(t, stderr, "LINT DRIFT card="+tc.name+" "+tc.check, tc.name)
		assert.Contains(t, stderr, "nova-sprint add REFUSED: 1 brief finding(s)", tc.name)
		assert.False(t, ta.placed(tc.name), "%s: nothing written", tc.name)
	}
	out := ta.ok("add --stream s1 --one --actor lead --brief-file " + write("clean", header+"\nTHE TASK. The owner said \"ada wants x fixed\"; fix x."))
	assert.Contains(t, out, "MOVED clean -> ready")
}
