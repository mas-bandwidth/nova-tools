package main

import (
	"github.com/stretchr/testify/assert"
	"testing"
)

func TestHardWidthReviewNegativeLimitCannotTakePastWidth(t *testing.T) {
	t.Parallel()
	r := newServerRig(t, twoLanes()...)
	res := r.one("take", "--as", "m1", "--limit", "-1", "--epoch", "0", "--json")
	assert.LessOrEqual(t, len(r.queue("m1")["working"]), 2, "a negative limit must refuse or clamp, never bypass the hard width: %s%s", res.Stdout, res.Stderr)
}
