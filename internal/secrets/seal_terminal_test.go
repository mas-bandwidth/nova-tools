package secrets

import (
	"errors"
	"io"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

type terminalFakeClock struct {
	mu         sync.Mutex
	current    time.Time
	sleepCalls int
	slept      time.Duration
}

func newTerminalFakeClock(start time.Time) *terminalFakeClock {
	return &terminalFakeClock{current: start}
}

func (fc *terminalFakeClock) Now() time.Time {
	fc.mu.Lock()
	defer fc.mu.Unlock()
	return fc.current
}

func (fc *terminalFakeClock) Sleep(d time.Duration) {
	fc.mu.Lock()
	defer fc.mu.Unlock()
	fc.sleepCalls++
	fc.slept += d
	fc.current = fc.current.Add(d)
}

func (fc *terminalFakeClock) stats() (int, time.Duration) {
	fc.mu.Lock()
	defer fc.mu.Unlock()
	return fc.sleepCalls, fc.slept
}

func newTerminalTestOptions(t *testing.T, secretValue string, clock *terminalFakeClock, ghViewHandler func() ([]byte, error)) SealOptions {
	t.Helper()
	f := newSealFixture(t, "TARGET: old\n")
	opts := f.options(t, "TARGET", secretValue+"\n", false)
	opts.Now = clock.Now
	opts.Sleep = clock.Sleep
	opts.Exec = func(stdin io.Reader, env []string, dir, name string, args ...string) ([]byte, error) {
		cmdName := filepath.Base(name)
		if cmdName == "sops" || name == opts.SopsPath {
			if slices.Contains(args, "--version") {
				return []byte("sops 3.13.3\n"), nil
			}
			if slices.Contains(args, "-d") {
				return []byte("TARGET: old\n"), nil
			}
			return []byte("ENC[marker]\n"), nil
		}
		if cmdName == "git" || name == opts.GitPath {
			if len(args) >= 2 && args[0] == "rev-parse" && args[1] == "--abbrev-ref" {
				return []byte("main\n"), nil
			}
			if len(args) >= 2 && args[0] == "rev-parse" && args[1] == "HEAD" {
				return []byte("1111111111111111111111111111111111111111\n"), nil
			}
			return []byte(""), nil
		}
		if cmdName == "gh" || name == opts.GHPath {
			if len(args) >= 2 && args[0] == "pr" && args[1] == "create" {
				return []byte("https://example.com/mas-bandwidth/secrets/pull/42\n"), nil
			}
			if len(args) >= 2 && args[0] == "pr" && args[1] == "view" {
				return ghViewHandler()
			}
			if len(args) >= 2 && args[0] == "pr" && args[1] == "merge" {
				return []byte(""), nil
			}
		}
		return realExecCommand(stdin, env, dir, name, args...)
	}
	return opts
}

// TestSealStopsOnTerminalReviewState asserts that terminal review states stop
// immediately with a redacted one-line reason and remedy without waiting out
// the poll deadline, and only pending review polls to the bounded deadline
// (SPEC-SECRETS seal).
func TestSealStopsOnTerminalReviewState(t *testing.T) {
	t.Parallel()
	skipPOSIXFakesOnWindows(t)

	const secretValue = "topsecret-value-must-not-leak"
	startTime := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)

	terminalCases := []struct {
		name    string
		handler func() ([]byte, error)
	}{
		{
			name: "CHANGES_REQUESTED",
			handler: func() ([]byte, error) {
				return []byte(`{"state":"OPEN","reviewDecision":"CHANGES_REQUESTED"}`), nil
			},
		},
		{
			name: "CLOSED",
			handler: func() ([]byte, error) {
				return []byte(`{"state":"CLOSED","reviewDecision":""}`), nil
			},
		},
		{
			name: "MERGED",
			handler: func() ([]byte, error) {
				return []byte(`{"state":"MERGED","reviewDecision":""}`), nil
			},
		},
		{
			name: "missing PR",
			handler: func() ([]byte, error) {
				return nil, errors.New("Could not resolve to a PullRequest with the number 42")
			},
		},
		{
			name: "terminal check failure",
			handler: func() ([]byte, error) {
				return []byte(`{"state":"OPEN","reviewDecision":"","statusCheckRollup":[{"name":"seat-rule","status":"COMPLETED","conclusion":"FAILURE"}]}`), nil
			},
		},
	}

	for _, tc := range terminalCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			clock := newTerminalFakeClock(startTime)
			opts := newTerminalTestOptions(t, secretValue, clock, tc.handler)

			line, err := RunSeal(opts)
			require.Error(t, err, "terminal state %s must return an error", tc.name)
			assert.Empty(t, line, "terminal state must not return an OK line")

			sleepCalls, slept := clock.stats()
			assert.Equal(t, 0, sleepCalls, "terminal state %s must stop immediately without sleeping", tc.name)
			assert.Equal(t, time.Duration(0), slept, "terminal state %s must stop immediately without advancing clock", tc.name)

			errMsg := err.Error()
			assert.False(t, strings.Contains(errMsg, "\n"), "error must be one line: %q", errMsg)
			assert.False(t, strings.Contains(errMsg, secretValue), "error must not leak secret value: %q", errMsg)
			assert.True(t, oneline.HasRemedy(errMsg), "error must carry a remedy: %q", errMsg)
		})
	}

	t.Run("a queued gate is not cut off at 2 minutes", func(t *testing.T) {
		t.Parallel()
		clock := newTerminalFakeClock(startTime)
		var progress strings.Builder
		opts := newTerminalTestOptions(t, secretValue, clock, func() ([]byte, error) {
			_, slept := clock.stats()
			if slept < 2*time.Minute+5*time.Second {
				return []byte(`{"state":"OPEN","reviewDecision":"REVIEW_REQUIRED","statusCheckRollup":[{"name":"seat-rule","status":"QUEUED"}]}`), nil
			}
			return []byte(`{"state":"OPEN","reviewDecision":"APPROVED","statusCheckRollup":[{"name":"seat-rule","status":"COMPLETED","conclusion":"SUCCESS","startedAt":"2026-10-04T12:02:05Z"}]}`), nil
		})
		opts.Progress = &progress

		line, err := RunSeal(opts)
		require.NoError(t, err, "a gate that has not started must not be abandoned at 2 minutes: %v", err)
		assert.Contains(t, line, "merged", "the seal finishes once the gate approves: %q", line)
		assert.NotContains(t, line, secretValue, "OK line must not leak secret value")
		assert.Contains(t, progress.String(), "gate job has not started", "the wait says the job has not started: %s", progress.String())

		sleepCalls, slept := clock.stats()
		assert.Greater(t, sleepCalls, 0, "a queued gate must keep polling")
		assert.GreaterOrEqual(t, slept, 2*time.Minute, "the old 2-minute cap must not stop the wait")
	})

	t.Run("APPROVED merges without waiting", func(t *testing.T) {
		t.Parallel()
		clock := newTerminalFakeClock(startTime)
		opts := newTerminalTestOptions(t, secretValue, clock, func() ([]byte, error) {
			return []byte(`{"state":"OPEN","reviewDecision":"APPROVED","statusCheckRollup":[]}`), nil
		})

		line, err := RunSeal(opts)
		require.NoError(t, err, "approved review must merge without error")
		assert.Contains(t, line, "merged", "approved review must report merged: %q", line)
		assert.False(t, strings.Contains(line, secretValue), "merged line must not leak secret value")

		sleepCalls, slept := clock.stats()
		assert.Equal(t, 0, sleepCalls, "approved review must merge immediately without polling")
		assert.Equal(t, time.Duration(0), slept, "approved review must not wait")
	})
	t.Run("APPROVED with a failed seat-rule check and no matching head refuses", func(t *testing.T) {
		t.Parallel()
		clock := newTerminalFakeClock(startTime)
		opts := newTerminalTestOptions(t, secretValue, clock, func() ([]byte, error) {
			return []byte(`{"state":"OPEN","reviewDecision":"APPROVED","statusCheckRollup":[{"name":"seat-rule","status":"COMPLETED","conclusion":"FAILURE","detailsUrl":"https://example.com/gate-log"}]}`), nil
		})

		line, err := RunSeal(opts)
		require.Error(t, err, "an approved review does not override a failed seat-rule check")
		assert.Empty(t, line, "the refusal must not be an OK line")
		assert.NotContains(t, err.Error(), secretValue, "the refusal must not leak the value")
		assert.Contains(t, err.Error(), "seat-rule", "the refusal must name the failed check: %v", err)
		assert.Contains(t, err.Error(), "https://example.com/gate-log", "the refusal prints the gate's failing line: %v", err)
		assert.NotContains(t, err.Error(), "REFUSED", "the check failure is the check sentence, not a second refusal word")

		sleepCalls, slept := clock.stats()
		assert.Equal(t, 0, sleepCalls, "a failed check must stop without waiting")
		assert.Equal(t, time.Duration(0), slept, "a failed check must not wait")
	})

	t.Run("a stale seat-rule check is waived when headRefOid is the sealed commit", func(t *testing.T) {
		t.Parallel()
		clock := newTerminalFakeClock(startTime)
		opts := newTerminalTestOptions(t, secretValue, clock, func() ([]byte, error) {
			return []byte(`{"state":"OPEN","reviewDecision":"APPROVED","headRefOid":"1111111111111111111111111111111111111111","statusCheckRollup":[{"name":"seat-rule","status":"COMPLETED","conclusion":"FAILURE"}]}`), nil
		})

		line, err := RunSeal(opts)
		require.NoError(t, err, "a stale seat-rule check on this commit must not refuse: %v", err)
		assert.Contains(t, line, "merged", "the waived review must report merged: %q", line)
		assert.Contains(t, line, "GATE APPROVE", "the waived case prints the local gate verdict: %q", line)
		assert.NotContains(t, line, "REFUSED", "the waived case must not say REFUSED: %q", line)
		assert.NotContains(t, line, secretValue, "merged line must not leak the value")

		sleepCalls, slept := clock.stats()
		assert.Equal(t, 0, sleepCalls, "a waived seat-rule check must not wait")
		assert.Equal(t, time.Duration(0), slept, "a waived seat-rule check must not wait")
	})

	t.Run("a local gate failure refuses before the review", func(t *testing.T) {
		t.Parallel()
		clock := newTerminalFakeClock(startTime)
		opts := newTerminalTestOptions(t, secretValue, clock, func() ([]byte, error) {
			return []byte(`{"state":"OPEN","reviewDecision":"APPROVED","headRefOid":"1111111111111111111111111111111111111111","statusCheckRollup":[{"name":"seat-rule","status":"COMPLETED","conclusion":"FAILURE"}]}`), nil
		})
		opts.Gate = func(GateInput) (string, int) {
			return "GATE FAILED rule=3 check=3 file=notes.txt: only .sops.yaml, README.md and seat .yaml files may change", 1
		}

		line, err := RunSeal(opts)
		require.Error(t, err, "a local gate failure must refuse")
		assert.Empty(t, line, "a gate failure must not be an OK line")
		assert.Contains(t, err.Error(), "GATE FAILED", "the refusal is the gate verdict: %v", err)
		assert.NotContains(t, err.Error(), secretValue, "the refusal must not leak the value")

		sleepCalls, slept := clock.stats()
		assert.Equal(t, 0, sleepCalls, "a local gate failure must not wait on the review")
		assert.Equal(t, time.Duration(0), slept, "a local gate failure must not wait")
	})
}
