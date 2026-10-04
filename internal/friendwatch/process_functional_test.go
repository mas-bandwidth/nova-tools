//go:build functional && (darwin || linux)

package friendwatch

import (
	"bufio"
	"context"
	"github.com/stretchr/testify/require"
	"io"
	"os"
	"syscall"
	"testing"
	"time"
)

// TestOwnedGroupRemovesTermIgnoringDescendant observes a real inherited file,
// not exec's managed copier pipe. Stdin EOF releases all fixture processes even
// if the regression fails, without an unrelated PID or group signal.
func TestOwnedGroupRemovesTermIgnoringDescendant(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"cancel", "complete"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			caller := syscall.Getpgrp()
			inR, inW, err := os.Pipe()
			require.NoError(t, err)
			defer inR.Close()
			defer inW.Close()
			outR, outW, err := os.Pipe()
			require.NoError(t, err)
			defer outR.Close()
			defer outW.Close()
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			defer cancel()
			lines := make(chan string, 8)
			eof := make(chan struct{})
			go func() {
				defer close(eof)
				scanner := bufio.NewScanner(outR)
				for scanner.Scan() {
					lines <- scanner.Text()
				}
			}()
			done := make(chan error, 1)
			go func() {
				done <- Run(ctx, Options{Sprint: "/usr/bin/true", Server: "unused:1", Friend: "fixture", Argv: []string{os.Args[0], "--friendwatch-test-fixture", mode}, Every: time.Second, Timeout: time.Second, Parent: os.Getppid(), Stdin: inR, Stdout: outW})
			}()
			select {
			case line := <-lines:
				require.Equal(t, "grandchild ready", line)
			case <-ctx.Done():
				t.Fatal("fixture did not become ready")
			}
			if mode == "cancel" {
				cancel()
			}
			select {
			case err := <-done:
				if mode == "complete" {
					require.NoError(t, err)
				} else {
					require.ErrorIs(t, err, context.Canceled)
				}
			case <-t.Context().Done():
				t.Fatal("owned wait did not finish")
			}
			require.NoError(t, outW.Close())
			deadline := time.NewTimer(10 * time.Second)
			defer deadline.Stop()
			select {
			case <-eof:
			case <-deadline.C:
				t.Fatal("TERM-ignoring descendant retained its inherited output pipe")
			}
			require.Equal(t, caller, syscall.Getpgrp(), "caller must stay outside the owned group")
		})
	}
}
func TestOwnedCommandKeepsNormalTermDisposition(t *testing.T) {
	t.Parallel()
	inR, inW, err := os.Pipe()
	require.NoError(t, err)
	defer inR.Close()
	defer inW.Close()
	outR, outW, err := os.Pipe()
	require.NoError(t, err)
	defer outR.Close()
	defer outW.Close()
	owned, done, err := startOwnedCommand(t.Context(), Options{Argv: []string{os.Args[0], "--friendwatch-test-fixture", "ordinary"}, Stdin: inR, Stdout: outW})
	require.NoError(t, err)
	reader := bufio.NewReader(outR)
	line, err := reader.ReadString('\n')
	require.NoError(t, err)
	require.Equal(t, "ordinary ready\n", line)
	require.NoError(t, owned.cleanup())
	require.NoError(t, outW.Close())
	rest, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.Contains(t, string(rest), "ordinary term")
	select {
	case err := <-done:
		require.NoError(t, err)
	default:
	}
}
