package main

import "testing"

// TestHelpListsSwarmRootForm pins that the bare help banner makes both sum forms
// discoverable: the --out/--month report, and the shipped --swarm-root/--day/--out
// ledger form. Before #494 the banner named only the first, so the shipped route
// was hidden behind reading the source.
func TestHelpListsSwarmRootForm(t *testing.T) {
	r := invoke(t, "help")
	wantExit(t, r, 0)
	wantContains(t, r.stdout, "--out <dir> --month <YYYY-MM>")
	wantContains(t, r.stdout, "--swarm-root <dir> --day <YYYY-MM-DD> --out <ledger.tsv>")
}

// TestHelpListsFoldPoolForm pins the one production line 2efa3f52 added: fold-pool
// was already a verb, but the banner hid it. The test that landed with that commit
// reads docs/SPEC-TOKENS.md only, so reverting cmd/nova-tokens/main.go stayed green
// (#2024). This assertion goes through run("help") so a revert of that usage line
// goes red.
func TestHelpListsFoldPoolForm(t *testing.T) {
	r := invoke(t, "help")
	wantExit(t, r, 0)
	wantContains(t, r.stdout, "nova-tokens fold-pool --pool <dir> --ledger <file> [--since <stamp>]")
}
