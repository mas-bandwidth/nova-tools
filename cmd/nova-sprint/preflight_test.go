package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/swarm"
)

// preflightText is a brief that passes the card lint and carries the typed header
// lines the preflight reads: an optional DEPENDS-ON, PATHS and TEST under line 1,
// then the RULES paragraph every brief carries.
func preflightText(lead, paths, needs, test string) string {
	var b strings.Builder
	b.WriteString(lead + "\n")
	if needs != "" {
		b.WriteString("DEPENDS-ON: " + needs + "\n")
	}
	if paths != "" {
		b.WriteString("PATHS: " + paths + "\n")
	}
	if test != "" {
		b.WriteString("TEST: " + test + "\n")
	}
	b.WriteString("\n" + swarm.ChildRulesParagraph())
	return b.String()
}

// writePreflightBrief writes preflightText into dir under name.md and returns the path.
func writePreflightBrief(t *testing.T, dir, name, lead, paths, needs, test string) string {
	t.Helper()
	path := filepath.Join(dir, name+".md")
	require.NoError(t, os.WriteFile(path, []byte(preflightText(lead, paths, needs, test)), 0o600))
	return path
}

// Preflight checks a batch of briefs together, read-only: the card lint, every
// DEPENDS-ON present on the table or in the directory and none dropped, PATHS that
// overlap a ready, working or review card's PATHS or another brief's, and the TEST
// and BASE. A working card and two briefs whose paths overlap, a brief naming a
// dropped need and a clean brief: the three are reported with their reason and the
// clean one passes, exit 1.
func TestPreflightReportsCollisionsAndDroppedNeeds(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	const test = "./cmd/nova-sprint TestTheCommandDrivesAStreamToLanded"
	// one working card already on the table, its brief naming a path
	live := writePreflightBrief(t, t.TempDir(), "live", "the live card (s1) tier: flash", "src/live.go, cmd/nova-sprint/*_test.go", "", test)
	ta.ok("add --one --stream s1 live --brief-file " + live)
	// a need that is dropped before the batch is checked
	ta.ok("add --one --stream s1 gone")
	ta.ok("drop gone --reason 'obsolete'")
	ta.ok("start")
	ta.deal(1)
	ta.ok("take --as m1 --limit 1")

	dir := t.TempDir()
	// a overlaps the working card and b; b overlaps a; c names the dropped card;
	// d is clean
	writePreflightBrief(t, dir, "a", "the first (s1)", "src/live.go, docs/*.md", "", test)
	writePreflightBrief(t, dir, "b", "the second (s1)", "docs/SPEC-SPRINT.md", "", test)
	writePreflightBrief(t, dir, "c", "the third (s1)", "", "gone", test)
	writePreflightBrief(t, dir, "d", "the clean one (s1)", "src/clean.go", "", test)

	before := ta.applies()
	code, out, errs := ta.do("preflight --brief-dir " + dir)
	require.Equal(t, 1, code, "a batch with defects: exit %d\n%s%s", code, out, errs)
	require.Contains(t, out, "PREFLIGHT a FAIL", "a overlaps: %s", out)
	require.Contains(t, out, "src/live.go", "a names the colliding file: %s", out)
	require.Contains(t, out, "live", "a names the live card: %s", out)
	require.Contains(t, out, "PREFLIGHT b FAIL", "b overlaps a: %s", out)
	require.Contains(t, out, "docs/SPEC-SPRINT.md", "b names its file: %s", out)
	require.Contains(t, out, "gone", "c names the dropped need: %s", out)
	require.Contains(t, out, "dropped", "c names the need dropped: %s", out)
	require.NotContains(t, out, "PREFLIGHT d FAIL", "the clean brief passes: %s", out)
	require.Contains(t, out, "PREFLIGHT FAIL briefs=4 failed=3", "the summary: %s", out)
	require.Equal(t, before, ta.applies(), "preflight is read-only")
}
