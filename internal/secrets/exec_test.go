package secrets

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRunExecRefusalNamesEveryMissingRequiredFlagTogether pins 6dbfe26e (#1477):
// RunExec names every missing required flag in one refusal, plus a pasteable
// example, rather than returning on the first empty string. Reverting exec.go
// left ./internal/secrets green because nothing in the package called RunExec.
func TestRunExecRefusalNamesEveryMissingRequiredFlagTogether(t *testing.T) {
	t.Parallel()

	code, err := RunExec("", "", "", "", "", nil, []string{os.Args[0]})
	require.Equal(t, 125, code, "expected exit 125, got %d err=%v", code, err)
	require.Error(t, err, "expected a refusal, got nil")
	msg := err.Error()
	for _, flag := range []string{"--store", "--as", "--key", "--sops", "--only"} {
		assert.Contains(t, msg, flag, "refusal must name %s together, got: %s", flag, msg)
	}
	assert.Contains(t, msg, "example:", "refusal must carry a pasteable example invocation, got: %s", msg)
	assert.Contains(t, msg, "nova-secrets exec", "refusal must carry a pasteable example invocation, got: %s", msg)
}
