package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// THE HELP PROMISES TWO DIFFERENT EXITS AND THE LINT KEEPS BOTH. Exit 1 is "the verb ran and
// said NO -- a lint that found a defect"; exit 2 is "could not run". Every lint finding used
// to exit 2, so a caller could not tell a bad card from a broken tool (a cold rating of the
// tools, 2026-09-30).

// A card with findings is the verb saying NO: exit 1, a LINT DRIFT line, nothing on stderr.
func TestLintFindingsExitOneAndAnUnreadableCardExitsTwo(t *testing.T) {
	t.Parallel()

	bad := writeLintCard(t, "bad.card", strings.Replace(lintGoodCard(), "STEP 2. Read docs/SPEC-SWARM.md first.", "STEP 2. cd ../elsewhere", 1))
	exit, stdout, stderr := runSwarm(t, "lint", "--card", bad)
	require.Equal(t, 1, exit, "a card with findings exits 1 with a LINT DRIFT line, got %d\nstdout: %s\nstderr: %s", exit, stdout, stderr)
	require.Contains(t, stdout, "LINT DRIFT card=bad.card ", "a card with findings exits 1 with a LINT DRIFT line, got %d\nstdout: %s\nstderr: %s", exit, stdout, stderr)
	require.Empty(t, stderr, "a finding is a verdict on stdout, nothing on stderr: %q", stderr)

	missing := filepath.Join(t.TempDir(), "absent.card")
	exit, stdout, stderr = runSwarm(t, "lint", "--card", missing)
	require.Equal(t, 2, exit, "a card that cannot be read exits 2 with the reason on stderr, got %d\nstdout: %s\nstderr: %s", exit, stdout, stderr)
	require.Empty(t, stdout, "a card that cannot be read exits 2 with the reason on stderr, got %d\nstdout: %s\nstderr: %s", exit, stdout, stderr)
	require.Contains(t, stderr, "--card wants a readable file", "a card that cannot be read exits 2 with the reason on stderr, got %d\nstdout: %s\nstderr: %s", exit, stdout, stderr)
}

// The launcher script lint keeps the same two exits.
func TestLintFleetFindingsExitOneAndAnUnreadableScriptExitsTwo(t *testing.T) {
	t.Parallel()

	script := filepath.Join(t.TempDir(), "launch.sh")
	write(t, script, "#!/bin/bash\nmapfile -t lines < input\n")
	exit, stdout, stderr := runSwarm(t, "lint", "--fleet", script)
	require.Equal(t, 1, exit, "a launcher with findings exits 1, got %d\nstdout: %s\nstderr: %s", exit, stdout, stderr)
	require.Contains(t, stdout, "LINT DRIFT script=launch.sh ", "a launcher with findings exits 1, got %d\nstdout: %s\nstderr: %s", exit, stdout, stderr)

	exit, _, stderr = runSwarm(t, "lint", "--fleet", filepath.Join(t.TempDir(), "absent.sh"))
	require.Equal(t, 2, exit, "a launcher that cannot be read exits 2, got %d\nstderr: %s", exit, stderr)
	require.Contains(t, stderr, "--fleet wants a readable launcher script", "a launcher that cannot be read exits 2, got %d\nstderr: %s", exit, stderr)
}

// The banner belongs to the tool, not the card: the exit-codes sentence of another tool is
// not a rule the coordinator gives a child, so `template --name card` prints no such line
// and the template lints clean as printed.
func TestTheCardTemplateCarriesNoExitCodesLineAndLintsClean(t *testing.T) {
	t.Parallel()

	exit, tmpl, stderr := runSwarm(t, "template", "--name", "card")
	require.Equal(t, 0, exit, "template --name card exits 0, got %d\nstderr: %s", exit, stderr)
	for _, line := range strings.Split(tmpl, "\n") {
		require.False(t, strings.HasPrefix(strings.ToLower(line), "exit codes"), "the card template carries a banner line inside its RULES: %q", line)
	}
	card := writeLintCard(t, "template.card", filledLibraries(tmpl))
	exit, stdout, stderr := runSwarm(t, "lint", "--card", card, "--child-rules")
	require.Equal(t, 0, exit, "the card template lints clean under --child-rules, got %d\nstdout: %s\nstderr: %s", exit, stdout, stderr)
	require.Contains(t, stdout, "LINT OK card=template.card ", "the card template lints clean under --child-rules, got %d\nstdout: %s\nstderr: %s", exit, stdout, stderr)
}
