package secrets

import (
	"errors"
	"io"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/oneline"
)

// newTerminalTestOptions wires a clockwork fake clock into the options: Now is
// the fake's, and Sleep is synchronous (clockwork's own Sleep blocks until an
// Advance, but RunSeal sleeps synchronously), advancing the fake and recording
// the sleeps so the assertions below can hold the poll count to a number.
func newTerminalTestOptions(t *testing.T, secretValue string, clock *clockwork.FakeClock, ghViewHandler func() ([]byte, error)) (SealOptions, func() (int, time.Duration)) {
	t.Helper()
	f := newSealFixture(t, "TARGET: old\n")
	opts := f.options(t, "TARGET", secretValue+"\n", false)
	var sleepCalls int
	var slept time.Duration
	opts.Now = clock.Now
	opts.Sleep = func(d time.Duration) { sleepCalls++; slept += d; clock.Advance(d) }
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
	return opts, func() (int, time.Duration) { return sleepCalls, slept }
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
			clock := clockwork.NewFakeClockAt(startTime)
			opts, stats := newTerminalTestOptions(t, secretValue, clock, tc.handler)

			line, err := RunSeal(opts)
			require.Error(t, err, "terminal state %s must return an error", tc.name)
			assert.Empty(t, line, "terminal state must not return an OK line")

			sleepCalls, slept := stats()
			assert.Equal(t, 0, sleepCalls, "terminal state %s must stop immediately without sleeping", tc.name)
			assert.Equal(t, time.Duration(0), slept, "terminal state %s must stop immediately without advancing clock", tc.name)

			errMsg := err.Error()
			assert.False(t, strings.Contains(errMsg, "\n"), "error must be one line: %q", errMsg)
			assert.False(t, strings.Contains(errMsg, secretValue), "error must not leak secret value: %q", errMsg)
			assert.True(t, oneline.HasRemedy(errMsg), "error must carry a remedy: %q", errMsg)
		})
	}

	t.Run("PENDING review polls to deadline", func(t *testing.T) {
		t.Parallel()
		clock := clockwork.NewFakeClockAt(startTime)
		opts, stats := newTerminalTestOptions(t, secretValue, clock, func() ([]byte, error) {
			return []byte(`{"state":"OPEN","reviewDecision":"REVIEW_REQUIRED","statusCheckRollup":[]}`), nil
		})

		line, err := RunSeal(opts)
		require.NoError(t, err, "pending review timeout must return an OK line, not error")
		assert.Contains(t, line, "open (gate not yet approved)", "pending review must report open line: %q", line)
		assert.False(t, strings.Contains(line, secretValue), "OK line must not leak secret value")

		sleepCalls, slept := stats()
		assert.Greater(t, sleepCalls, 0, "pending review must poll")
		assert.GreaterOrEqual(t, slept, 2*time.Minute, "pending review must poll to the bounded deadline")
	})

	t.Run("APPROVED merges without waiting", func(t *testing.T) {
		t.Parallel()
		clock := clockwork.NewFakeClockAt(startTime)
		opts, stats := newTerminalTestOptions(t, secretValue, clock, func() ([]byte, error) {
			return []byte(`{"state":"OPEN","reviewDecision":"APPROVED","statusCheckRollup":[]}`), nil
		})

		line, err := RunSeal(opts)
		require.NoError(t, err, "approved review must merge without error")
		assert.Contains(t, line, "merged", "approved review must report merged: %q", line)
		assert.False(t, strings.Contains(line, secretValue), "merged line must not leak secret value")

		sleepCalls, slept := stats()
		assert.Equal(t, 0, sleepCalls, "approved review must merge immediately without polling")
		assert.Equal(t, time.Duration(0), slept, "approved review must not wait")
	})
}
