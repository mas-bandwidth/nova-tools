package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// The retired beat flags are out of the banner's usage, and wait -h says --quiet-beats is
// accepted and changes nothing: a beat-only change never wakes a wait.
func TestWaitHelpQuietBeatsDocumentsNoWake(t *testing.T) {
	t.Parallel()
	banner := invoke(t, "", "help").mustCode(t, 0).stdout
	for _, retired := range []string{"--quiet-beats", "--no-beat", "--beat-lease", "--beat "} {
		require.NotContains(t, banner, retired, "the banner still offers a retired flag")
	}
	help := invoke(t, "", "wait", "-h").mustCode(t, 0).stdout
	require.NotContainsf(t, help, "makes a wait return on a change that is ONLY beats", "wait help still promises --quiet-beats wakes a wait:\n%s", help)
	require.Containsf(t, help, "retired: accepted and changes nothing", "wait -h does not say --quiet-beats is accepted and changes nothing:\n%s", help)
}
