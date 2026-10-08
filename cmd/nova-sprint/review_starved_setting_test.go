package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// The set flag reaches the same property the review alarm reads, using the
// command's in-memory store and clock.
func TestSetReviewStarvedDurationAndOff(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a --members m1")

	assert.Contains(t, ta.ok("set --review-starved 500ms"), "review-starved 1s")
	assert.Contains(t, ta.ok("set --review-starved off"), "review-starved off")
	assert.Contains(t, ta.ok("set --review-starved 3s --friend-finish 45m"), "review-starved 3s")

	code, _, errs := ta.do("set --review-starved soon")
	assert.NotEqual(t, 0, code)
	assert.Contains(t, errs, "--review-starved wants a duration above zero")
}
