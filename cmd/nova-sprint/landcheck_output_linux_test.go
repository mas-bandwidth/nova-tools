//go:build linux

package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/friend"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// SPEC-SPRINT section 7: an overflowing check is a failure and its group is
// reaped before the next check can run against the same clone.
func TestLandingCheckOutputOverflowReapsDescendants(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	check := "(while [ ! -f release ]; do sleep 1; done) >/dev/null 2>&1 & echo $! > child.pid; head -c " + strconv.Itoa(landCheckOutputLimit+1) + " /dev/zero; wait"
	l := &lander{a: &app{gitEnv: []string{"PATH=/usr/bin:/bin"}}, check: check}
	why, out := l.runCheck(t.Context(), dir)
	raw, err := os.ReadFile(filepath.Join(dir, "child.pid"))
	require.NoError(t, err)
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	require.NoError(t, err)
	defer func() {
		assert.NoError(t, os.WriteFile(filepath.Join(dir, "release"), nil, 0o600))
		assert.Eventually(t, func() bool { return friend.ProcessIdentity(pid) == "" }, 30*time.Second, 10*time.Millisecond)
	}()
	assert.Contains(t, why, "check output exceeds")
	assert.Len(t, out, landCheckOutputLimit)
	assert.Empty(t, friend.ProcessIdentity(pid), "overflow cannot release a live descendant")
}
