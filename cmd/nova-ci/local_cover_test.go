package main

// local_cover_test.go is the unit coverage of execLocal, the verb's production
// runner (local.go). The card's unit tier starts no child: local_test.go's fake
// answers every command from a table, so execLocal itself stood at 0.0% in the
// per-function coverage table. These rows drive execLocal on the two answers it
// gives before a child exists: the refusal of an empty argv, and a command the
// start cannot find, which errors before any process is created. The exit
// status of a child that actually runs is the function's main path and needs a
// real subprocess, so no unit test can reach it: it runs only when the verb is
// invoked for real, with execLocal wired in by main.go.

import (
	"bytes"
	"errors"
	"os/exec"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLocalCoverExecLocalRefusesBeforeStartingAChild pins execLocal's two
// answers that need no child process: an empty argv is refused as "empty
// command" with status -1, and a command that cannot be found fails to start
// with status -1 and an error that is not an exit status. Neither writes to the
// command's streams.
func TestLocalCoverExecLocalRefusesBeforeStartingAChild(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		argv    []string
		wantErr string
	}{
		{"empty argv is refused", nil, "empty command"},
		{"an empty argv slice is refused", []string{}, "empty command"},
		{"a command that cannot be found fails to start", []string{"nova-ci-execlocal-no-such-binary.test"}, "executable file not found"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var out, errb bytes.Buffer
			code, err := execLocal(localCmd{Argv: tc.argv, Stdout: &out, Stderr: &errb})
			assert.Equal(t, -1, code, "status = %d, want -1: %v", code, err)
			require.Error(t, err, "a start that never happened is an error, not exit status 0")
			assert.False(t, errors.As(err, new(*exec.ExitError)), "err = %v, want a start error, not a child's exit status", err)
			if tc.wantErr != "" {
				assert.Contains(t, err.Error(), tc.wantErr, "err = %v, want it to say %q", err, tc.wantErr)
			}
			assert.Empty(t, out.String(), "stdout = %q, want nothing written before a child starts", out.String())
			assert.Empty(t, errb.String(), "stderr = %q, want nothing written before a child starts", errb.String())
		})
	}
}
