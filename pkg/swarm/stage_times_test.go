package swarm

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestStageCommandTimingAccumulatesFailedAttempts(t *testing.T) {
	t.Parallel()
	elapsed := time.Hour
	out, err := stageTimedOutput(stageGit(context.Background(), "--version"), &elapsed)
	assert.NoError(t, err)
	assert.Contains(t, string(out), "git version")
	assert.Greater(t, elapsed, time.Hour, "an attempt adds to the phase rather than replacing it")
	beforeFailure := elapsed
	_, err = stageTimedOutput(stageGit(context.Background(), "--stage-timing-invalid-option"), &elapsed)
	assert.Error(t, err)
	assert.Greater(t, elapsed, beforeFailure, "failed attempts also count toward the phase")
}
