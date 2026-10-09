//go:build linux

package main

import (
	"context"
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

// SPEC-SPRINT section 7: a check's same-group descendants cannot keep executing
// against the clone after its shell exits or the caller cancels.
func TestLandingCheckReapsRedirectedDescendants(t *testing.T) {
	t.Parallel()
	for _, ending := range []string{"exit", "cancel", "deadline"} {
		t.Run(ending, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			pidFile := filepath.Join(dir, "child.pid")
			ctx, cancel := context.WithCancel(t.Context())
			if ending == "deadline" {
				cancel()
				ctx, cancel = context.WithTimeout(t.Context(), 10*time.Second)
			}
			defer cancel()
			check := "(while [ ! -f release ]; do sleep 1; done) >/dev/null 2>&1 & echo $! > child.pid; echo output; echo diagnostic >&2; printf 'environment=%s\\n' \"$NOVA_CHECK_PRIVATE\"; touch ready"
			if ending != "exit" {
				check += "; wait"
			}
			l := &lander{a: &app{gitEnv: []string{"PATH=/usr/bin:/bin", "NOVA_CHECK_PRIVATE=fixture-value"}}, check: check}
			done := make(chan struct{})
			var why, out string
			go func() { why, out = l.runCheck(ctx, dir); close(done) }()
			require.Eventually(t, func() bool { _, err := os.Stat(filepath.Join(dir, "ready")); return err == nil }, 30*time.Second, 10*time.Millisecond)
			raw, err := os.ReadFile(pidFile)
			require.NoError(t, err)
			pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
			require.NoError(t, err)
			defer func() {
				assert.NoError(t, os.WriteFile(filepath.Join(dir, "release"), nil, 0o600))
				assert.Eventually(t, func() bool { return friend.ProcessIdentity(pid) == "" }, 30*time.Second, 10*time.Millisecond)
			}()
			if ending == "cancel" {
				cancel()
			}
			select {
			case <-done:
			case <-t.Context().Done():
				t.Fatal("native check did not return")
			}
			assert.Contains(t, out, "output")
			assert.Contains(t, out, "diagnostic", "successful checks retain stderr too")
			assert.Contains(t, out, "environment=fixture-value", "the caller's explicit environment reaches the shell")
			if ending != "exit" {
				assert.NotEmpty(t, why)
			} else {
				assert.Empty(t, why)
			}
			if ending == "deadline" {
				assert.Contains(t, why, "did not finish before its deadline")
			}
			assert.Empty(t, friend.ProcessIdentity(pid), "check returned while its descendant could still execute")
		})
	}
}
