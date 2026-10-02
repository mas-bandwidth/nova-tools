package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestExecRefusalNamesEveryMissingRequiredFlagTogether runs exec with no flags and
// asserts that the single refusal names --store, --as, --key and --sops together, plus a
// pasteable example invocation, rather than revealing the required flags one refusal at a
// time (nova-tools#1477).
func TestExecRefusalNamesEveryMissingRequiredFlagTogether(t *testing.T) {
	t.Parallel()
	bin := buildNovaSecrets(t)

	out, errOut, code := runNovaSecrets(bin, "exec", "--", "true")
	require.Equal(t, 125, code, "expected exit 125, got %d (stdout=%q)", code, out)

	lines := strings.Split(strings.TrimSpace(errOut), "\n")
	assert.Len(t, lines, 1, "expected a single refusal line, got %d: %s", len(lines), errOut)

	for _, flag := range []string{"--store", "--as", "--key", "--sops"} {
		assert.Contains(t, errOut, flag, "refusal must name %s together, got: %s", flag, errOut)
	}

	assert.Contains(t, errOut, "example:", "refusal must carry a pasteable example invocation, got: %s", errOut)
	assert.Contains(t, errOut, "nova-secrets exec", "refusal must carry a pasteable example invocation, got: %s", errOut)
}
