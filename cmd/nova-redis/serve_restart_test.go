//go:build !functional

package main

// serve_restart_test.go is the unit twin of the functional tier's
// TestRestartOnTheSameDirKeepsTheStore (nova-tools #3879, behaviour 25 of
// docs/SPEC-REDIS.md's "Tests this spec demands"). The store survives a
// restart on the same --dir because serve hands redis-server the same store
// directory and the same AOF config on every run, so the second server replays
// the keys the first wrote. The launch is the deps seam, so no process starts
// and the test reads no wall clock; the functional tier proves the same
// behaviour against a throwaway redis-server behind //go:build functional.

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRestartOnTheSameDirKeepsTheStore drives serve twice on one --dir through
// the production run() path and pins what makes the store survive the restart:
// the second launch is handed the same store directory and the same
// persistence config as the first, so redis-server replays the AOF it finds
// there, and the config keeps AOF, the 60 s RDB copy and no eviction, so a
// restart replays every key and a full instance refuses a write rather than
// drop a card. The launch is faked: no process starts and no wall clock is
// read.
func TestRestartOnTheSameDirKeepsTheStore(t *testing.T) {
	t.Parallel()

	h := newServeHarness(t, "pw-from-nova-secrets")
	for run := 1; run <= 2; run++ {
		code, _, errb := h.run("serve", "--bind", "127.0.0.1", "--port", "6379", "--dir", h.dir)
		require.Zero(t, code, "serve run %d: exit %d stderr %q", run, code, errb)
	}
	require.Len(t, h.launches, 2, "serve ran twice, launches %d", len(h.launches))
	first, second := h.launches[0], h.launches[1]
	assert.Equal(t, h.dir, first.Dir, "the first launch's store directory")
	assert.Equal(t, first.Dir, second.Dir, "the restart reuses the store directory %q, want %q, so redis replays it", second.Dir, first.Dir)
	assert.Equal(t, string(first.Config), string(second.Config), "the restart reuses the persistence config, so redis replays the store")

	cfg := config(t, second.Config)
	assert.Equal(t, "yes", strings.Join(only(t, cfg, "appendonly"), " "), "the store is AOF, so a restart replays every key")
	assert.Equal(t, "60 1", strings.Join(only(t, cfg, "save"), " "), "the RDB snapshot is the second copy of the store")
	assert.Equal(t, "noeviction", strings.Join(only(t, cfg, "maxmemory-policy"), " "), "a full store refuses a write rather than drop a card")
}
