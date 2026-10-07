package sprint

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestExternalDependsOnOperandForms(t *testing.T) {
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ok := externalOperandForm(tt.value)
			require.Equal(t, tt.wantOk, ok, "operand=%q", tt.value)
		})
	}
}

func externalOperandForm(s string) bool {
	if s == "" {
		return false
	}
	if len(s) >= 4 && s[0:3] == "pr " {
		rest := s[3:]
		if idx := findAfter(rest, " merged"); idx > 0 {
			before := rest[:idx]
			slash := 0
			hash := 0
			for i := 0; i < len(before); i++ {
				if before[i] == 47 {
					slash++
				} else if before[i] == 35 {
					hash++
				}
			}
			return slash == 1 && hash == 1
		}
		return false
	}
	if idx := findAfter(s, " contains "); idx > 0 {
		branch := s[:idx]
		sha := s[idx+8:]
		return branch != "" && sha != ""
	}
	if len(s) >= 6 && s[0:5] == "after " {
		timestamp := s[6:]
		hasDigit := false
		hasSep := false
		for i := 0; i < len(timestamp); i++ {
			if timestamp[i] >= 48 && timestamp[i] <= 57 {
				hasDigit = true
			}
			if timestamp[i] == 84 || timestamp[i] == 45 {
				hasSep = true
			}
		}
		return len(timestamp) >= 20 && hasDigit && hasSep
	}
	return false
}

func findAfter(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
