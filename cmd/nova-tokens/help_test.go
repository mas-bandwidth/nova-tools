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
