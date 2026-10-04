package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// The run verb's production signal seam, reached as code for the first time: run_test.go
// and handoff_test.go replace runSignals with fakes, so notifyTerminating itself sat at
// 0.0% in the unit tier's per-function table. This test walks its main path -- the
// captured channel, its buffer and the stop that puts the handlers back -- without
// sending a signal, because a signal sent to this process reaches every channel
// registered in it, and the darwin wall tests run wrapped children in process beside
// this one. Its registration of SIGINT and SIGTERM is held by supervise's tests, which
// exercise the seam's reader end; the delivery end is the functional tier's to prove.
func TestRunCoverNotifyTerminatingHandsBackAChannelAndAStop(t *testing.T) {
	t.Parallel()

	c, stop := notifyTerminating()
	require.NotNil(t, c, "the seam hands back the channel the terminating signals arrive on")
	require.Equal(t, 4, cap(c), "the channel buffers the signals a burst delivers before supervise reads")
	require.NotNil(t, stop, "the seam hands back the stop that puts the handlers back")
	stop()
	stop()
}
