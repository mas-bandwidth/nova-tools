package friend

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestRecoveryRateLimiting(t *testing.T) {
	t.Parallel()
	recovery := &SessionRecovery{
		RecoveryCount: DefaultRecoverMax,
		LastRecovery:  time.Now().Add(-30 * time.Minute),
	}
	assert.False(t, canRecover(recovery, DefaultRecoverMax))
	recovery.LastRecovery = time.Now().Add(-2 * time.Hour)
	assert.True(t, canRecover(recovery, DefaultRecoverMax))
}

func TestContextLimitDetection(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		err  string
		expected bool
	}{
		{"context-length", "context-length exceeded", true},
		{"prompt-too-long", "prompt-too-long error", true},
		{"no match", "rate limit error", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, detectContextLimit(tt.err))
		})
	}
}

func TestCompactionDetection(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		output string
		expected bool
	}{
		{"compacting", "message is compacting to reduce context", true},
		{"no compaction", "normal response", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, detectCompaction(tt.output))
		})
	}
}
