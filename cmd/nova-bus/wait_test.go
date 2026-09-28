package main

import (
	"strings"
	"testing"
)

// The wait help must document #328, not #674. The flag is accepted and changes
// nothing, so the long paragraph promising a WAIT OK line on a beat-only change is
// stale: the flag's own usage string says so, and the banner must agree. (#903)
func TestWaitHelpQuietBeatsDocumentsNoWake(t *testing.T) {
	t.Parallel()
	banner := invoke(t, "", "help").mustCode(t, 0).stdout
	if strings.Contains(banner, "makes a wait return on a change that is ONLY beats") {
		t.Fatalf("wait help still promises --quiet-beats wakes a wait:\n%s", banner)
	}
	if !strings.Contains(banner, "accepted and changes nothing") {
		t.Fatalf("wait help does not say --quiet-beats is accepted and changes nothing:\n%s", banner)
	}
}
