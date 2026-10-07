//go:build functional && !windows

package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// The interrupt a command gets when nothing has replaced it is the terminal's
// (SIGINT) and a termination (SIGTERM), each of which ends its context. The
// signal is a real one, sent to this process, so the test waits for the
// system to deliver it.
func TestTheInterruptOfTheCommandIsSIGINTAndSIGTERM(t *testing.T) {
	t.Parallel()
	self, err := os.FindProcess(os.Getpid())
	require.NoError(t, err)
	// One signal at a time: a signal reaches every context listening for it.
	for _, sig := range []syscall.Signal{syscall.SIGINT, syscall.SIGTERM} {
		t.Run(sig.String(), func(t *testing.T) {
			// a listener of the test's own, so that a signal nothing catches
			// fails the test and does not end the process
			guard := make(chan os.Signal, 1)
			signal.Notify(guard, sig)
			defer signal.Stop(guard)
			ctx, stop := newApp(func(string) string { return "" }).notify(context.Background())
			defer stop()
			require.NoError(t, self.Signal(sig))
			select {
			case <-ctx.Done():
			case <-time.After(30 * time.Second):
				t.Errorf("%v did not end the context of the command", sig)
			}
		})
	}
}
