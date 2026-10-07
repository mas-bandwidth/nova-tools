package sprint

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestAnExternalDependsOnReleasesOnTheTickWhenItHolds verifies that a card
// waiting on an external operand is admitted, waits, and is released by
// the tick the first tick its operand holds.
func TestAnExternalDependsOnReleasesOnTheTickWhenItHolds(t *testing.T) {
	t.Parallel()

	t.Run("pr merged operand", func(t *testing.T) {
		// Set up fake PR source
		SetPRMerged("mas-bandwidth/nova-tools", 5303)

		// Create a card with external DEPENDS-ON
		c := &Card{
			ID: "test-card-1",
			fields: map[string]string{
				"kind":      "work",
				"depends_on": "pr mas-bandwidth/nova-tools#5303 merged",
				"stream":    "test-stream",
			},
		}

		// Card should have external wait operand
		wait := WaitOf(&Snapshot{}, c)
		require.Equal(t, WaitOnExternal, wait.Operand)
		require.Len(t, wait.On, 1)

		// The operand should be detected as external
		ext := ExternalOperands(c)
		require.Len(t, ext, 1)

		// External operand should be detected as holding
		require.True(t, ExternalOperandHolds(c))

		// Wait info should be human-readable
		info := ExternalOperandWaitInfo(c)
		require.Contains(t, info, "pr mas-bandwidth/nova-tools#5303 merged")
	})

	t.Run("branch contains operand", func(t *testing.T) {
		// Set up fake branch source
		SetBranchContains("main", "abc123def456")

		// Create a card with external DEPENDS-ON
		c := &Card{
			ID: "test-card-2",
			fields: map[string]string{
				"kind":       "work",
				"depends_on": "main contains abc123def456",
				"stream":     "test-stream",
			},
		}

		// Card should have external wait operand
		wait := WaitOf(&Snapshot{}, c)
		require.Equal(t, WaitOnExternal, wait.Operand)
		require.Len(t, wait.On, 1)

		// The operand should be detected as external
		ext := ExternalOperands(c)
		require.Len(t, ext, 1)

		// External operand should be detected as holding
		require.True(t, ExternalOperandHolds(c))
	})

	t.Run("after timestamp operand", func(t *testing.T) {
		// Use a timestamp in the past so it should hold
		c := &Card{
			ID: "test-card-3",
			fields: map[string]string{
				"kind":       "work",
				"depends_on": "after 2020-01-01T00:00:00Z",
				"stream":     "test-stream",
			},
		}

		// Card should have external wait operand
		wait := WaitOf(&Snapshot{}, c)
		require.Equal(t, WaitOnExternal, wait.Operand)
		require.Len(t, wait.On, 1)

		// The operand should be detected as external
		ext := ExternalOperands(c)
		require.Len(t, ext, 1)

		// External operand should be detected as holding (timestamp in past)
		require.True(t, ExternalOperandHolds(c))
	})

	t.Run("mixed operands", func(t *testing.T) {
		SetPRMerged("owner/repo", 123)
		SetBranchContains("develop", "def456")

		c := &Card{
			ID: "test-card-4",
			fields: map[string]string{
				"kind":       "work",
				"depends_on": "pr owner/repo#123 merged, develop contains def456",
				"stream":     "test-stream",
			},
		}

		// Should detect both external operands
		ext := ExternalOperands(c)
		require.Len(t, ext, 2)

		// WaitOf should return external operand
		wait := WaitOf(&Snapshot{}, c)
		require.Equal(t, WaitOnExternal, wait.Operand)
		require.Len(t, wait.On, 2)
	})

	t.Run("non-external operands", func(t *testing.T) {
		c := &Card{
			ID: "test-card-5",
			fields: map[string]string{
				"kind":       "work",
				"depends_on": "some-card-123, another-card-456",
				"stream":     "test-stream",
			},
		}

		// Should detect no external operands
		ext := ExternalOperands(c)
		require.Len(t, ext, 0)

		// WaitOf should return card operand
		wait := WaitOf(&Snapshot{}, c)
		require.Equal(t, WaitOnCards, wait.Operand)
	})
}

// TestExternalOperandFormMalformed tests that malformed operands are rejected.
func TestExternalOperandFormMalformed(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		value string
	}{
		{"pr without merged", "pr owner/repo#123"},
		{"branch without contains", "main abc123"},
		{"malformed timestamp", "after not-a-timestamp"},
		{"empty", ""},
		{"just card id", "some-card-123"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			form := ParseExternalOperand(tt.value)
			require.Equal(t, ExternalOperandForm(""), form, "operand=%q", tt.value)
		})
	}
}

// TestExternalOperandCache verifies that CheckExternalOperands can be used
// to cache results per tick.
func TestExternalOperandCache(t *testing.T) {
	t.Parallel()

	SetPRMerged("owner/repo", 123)
	ops := []string{"pr owner/repo#123 merged"}

	// First check
	results1 := CheckExternalOperands(ops)
	require.True(t, results1["pr owner/repo#123 merged"])

	// Second check (cached)
	results2 := CheckExternalOperands(ops)
	require.True(t, results2["pr owner/repo#123 merged"])
}
