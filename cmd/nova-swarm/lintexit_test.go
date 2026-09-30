package main

import (
	"path/filepath"
	"strings"
	"testing"
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
	if exit != 1 || !strings.Contains(stdout, "LINT DRIFT card=bad.card ") {
		t.Fatalf("a card with findings exits 1 with a LINT DRIFT line, got %d\nstdout: %s\nstderr: %s", exit, stdout, stderr)
	}
	if stderr != "" {
		t.Fatalf("a finding is a verdict on stdout, nothing on stderr: %q", stderr)
	}

	missing := filepath.Join(t.TempDir(), "absent.card")
	exit, stdout, stderr = runSwarm(t, "lint", "--card", missing)
	if exit != 2 || stdout != "" || !strings.Contains(stderr, "--card wants a readable file") {
		t.Fatalf("a card that cannot be read exits 2 with the reason on stderr, got %d\nstdout: %s\nstderr: %s", exit, stdout, stderr)
	}
}

// The launcher script lint keeps the same two exits.
func TestLintFleetFindingsExitOneAndAnUnreadableScriptExitsTwo(t *testing.T) {
	t.Parallel()

	script := filepath.Join(t.TempDir(), "launch.sh")
	write(t, script, "#!/bin/bash\nmapfile -t lines < input\n")
	exit, stdout, stderr := runSwarm(t, "lint", "--fleet", script)
	if exit != 1 || !strings.Contains(stdout, "LINT DRIFT script=launch.sh ") {
		t.Fatalf("a launcher with findings exits 1, got %d\nstdout: %s\nstderr: %s", exit, stdout, stderr)
	}

	exit, _, stderr = runSwarm(t, "lint", "--fleet", filepath.Join(t.TempDir(), "absent.sh"))
	if exit != 2 || !strings.Contains(stderr, "--fleet wants a readable launcher script") {
		t.Fatalf("a launcher that cannot be read exits 2, got %d\nstderr: %s", exit, stderr)
	}
}

// The banner belongs to the tool, not the card: the exit-codes sentence of another tool is
// not a rule the coordinator gives a child, so `template --name card` prints no such line
// and the template lints clean as printed.
func TestTheCardTemplateCarriesNoExitCodesLineAndLintsClean(t *testing.T) {
	t.Parallel()

	exit, tmpl, stderr := runSwarm(t, "template", "--name", "card")
	if exit != 0 {
		t.Fatalf("template --name card exits 0, got %d\nstderr: %s", exit, stderr)
	}
	for _, line := range strings.Split(tmpl, "\n") {
		if strings.HasPrefix(strings.ToLower(line), "exit codes") {
			t.Fatalf("the card template carries a banner line inside its RULES: %q", line)
		}
	}
	card := writeLintCard(t, "template.card", tmpl)
	exit, stdout, stderr := runSwarm(t, "lint", "--card", card, "--child-rules")
	if exit != 0 || !strings.Contains(stdout, "LINT OK card=template.card ") {
		t.Fatalf("the card template lints clean under --child-rules, got %d\nstdout: %s\nstderr: %s", exit, stdout, stderr)
	}
}
