//go:build functional && (darwin || linux)

package friendwatch

import (
	"bufio"
	"context"
	"github.com/mas-bandwidth/nova-tools/internal/subproc"
	"github.com/stretchr/testify/require"
	"io"
	"os"
	"strconv"
	"strings"
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
			bound, finish := context.WithTimeout(t.Context(), 30*time.Second)
			defer finish()
			ctx, cancel := context.WithCancel(bound)
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
			case <-bound.Done():
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

// TestCrashedKeeperRemainsUnreapedUntilGroupCleanup checks the PID reservation
// through a real keeper death, while a TERM-ignoring member still owns stdout.
func TestCrashedKeeperRemainsUnreapedUntilGroupCleanup(t *testing.T) {
	t.Parallel()
	inR, inW, err := os.Pipe()
	require.NoError(t, err)
	defer inR.Close()
	defer inW.Close()
	outR, outW, err := os.Pipe()
	require.NoError(t, err)
	defer outR.Close()
	defer outW.Close()
	require.NoError(t, outR.SetReadDeadline(time.Now().Add(30*time.Second)))
	owned, done, err := startOwnedCommand(t.Context(), Options{Argv: []string{os.Args[0], "--friendwatch-test-fixture", "cancel"}, Stdin: inR, Stdout: outW})
	require.NoError(t, err)
	cleaned := false
	defer func() {
		if !cleaned {
			_ = owned.cleanup()
		}
	}()
	reader := bufio.NewReader(outR)
	line, err := reader.ReadString('\n')
	require.NoError(t, err)
	require.Equal(t, "grandchild ready\n", line)
	pidLine, err := reader.ReadString('\n')
	require.NoError(t, err)
	childPID, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(pidLine, "grandchild pid ")))
	require.NoError(t, err)
	require.NoError(t, owned.leader.Process.Kill(), "only the start-owned keeper handle is killed")
	select {
	case err := <-done:
		require.ErrorContains(t, err, "status failed")
	case <-time.After(30 * time.Second):
		t.Fatal("keeper EOF was not observed")
	}
	require.Nil(t, owned.leader.ProcessState, "status EOF must not reap keeper")
	group, err := syscall.Getpgid(childPID)
	require.NoError(t, err)
	require.Equal(t, owned.group, group, "surviving fixture member remains in the start-verified group")
	probe, release := subproc.CommandFor(t.Context(), 10*time.Second, "/bin/ps", "-p", strconv.Itoa(owned.leader.Process.Pid), "-o", "pid=,ppid=,stat=")
	body, err := probe.Output()
	release()
	require.NoError(t, err)
	fields := strings.Fields(string(body))
	require.Len(t, fields, 3)
	require.Equal(t, strconv.Itoa(owned.leader.Process.Pid), fields[0])
	require.Equal(t, strconv.Itoa(os.Getpid()), fields[1])
	require.Contains(t, fields[2], "Z", "keeper remains a zombie owned by this supervisor until cleanup")
	t.Logf("retained keeper: %s", strings.TrimSpace(string(body)))
	require.NoError(t, owned.cleanup())
	cleaned = true
	require.NotNil(t, owned.leader.ProcessState, "reap occurs after final signals")
	require.NoError(t, outW.Close())
	_, err = io.ReadAll(reader)
	require.NoError(t, err, "descendant must release real inherited stdout")
}

// TestOwnedCleanupLeavesOutsideSiblingAlive separates group cleanup from a
// same-caller-group sibling whose exact process handle remains test owned.
func TestOwnedCleanupLeavesOutsideSiblingAlive(t *testing.T) {
	t.Parallel()
	inputR, inputW, err := os.Pipe()
	require.NoError(t, err)
	defer inputR.Close()
	defer inputW.Close()
	outputR, outputW, err := os.Pipe()
	require.NoError(t, err)
	defer outputR.Close()
	defer outputW.Close()
	require.NoError(t, outputR.SetReadDeadline(time.Now().Add(30*time.Second)))
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	sibling := subproc.Long(ctx, os.Args[0], "--friendwatch-test-fixture", "outside")
	sibling.Stdin = inputR
	sibling.Stdout = outputW
	require.NoError(t, sibling.Start())
	waited := false
	defer func() {
		inputW.Close()
		if !waited {
			_ = sibling.Wait()
		}
	}()
	reader := bufio.NewReader(outputR)
	line, err := reader.ReadString('\n')
	require.NoError(t, err)
	require.Equal(t, "outside ready\n", line)
	group, err := syscall.Getpgid(sibling.Process.Pid)
	require.NoError(t, err)
	require.Equal(t, syscall.Getpgrp(), group)
	owned, done, err := startOwnedCommand(ctx, Options{Argv: []string{"/usr/bin/true"}})
	require.NoError(t, err)
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-ctx.Done():
		t.Fatal("owned command did not complete")
	}
	require.NoError(t, owned.cleanup())
	_, err = inputW.Write([]byte("still alive\n"))
	require.NoError(t, err)
	line, err = reader.ReadString('\n')
	require.NoError(t, err)
	require.Equal(t, "still alive\n", line)
	inputW.Close()
	require.NoError(t, sibling.Wait())
	waited = true
}
