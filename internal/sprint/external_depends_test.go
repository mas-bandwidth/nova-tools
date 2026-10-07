package sprint

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestExternalOperandForm verifies that the three external operand forms
// are recognized correctly.
func TestExternalOperandForm(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		value  string
		wantOk bool
	}{
		{"pr merged form", "pr mas-bandwidth/nova-tools#5303 merged", true},
		{"branch contains form", "sprint/external-depends-on-b.w5.g1.e15 contains abc123", true},
		{"after timestamp form", "after 2026-10-06T10:30:00Z", true},
		{"pr without merged suffix", "pr mas-bandwidth/nova-tools#5303", false},
		{"malformed repo", "pr nova-tools#5303 merged", false},
		{"missing sha", "branch contains", false},
		{"malformed timestamp", "after not-a-timestamp", false},
		{"empty string", "", false},
		{"card id", "some-card-123", false},
		{"owner/repo#n", "owner/repo#123", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := ParseExternalOperand(tt.value)
			require.Equal(t, tt.wantOk, got != "", "operand=%q", tt.value)
		})
	}
}

// TestExternalOperandDetails verifies that parsing extracts correct details.
func TestExternalOperandDetails(t *testing.T) {
	t.Parallel()

	t.Run("pr merged", func(t *testing.T) {
		ops := ParseExternalOperandDetails("pr mas-bandwidth/nova-tools#5303 merged")
		require.Equal(t, FormPrMerged, ops.Form)
		require.Equal(t, "mas-bandwidth/nova-tools", ops.Repo)
		require.Equal(t, 5303, ops.PRNumber)
	})

	t.Run("branch contains", func(t *testing.T) {
		ops := ParseExternalOperandDetails("main contains abc123def456")
		require.Equal(t, FormBranchContains, ops.Form)
		require.Equal(t, "main", ops.Branch)
		require.Equal(t, "abc123def456", ops.Sha)
	})

	t.Run("after timestamp", func(t *testing.T) {
		ops := ParseExternalOperandDetails("after 2026-10-06T10:30:00Z")
		require.Equal(t, FormAfter, ops.Form)
		require.Equal(t, "2026-10-06T10:30:00Z", ops.At)
	})
}

// TestExternalOperands verifies extraction of external operands from DEPENDS-ON.
func TestExternalOperands(t *testing.T) {
	t.Parallel()

	c := &Card{fields: map[string]string{
		"depends_on": "pr mas-bandwidth/nova-tools#5303 merged, main contains abc123, after 2026-10-06T10:30:00Z",
	}}

	ext := ExternalOperands(c)
	require.Len(t, ext, 3)
	require.Contains(t, ext, "pr mas-bandwidth/nova-tools#5303 merged")
	require.Contains(t, ext, "main contains abc123")
	require.Contains(t, ext, "after 2026-10-06T10:30:00Z")
}

// TestExternalOperandString verifies human-readable descriptions.
func TestExternalOperandString(t *testing.T) {
	t.Parallel()

	t.Run("pr merged", func(t *testing.T) {
		ops := ExternalOperand{Form: FormPrMerged, Repo: "owner/repo", PRNumber: 123}
		require.Equal(t, "pr owner/repo#123 merged", ops.String())
	})

	t.Run("branch contains", func(t *testing.T) {
		ops := ExternalOperand{Form: FormBranchContains, Branch: "main", Sha: "abc123"}
		require.Equal(t, "main contains abc123", ops.String())
	})

	t.Run("after", func(t *testing.T) {
		ops := ExternalOperand{Form: FormAfter, At: "2026-10-06T10:30:00Z"}
		require.Equal(t, "after 2026-10-06T10:30:00Z", ops.String())
	})
}
