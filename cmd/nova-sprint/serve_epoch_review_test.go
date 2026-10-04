package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The wire must check the epoch the actual CLI parses, not any earlier token.
func TestServerReviewRejectsAnEpochDisabledByALaterFlag(t *testing.T) {
	t.Parallel()
	for _, tail := range [][]string{
		{"--epoch", "0", "--epoch", "-1"},
		{"--epoch=0", "-epoch=-1"},
		{"--op", "--epoch=0"},
	} {
		t.Run(tail[len(tail)-1], func(t *testing.T) {
			t.Parallel()
			r := newServerRig(t, twoLanes()...)
			r.boss("nova-sprint clear --confirm sprint")
			r.boss("nova-sprint add --stream s2 --count 4")
			r.boss("nova-sprint start")
			r.boss("nova-sprint tick")
			r.boss("nova-sprint tick")
			require.NotEmpty(t, r.queue("m1")["ready"])
			args := append([]string{"take", "--as", "m1", "--limit", "1", "--json"}, tail...)
			result := r.one(args...)
			assert.Equal(t, 2, result.Code, "stale caller disabled epoch guard: %s %s", result.Stdout, result.Stderr)
			assert.Empty(t, r.queue("m1")["working"], "a request carrying old epoch zero must not take from the reset sprint")
		})
	}
}
