package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A loop started before a binary is installed under it would tick the store with
// the code it began with: the owner's store, 2026-10-01, where a loop started at
// 23:57 (before the routes) dealt a fresh card with no route at 07:11 while the
// verbs, run from the new binary, drew routes. run reads its binary's file before
// every tick and stops, saying so, when it was replaced; an unchanged binary ticks on.
func TestRunStopsWhenItsBinaryIsReplaced(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("add --stream s1 --count 3")
	ta.ok("start")
	exe := filepath.Join(t.TempDir(), "nova-sprint")
	require.NoError(t, os.WriteFile(exe, []byte("the build the loop began with"), 0o755))
	ta.a.executable = func() (string, error) { return exe, nil }
	st, _, code := ta.a.machineVerb("run", nil, &bytes.Buffer{})
	require.NotNil(t, st, "run: %d", code)

	ticks := 0
	ta.a.ticked = func(int, time.Time, string) { ticks++ }
	var out, errb bytes.Buffer
	assert.False(t, ta.a.runLoop(context.Background(), st, 20, 3, &out, &errb), "an unchanged binary ticks on")
	assert.Equal(t, 3, ticks)
	assert.NotContains(t, out.String(), "RUN STOP")

	ticks = 0
	ta.a.ticked = func(n int, _ time.Time, _ string) {
		ticks++
		if n == 1 {
			// a release installs a new build under the running loop
			require.NoError(t, os.WriteFile(exe, []byte("the build installed under it, longer"), 0o755))
		}
	}
	out.Reset()
	assert.True(t, ta.a.runLoop(context.Background(), st, 20, 5, &out, &errb), "a replaced binary stops the loop")
	assert.Equal(t, 1, ticks, "no tick after the replacement")
	assert.Contains(t, out.String(), "RUN STOP the binary this loop runs was replaced on disk since it began ("+exe+" ")
	assert.Contains(t, out.String(), "exiting so its supervisor starts the new one")
}
