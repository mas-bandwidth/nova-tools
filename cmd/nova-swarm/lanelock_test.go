package main

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/member"
)

// The lane lock's decisions, on strings: no process, no time beyond a zero-length wait.

func TestALaneLockIsExclusiveOnlyForATimingGate(t *testing.T) {
	t.Parallel()

	for card, want := range map[string]bool{
		"rocketnet-win-lifecycle-stress":        true,
		"rocketnet-win-soak-per-transport":      true,
		"rocketnet-win-transport-matrix-repeat": true,
		"rocketnet-win-load-1000-clients":       true,
		"rocketnet-win-msvc-asan":               false,
		"rocketnet-win-libfuzzer-clang-cl":      false,
		"rocketnet-windows-udp-connreset":       false,
		"rocketnet-win-download-docs":           false, // "load" inside a word is not the gate
	} {
		assert.Equal(t, want, laneLockExclusive(card, "GATE-NEEDS: windows\n"), card)
	}
	assert.True(t, laneLockExclusive("plain", "KIND: fix\nGATE-NEEDS: windows, exclusive\n"))
	assert.False(t, laneLockExclusive("plain", "the soak is described in prose, GATE-NEEDS: windows\nnot exclusive\n"))
}

func TestTheLaneLockArgvNamesTheCardAndTheMode(t *testing.T) {
	t.Parallel()

	tmpl, err := parseLaneLock(`["C:\\nova-swarm\\bin\\winlock.exe","hold","--card","{card}"]`)
	require.NoError(t, err)
	assert.Equal(t, []string{`C:\nova-swarm\bin\winlock.exe`, "hold", "--card", "rocketnet-win-msvc-asan"},
		laneLockArgv(tmpl, member.Packet{Card: "rocketnet-win-msvc-asan"}))
	assert.Equal(t, []string{`C:\nova-swarm\bin\winlock.exe`, "hold", "--card", "x-soak", "--exclusive"},
		laneLockArgv(tmpl, member.Packet{Card: "x-soak"}))
	none, err := parseLaneLock("")
	require.NoError(t, err)
	assert.Nil(t, none)
	for _, bad := range []string{"winlock.exe hold", "[]", `[""]`, `[1]`} {
		_, err := parseLaneLock(bad)
		assert.Error(t, err, bad)
	}
}

func TestTheLaneLockStartsOnlyAfterHeld(t *testing.T) {
	t.Parallel()

	line, err := awaitHeld(strings.NewReader("HELD mode=shared slot=2/4\n"), time.Hour)
	require.NoError(t, err)
	assert.Equal(t, "HELD mode=shared slot=2/4", line)
	_, err = awaitHeld(strings.NewReader("winlock: the gate: denied\n"), time.Hour)
	assert.ErrorContains(t, err, "not HELD")
	_, err = awaitHeld(strings.NewReader(""), time.Hour)
	assert.ErrorContains(t, err, "not HELD", "a holder that ends before HELD is no lock")
	// a holder that never answers is bounded (an empty wait elapses at once)
	pr := blockingReader{}
	_, err = awaitHeld(pr, 0)
	assert.ErrorContains(t, err, "not held within")
}

// blockingReader never returns: the holder that never says HELD.
type blockingReader struct{}

func (blockingReader) Read([]byte) (int, error) { select {} }

func TestNoLaneLockTakesNothing(t *testing.T) {
	t.Parallel()

	r := &nativeRunner{}
	release, err := r.takeLaneLock(member.Packet{Card: "c"})
	require.NoError(t, err)
	release()
}
