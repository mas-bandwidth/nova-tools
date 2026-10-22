package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/swarm"
)

// A SILENT HARNESS IS NAMED, NOT COUNTED AS THE CARD'S FAULT. On 2026-10-15 a native run on
// mini-m5-1 (opencode on an openrouter route) printed nothing for 300s, never created its
// session store, spent no token, and was ended by the idle watch; the member then finished
// the card `idle-reap: no RESULT.md shape`, which the sprint counts as the card's own
// harness fault, twice, and stops redealing it. Nothing said the harness never started, and
// when the route had been refused for credit nothing said that either.
//
// The idle watch is the run's own seam (nativeRunConfig.idleFn) and it is handed the end it
// would send, with the time passed in (300s): no clock decides anything here and nothing
// waits. The seam is called by the run itself, after the fake harness is started and before
// the run waits on it, so what the harness "printed" or "logged" before the watch fired is
// written there, in the files a real harness writes, by the test.

// TestKilledRunsAreProviderEndsOnlyForCredit: a killed run (rc < 0) is the deadline's or the
// watch's end whatever its log says, except the provider's refusal for credit.
func TestKilledRunsAreProviderEndsOnlyForCredit(t *testing.T) {
	t.Parallel()
	since := time.Unix(0, 0)
	for _, tc := range []struct {
		name, printed string
		want          string
	}{
		{"out of credit, printed", `timestamp=t level=ERROR message="stream error" error.error="AI_APICallError: Insufficient credits"` + "\n", swarm.CauseCredit},
		{"a payment required status", `timestamp=t level=ERROR message="stream error" statusCode=402` + "\n", swarm.CauseCredit},
		{"a server error stays the deadline's", `timestamp=t level=ERROR message="stream error" error.error="HTTP 503 overloaded"` + "\n", ""},
		{"nothing printed", "SANDBOX OK cmd=opencode\n", ""},
	} {
		cause, ok := providerEnd("", t.TempDir(), 0, since, -1, []byte(tc.printed))
		require.Equal(t, tc.want != "", ok, tc.name)
		require.Equal(t, tc.want, cause.Class, tc.name)
	}
	// the exit path is untouched: a run that exited with a server error is still a provider end
	cause, ok := providerEnd("", t.TempDir(), 0, since, 1, []byte(`level=ERROR message="stream error" error.error="HTTP 503 overloaded"`+"\n"))
	require.True(t, ok)
	require.Equal(t, swarm.Cause5xx, cause.Class)
}

// TestIdleCauseNeedsSilenceNoTokensAndNoStore: the three things a harness that never started
// work lacks, each of which a harness that worked has at least one of.
func TestIdleCauseNeedsSilenceNoTokensAndNoStore(t *testing.T) {
	t.Parallel()
	bare := t.TempDir()
	withStore := t.TempDir()
	db := filepath.Join(withStore, "opencode", "opencode.db")
	require.NoError(t, os.MkdirAll(filepath.Dir(db), 0o755))
	require.NoError(t, os.WriteFile(db, nil, 0o644))
	for _, tc := range []struct {
		name, headless, harness, data string
		spent                         int
		observed                      bool
		want                          string
	}{
		{"silent, no tokens, no store", "", "silent", bare, 0, false, swarm.IdleCauseNeverStarted},
		{"silent, tokens read as a dash", "", "silent", bare, 0, true, swarm.IdleCauseNeverStarted},
		{"it printed", "", "ok", bare, 0, false, ""},
		{"it spent tokens", "", "silent", bare, 12, true, ""},
		{"it made its store", "", "silent", withStore, 0, false, ""},
		{"a headless harness keeps no opencode store", "claude", "silent", withStore, 0, false, swarm.IdleCauseNeverStarted},
	} {
		require.Equal(t, tc.want, idleCauseOf(tc.headless, tc.harness, tc.data, tc.spent, tc.observed), tc.name)
	}
	require.True(t, strings.HasPrefix(swarm.CardIdleLine("c", swarm.IdleEnd{Idle: 300 * time.Second, Cause: swarm.IdleCauseNeverStarted}), "CARD IDLE task=c step=- idle=300s cause="))
}
