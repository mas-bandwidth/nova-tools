//go:build slow

package main

import "testing"

// The owner's run: three streams of 1,000, eight machines of width 64, four
// readers, the world with no failures: the streams are dealt, read and landed
// together, tick by tick.
func TestTheOwnersRunLandsTheStreamsTogether(t *testing.T) {
	t.Parallel()
	fairStreams(t, 1000, 64)
}

// More in review at once than one ask used to take (200 a tick): every
// stream is read and landed together still.
func TestTheStreamsAreReadAndLandedTogetherPastTheOldAskBound(t *testing.T) {
	t.Parallel()
	fairStreams(t, 250, 64)
}
