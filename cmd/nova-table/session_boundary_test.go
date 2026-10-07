package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"io"
	"net"
	"os"
	"strings"
	"testing"
)

func TestShellLineLimitAndRecovery(t *testing.T) {
	t.Parallel()
	for _, end := range []string{"\n", "\r\n", ""} {
		var out, errs bytes.Buffer
		line := "#" + strings.Repeat("x", maxShellLine-1) + end
		{
			code := (&application{}).readCommands(strings.NewReader(line), &out, &errs, false, false)
			require.EqualValues(t, 0, code, "exact limit with ending %q: %d %s", end, code, &errs)
			require.EqualValues(t, 0, errs.Len(), "exact limit with ending %q: %d %s", end, code, &errs)
		}
	}
	for _, keep := range []bool{false, true} {
		var out, errs bytes.Buffer
		input := "#" + strings.Repeat("x", maxShellLine) + "\nversion\n"
		code := (&application{}).readCommands(strings.NewReader(input), &out, &errs, keep, false)
		require.EqualValues(t, 2, code, "long line keep=%v: %d out=%q err=%q", keep, code, out.String(), errs.String())
		require.Equal(t, keep, strings.Contains(out.String(), "nova-table "), "long line keep=%v: %d out=%q err=%q", keep, code, out.String(), errs.String())
		require.Contains(t, errs.String(), "line 1", "long line keep=%v: %d out=%q err=%q", keep, code, out.String(), errs.String())
	}
}

func TestShellConnectionFailureIsUsage(t *testing.T) {
	t.Parallel()
	err := fmt.Errorf("table call: %w", &net.OpError{Op: "dial", Net: "unix", Err: os.ErrNotExist})
	var out bytes.Buffer
	{
		code := (&connection{}).refusal(&out, "list", err)
		require.EqualValues(t, 2, code, "missing socket: code=%d %s", code, &out)
	}
}

func TestShellCommandFailureNamesLine(t *testing.T) {
	t.Parallel()
	var out, errs bytes.Buffer
	code := (&application{}).readCommands(strings.NewReader("# comment\nunknown-command\n"), &out, &errs, false, false)
	require.EqualValues(t, 2, code, "%d %s", code, &errs)
	require.Contains(t, errs.String(), "line 2", "%d %s", code, &errs)
}

func TestShellPipelineFailureAfterLogicalRefusalBreaksConnection(t *testing.T) {
	t.Parallel()
	c := &connection{}
	ctx := context.Background()
	commands := []redis.Cmder{redis.NewCmd(ctx, "read-one"), redis.NewCmd(ctx, "read-two")}
	want := errors.New("logical refusal")
	call := c.ProcessPipelineHook(func(_ context.Context, cmds []redis.Cmder) error {
		cmds[0].SetErr(want)
		cmds[1].SetErr(&net.OpError{Op: "read", Net: "tcp", Err: io.EOF})
		return want
	})
	{
		err := call(ctx, commands)
		require.ErrorIs(t, err, want, "pipeline: %v broken=%v", err, c.broken.Load())
		require.True(t, c.broken.Load(), "pipeline: %v broken=%v", err, c.broken.Load())
	}
}

type shellPartialReadError struct{}

func (shellPartialReadError) Read(p []byte) (int, error) {
	return copy(p, "version"), errors.New("failed after partial input")
}

func TestShellDoesNotRunPartialFailedRead(t *testing.T) {
	t.Parallel()
	var out, errs bytes.Buffer
	code := (&application{}).readCommands(shellPartialReadError{}, &out, &errs, true, false)
	require.EqualValues(t, 2, code, "partial input: %d %s %s", code, &out, &errs)
	require.EqualValues(t, 0, out.Len(), "partial input: %d %s %s", code, &out, &errs)
	require.Contains(t, errs.String(), "line 1", "partial input: %d %s %s", code, &out, &errs)
}
