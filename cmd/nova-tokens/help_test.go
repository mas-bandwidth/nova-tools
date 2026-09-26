package main

import (
	"strings"
	"testing"
)

// TestHelpListsSwarmRootForm pins that the bare help banner makes both sum forms
// discoverable: the --out/--month report, and the shipped --swarm-root/--day/--out
// ledger form. Before #494 the banner named only the first, so the shipped route
// was hidden behind reading the source.
//
// #3464: the two forms are two different calls -- main.go refuses `--month` beside
// `--swarm-root` -- so each needs its OWN `nova-tokens sum` line. Written as one line
// with a bare continuation, the banner read as a single call carrying two --out flags
// and a reader who pasted it got the refusal. This test counts the lines, the way
// `report` already prints three, so a continuation cannot come back.
func TestHelpListsSwarmRootForm(t *testing.T) {
	t.Parallel()

	r := invoke(t, "help")
	wantExit(t, r, 0)
	wantContains(t, r.stdout, "--out <dir> --month <YYYY-MM>")
	wantContains(t, r.stdout, "--swarm-root <dir> --day <YYYY-MM-DD> --out <ledger.tsv>")

	// Cut the `usage:` block, which ends at the first blank line, so the pasteable
	// example lines below it are not mistaken for synopsis lines.
	inUsage := false
	n := 0
	for _, line := range strings.Split(r.stdout, "\n") {
		switch {
		case line == "usage:":
			inUsage = true
			continue
		case inUsage && line == "":
			inUsage = false
		case inUsage && strings.HasPrefix(line, "  nova-tokens sum "):
			n++
		}
	}
	if n != 2 {
		t.Errorf("the help banner's usage block carries %d `nova-tokens sum` synopsis lines, want 2 -- one per mode:\n%s", n, r.stdout)
	}
}

// TestHelpListsFoldPoolForm pins the one production line 2efa3f52 added: fold-pool
// was already a verb, but the banner hid it. The test that landed with that commit
// reads docs/SPEC-TOKENS.md only, so reverting cmd/nova-tokens/main.go stayed green
// (#2024). This assertion goes through run("help") so a revert of that usage line
// goes red.
func TestHelpListsFoldPoolForm(t *testing.T) {
	t.Parallel()

	r := invoke(t, "help")
	wantExit(t, r, 0)
	wantContains(t, r.stdout, "nova-tokens fold-pool --pool <dir> --ledger <file> [--since <stamp>]")
}
