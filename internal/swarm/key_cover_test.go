package swarm

// key_cover_test.go reaches the two key.go functions the unit tier's
// per-function coverage table showed short: SecretFromEnv (key.go:14) and
// redactedReason (key.go:25). Both are pure reads of the process's own
// environment and of an error value, so nothing here sleeps, reads a real
// clock, opens a socket, starts a process or touches a store. SecretFromEnv's
// main path reads a variable already present in this process rather than
// mutating the environment: t.Setenv panics under t.Parallel and os.Setenv
// would change every other test in the binary. Every test is named
// TestKeyCoverSomething so `-run TestKeyCover` selects them.

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestKeyCoverSecretFromEnv pins the main path (a present variable answers its
// own value unchanged) and the refusal (an absent variable is named, with the
// remedy, and answers no value).
func TestKeyCoverSecretFromEnv(t *testing.T) {
	t.Parallel()

	// The main path needs a variable that is present and non-empty in this
	// process. Take one that is already there; nothing is mutated.
	var present, presentValue string
	for _, kv := range os.Environ() {
		name, value, ok := strings.Cut(kv, "=")
		if ok && strings.TrimSpace(value) != "" {
			present, presentValue = name, value
			break
		}
	}
	require.NotEmpty(t, present, "this test needs one non-empty variable already in the environment")

	for _, tc := range []struct {
		name    string
		varName string
		want    string
		wantErr string
	}{
		{"reads a present variable's value unchanged", present, presentValue, ""},
		{"refuses an absent variable", "NOVA_SWARM_KEY_COVER_ABSENT", "", "absent or empty"},
		{"refuses a whitespace variable name", "   ", "", "absent or empty"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := SecretFromEnv(tc.varName)
			if tc.wantErr != "" {
				require.Error(t, err, "an unset secret must be refused, not answered empty")
				assert.Empty(t, got, "a refusal carries no value")
				assert.ErrorContains(t, err, tc.wantErr, "the refusal says the variable is absent or empty")
				assert.ErrorContains(t, err, tc.varName, "the refusal names the variable")
				return
			}
			require.NoError(t, err, "a present variable is not an error")
			assert.Equal(t, tc.want, got, "the secret's own value is answered unchanged")
		})
	}
}

// TestKeyCoverRedactedReason pins the three answers: a nil error is named and
// never dereferenced, a path error answers only its errno so the key file's
// path and bytes cannot appear in a reason, and any other error keeps its own
// text.
func TestKeyCoverRedactedReason(t *testing.T) {
	t.Parallel()

	const secretPath = "/run/keys/worker.pem"
	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{"a nil error is named, not dereferenced", nil, "<nil>"},
		{"a path error answers the errno alone", &os.PathError{Op: "open", Path: secretPath, Err: os.ErrNotExist}, os.ErrNotExist.Error()},
		{"an ordinary error keeps its own text", errors.New("worker description is not JSON"), "worker description is not JSON"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := redactedReason(tc.err)
			assert.Equal(t, tc.want, got)
			assert.NotContains(t, got, secretPath, "a redacted reason never carries the key file's path")
		})
	}
}
