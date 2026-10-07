package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Test 5: TestGetIsRefusedBeforeAnythingIsRead
func TestGetIsRefusedBeforeAnythingIsRead(t *testing.T) {
	t.Parallel()
	bin := buildNovaSecrets(t)
	out, errOut, code := runNovaSecrets(bin, "get", "--store", "/nonexistent/store/that/would/panic")
	require.Equal(t, 2, code, "expected exit code 2, got %d", code)
	assert.Empty(t, out, "expected empty stdout, got %s", out)
	lines := strings.Split(strings.TrimSpace(errOut), "\n")
	assert.Len(t, lines, 1, "expected exactly one line on stderr, got %d: %s", len(lines), errOut)
	assert.Contains(t, errOut, "sops -d", "expected stderr to name 'sops -d': %s", errOut)
}
