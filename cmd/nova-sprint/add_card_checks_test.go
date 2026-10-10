package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// add holds every brief to the card checks nova-card generate holds it to before it
// leaves (card.Checks): a card brief with no tier on line 1, a TEST outside its
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
	// the finish form every card brief carries (docs/SPEC-CARDS.md, finish-form-present);
	// the failing briefs below omit it on purpose, so each refusal also carries that finding.
	finishForm := "\nTHE FINISH FORM\nVerdict: LAND|HOLD|FAIL\nHead: <40-hex>\n"
	for _, tc := range []struct {
		name, brief string
		checks      []string
	}{
		{"no-tier", "RESULT: c sha=0123456789ab\nPATHS: internal/x/*.go\nTEST: internal/x TestY\n\nTHE TASK. Fix x.", []string{"check=tier-line line=1", "check=finish-form-present"}},
		{"test-out", "RESULT: c sha=0123456789ab tier: pro\nPATHS: internal/x/*.go\nTEST: internal/other TestY\n\nTHE TASK. Fix x.", []string{"check=test-outside-paths line=3", "check=finish-form-present"}},
		{"named", header + "\nTHE TASK. Fix x as ada asked.", []string{"check=personal-name line=5", "check=no-names-outside-quotes"}},
		{"dropped", header + "\nTHE TASK. Finish what s0-1 began.", []string{"check=dropped-card line=5", "check=finish-form-present"}},
	} {
		code, out, stderr := ta.do("add --stream s1 --one --actor lead --brief-file " + write(tc.name, tc.brief))
		assert.Equal(t, 2, code, "%s: %s%s", tc.name, out, stderr)
		for _, check := range tc.checks {
			assert.Contains(t, stderr, "LINT DRIFT card="+tc.name+" "+check, tc.name)
		}
		assert.False(t, ta.placed(tc.name), "%s: nothing written", tc.name)
	}
	out := ta.ok("add --stream s1 --one --actor lead --brief-file " + write("clean", header+finishForm+"\nTHE TASK. The owner said \"ada wants x fixed\"; fix x."))
	assert.Contains(t, out, "MOVED clean -> ready")
}
