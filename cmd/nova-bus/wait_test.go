package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// The wait help must document #328, not #674. The flag is accepted and changes
// nothing, so the long paragraph promising a WAIT OK line on a beat-only change is
// stale: the flag's own usage string says so, and the banner must agree. (#903)
func TestWaitHelpQuietBeatsDocumentsNoWake(t *testing.T) {
	t.Parallel()
	banner := invoke(t, "", "wait", "-h").mustCode(t, 0).stdout
	require.NotContainsf(t, banner, "makes a wait return on a change that is ONLY beats", "wait help still promises --quiet-beats wakes a wait:\n%s", banner)
	require.Containsf(t, banner, "accepted and changes nothing", "wait help does not say --quiet-beats is accepted and changes nothing:\n%s", banner)
}
