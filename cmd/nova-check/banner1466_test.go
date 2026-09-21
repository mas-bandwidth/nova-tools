package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The rule this test holds: a flag the verb's own refusal tells a reader to
// pass must be named on that verb's line in the banner the same refusal sends
// them to. The refusal and the banner are two halves of one answer, and a
// reader who is told "pass --allow-empty" and then opens the door they were
// sent to must not be told the flag does not exist.
//
// Both halves run the binary; neither scrapes Go source. The first invocation
// makes the verb refuse and collects the flags its remedy names; the second
// asks for the banner the refusal points at and checks that line names them.
func TestTheGateBannerNamesTheFlagItsOwnRefusalTellsYouToPass(t *testing.T) {
	dir := t.TempDir()
	cli := writeCLI(t, dir)
	receipts := filepath.Join(dir, "receipts")
	if err := os.MkdirAll(receipts, 0o755); err != nil {
		t.Fatal(err)
	}

	code, _, refusal := dogfoodRun(t, "dogfood", "gate", "--cli", cli, "--receipts", receipts)
	if code != 1 {
		t.Fatalf("the empty gate exit %d, want 1 (it should refuse an empty receipt set)\nstderr: %s", code, refusal)
	}

	token := regexp.MustCompile(`--[a-z][a-z0-9-]*`)
	seen := map[string]bool{}
	for _, tok := range token.FindAllString(refusal, -1) {
		seen[tok] = true
	}
	if len(seen) == 0 {
		t.Fatalf("the refusal names no flag, so this test would check nothing:\n%s", refusal)
	}

	code, stdout, helpErr := dogfoodRun(t, "help")
	if code != 0 {
		t.Fatalf("`nova-check help` exit %d, want 0\nstderr: %s", code, helpErr)
	}
	gateLines := []string{}
	for _, l := range strings.Split(stdout, "\n") {
		if strings.Contains(l, "nova-check dogfood gate") {
			gateLines = append(gateLines, l)
		}
	}
	if len(gateLines) != 1 {
		t.Fatalf("the banner has %d lines naming `nova-check dogfood gate`, want exactly 1:\n%s", len(gateLines), stdout)
	}
	banner := gateLines[0]

	for flag := range seen {
		if !strings.Contains(banner, flag) {
			t.Fatalf("the gate's refusal tells the reader to pass %s, but the banner line it sends them to does not name it:\nrefusal: %s\nbanner:  %s\nmissing flag: %s", flag, strings.TrimRight(refusal, "\n"), strings.TrimRight(banner, "\n"), flag)
		}
	}
}
