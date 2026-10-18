package main

import (
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// The member's own mirror refresh (member.go, mirrorEvery and mirrorKeeping): a worker seat
// refreshes at its start and then every ten minutes by the clock (it was every minute, on
// every seat), and a reader refreshes none. No git runs here: the reader's keep is nil before
// any keeper is made, and the cadence is mirrorDue's, on the test's clock from 1000 s.
func TestTheMemberMirrorRefreshIsEveryTenMinutes(t *testing.T) {
	t.Parallel()
	assert.Equal(t, 10*time.Minute, mirrorEvery)
	start := time.Unix(1000, 0)
	assert.True(t, mirrorDue(time.Time{}, start), "the first pass, at start, refreshes")
	assert.False(t, mirrorDue(start, start.Add(time.Minute)), "a minute later is not due: the old cadence")
	assert.False(t, mirrorDue(start, start.Add(10*time.Minute-time.Second)))
	assert.True(t, mirrorDue(start, start.Add(10*time.Minute)), "ten minutes after the last refresh is due")
}

func TestAReaderKeepsNoMirror(t *testing.T) {
	t.Parallel()
	assert.Nil(t, mirrorKeeping("https://github.com/o/r.git", true, t.TempDir(), t.TempDir(), io.Discard, io.Discard),
		"a reader refreshes no mirror: the worker seats of the bench keep the shared ~/nova-bench/mirror")
	assert.Nil(t, mirrorKeeping("", false, t.TempDir(), t.TempDir(), io.Discard, io.Discard), "no repositories named, no mirrors kept")
}
