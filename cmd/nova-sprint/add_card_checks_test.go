package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/swarm"
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
		// the text as written, with the rules: no tier is added to a brief that names none
		require.NoError(t, os.WriteFile(path, []byte(text+"\n\n"+swarm.ChildRulesParagraph()), 0o600))
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

// add refuses a brief that names no tier, a free task with no PATHS: line as much as a
// card brief, and writes nothing, unless --tier is given: a card whose tier is unset is
// dealt to no one (a-card-without-a-tier-is-not-dealt.w1). With --tier the brief carries it
// on line 1 (its own line above a header line 1), a brief naming its own tier keeps it,
// and a tier that is none is refused.
func TestAddRefusesABriefWithoutATierUnlessTierIsGiven(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	write := func(name, text string) string {
		t.Helper()
		path := filepath.Join(t.TempDir(), name+".md")
		require.NoError(t, os.WriteFile(path, []byte(text+"\n\n"+swarm.ChildRulesParagraph()), 0o600))
		return path
	}
	audit := write("audit", "an audit of the dealer\n\nRead the dealer and report.")
	code, out, stderr := ta.do("add --stream s1 audit --one --brief-file " + audit)
	assert.Equal(t, 2, code, out+stderr)
	assert.Contains(t, stderr, "nova-sprint add REFUSED: the brief names no tier")
	assert.Contains(t, stderr, "--tier <")
	assert.False(t, ta.placed("audit"), "nothing written")

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a1.md"), []byte("a1: a build tier: pro\n\n"+swarm.ChildRulesParagraph()), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a2.md"), []byte("a2: a build\n\n"+swarm.ChildRulesParagraph()), 0o600))
	code, out, stderr = ta.do("add --stream s2 --brief-dir " + dir)
	assert.Equal(t, 2, code, out+stderr)
	assert.Contains(t, stderr, "a2.md names no tier", "the many-brief form names the file")
	assert.False(t, ta.placed("a1"), "all or none: nothing written")

	ta.ok("add --stream s1 audit --one --tier flash --brief-file " + audit)
	assert.Contains(t, ta.ok("card audit --brief"), "an audit of the dealer tier: flash", "--tier is written on line 1")
	ta.ok("add --stream s2 --tier heavy --brief-dir " + dir)
	assert.Contains(t, ta.ok("card a2 --brief"), "a2: a build tier: heavy")
	assert.Contains(t, ta.ok("card a1 --brief"), "a1: a build tier: pro", "a brief naming its own tier keeps it")

	repo := write("repo", "REPO: mas-bandwidth/nova-tools\n\nRead the dealer and report.")
	ta.ok("add --stream s3 r1 --one --tier pro --brief-file " + repo)
	brief := ta.ok("card r1 --brief")
	assert.Contains(t, brief, "tier: pro\nREPO: mas-bandwidth/nova-tools", "the tier is a line of its own above a header line 1")

	code, out, stderr = ta.do("add --stream s3 r3 --one --tier frontier2 --brief-file " + audit)
	assert.Equal(t, 2, code, out+stderr)
	assert.Contains(t, stderr, "--tier wants")
	code, _, stderr = ta.do("add --stream s3 --count 2 --tier flash")
	assert.Equal(t, 2, code)
	assert.Contains(t, stderr, "--tier is the tier of the brief this add admits")
}
