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

// A successful promotion tree gate must finish its whole native process group
// before its checked cut is allowed to advance to push and queue admission.
func TestPromoteGateReapsRedirectedDescendantBeforeSuccess(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "child.pid")
	t.Cleanup(func() {
		assert.NoError(t, os.WriteFile(filepath.Join(dir, "release"), nil, 0o600))
		raw, err := os.ReadFile(pidFile)
		if os.IsNotExist(err) {
			return
		}
		if !assert.NoError(t, err) {
			return
		}
		pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
		if assert.NoError(t, err) {
			assert.Eventually(t, func() bool { return friend.ProcessIdentity(pid) == "" }, 30*time.Second, 10*time.Millisecond)
		}
	})
	check := "(while [ ! -f release ]; do sleep 1; done) >/dev/null 2>&1 & echo $! > child.pid; echo checked"
	p := &promoter{dir: dir, check: check, env: []string{"PATH=/usr/bin:/bin"}}
	out, err := p.runGate(t.Context(), "frozen-cut")
	require.NoError(t, err)
	assert.Contains(t, out, "checked")
	raw, err := os.ReadFile(pidFile)
	require.NoError(t, err)
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	require.NoError(t, err)
	assert.Empty(t, friend.ProcessIdentity(pid), "promotion gate returned while its redirected descendant could still run")
}
