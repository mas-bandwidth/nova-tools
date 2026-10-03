package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// writeHeaderBrief writes a passing brief whose header block carries DEPENDS-ON: and
// PATHS: lines, as a card the coordinator writes does, and returns its path.
func writeHeaderBrief(t *testing.T, dir, id, depends, paths string) string {
	t.Helper()
	lead := "RESULT: " + id + " sha=000000000000\nKIND: fix\nDEPENDS-ON: " + depends + "\nPATHS: " + paths + "\n\nFix " + id + "."
	path := filepath.Join(dir, id+".md")
	require.NoError(t, os.WriteFile(path, []byte(passingBrief(lead)), 0o600))
	return path
}

// add reads a brief's DEPENDS-ON: header line as its needs when --needs is not given
// (nova-tools#5096 item 17, the wave-2 card builder: "add neither reads DEPENDS-ON from
// the brief nor sees two open cards naming the same file; the chains were computed
// outside"): `-` and an owner/repo#n reference are no need, --needs given wins, and the
// many-brief form reads it for each card.
func TestAddReadsDependsOnFromTheBrief(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	dir := t.TempDir()
	c0 := writeHeaderBrief(t, dir, "c0", "mas-bandwidth/nova-tools#5096", "internal/c0.go")
	c1 := writeHeaderBrief(t, dir, "c1", "c0", "internal/c1.go")
	assert.Contains(t, ta.ok("add --stream s c0 --brief-file "+c0), "MOVED c0 -> ready")
	assert.Contains(t, ta.ok("add --stream s c1 --brief-file "+c1), "MOVED c1 -> waiting")
	assert.Equal(t, "c0", ta.primary("c1").F("needs"), "DEPENDS-ON: c0 is c1's need")
	ta.ok("add --stream s c2 --brief-file " + c1 + " --needs c1")
	assert.Equal(t, "c1", ta.primary("c2").F("needs"), "--needs given wins over the brief's line")
	many := t.TempDir()
	writeHeaderBrief(t, many, "p1", "-", "internal/p1.go")
	writeHeaderBrief(t, many, "p2", "p1", "internal/p2.go")
	ta.ok("add --stream t --brief-dir " + many)
	assert.Equal(t, "p1", ta.primary("p2").F("needs"), "the many-brief form reads DEPENDS-ON: per card")
	assert.Equal(t, string(sprint.Ready), ta.primary("p1").Col)
}

// Two cards of one add that name the same file in PATHS and neither of which needs the
// other would edit it at once: add refuses them, exit 2, nothing written, naming the file
// and the cards, unless --allow-shared-paths; cards chained by their needs share a file.
func TestAddRefusesTwoCardsNamingOneFileInPaths(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	dir := t.TempDir()
	writeHeaderBrief(t, dir, "q1", "-", "internal/x.go, internal/q1.go")
	writeHeaderBrief(t, dir, "q2", "-", "internal/q2.go internal/x.go")
	code, out, errs := ta.do("add --stream s --brief-dir " + dir)
	require.Equal(t, 2, code, "shared PATHS: %s%s", out, errs)
	assert.Contains(t, errs, "internal/x.go is named in PATHS by q1 and q2, and neither needs the other")
	assert.Contains(t, errs, "--allow-shared-paths")
	assert.NotContains(t, out, "MOVED", "nothing written")
	assert.Contains(t, ta.ok("add --stream s --brief-dir "+dir+" --allow-shared-paths"), "ADD OK stream=s cards=2 before=- moved=2")
	chained := t.TempDir()
	writeHeaderBrief(t, chained, "r1", "-", "internal/y.go")
	writeHeaderBrief(t, chained, "r2", "r1", "internal/y.go")
	assert.Contains(t, ta.ok("add --stream u --brief-dir "+chained), "ADD OK stream=u cards=2 before=- moved=2", "a chain shares its file in turn")
}
