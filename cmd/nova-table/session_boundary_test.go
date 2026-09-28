package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"testing"

	"github.com/redis/go-redis/v9"
)

func TestShellLineLimitAndRecovery(t *testing.T) {
	t.Parallel()
	for _, end := range []string{"\n", "\r\n", ""} {
		var out, errs bytes.Buffer
		line := "#" + strings.Repeat("x", maxShellLine-1) + end
		if code := (&application{}).readCommands(strings.NewReader(line), &out, &errs, false, false); code != 0 || errs.Len() != 0 {
			t.Fatalf("exact limit with ending %q: %d %s", end, code, &errs)
		}
	}
	for _, keep := range []bool{false, true} {
		var out, errs bytes.Buffer
		input := "#" + strings.Repeat("x", maxShellLine) + "\nversion\n"
		code := (&application{}).readCommands(strings.NewReader(input), &out, &errs, keep, false)
		if code != 2 || strings.Contains(out.String(), "nova-table ") != keep || !strings.Contains(errs.String(), "line 1") {
			t.Fatalf("long line keep=%v: %d out=%q err=%q", keep, code, out.String(), errs.String())
		}
	}
}

func TestShellConnectionFailureIsUsage(t *testing.T) {
	t.Parallel()
	err := fmt.Errorf("table call: %w", &net.OpError{Op: "dial", Net: "unix", Err: os.ErrNotExist})
	var out bytes.Buffer
	if code := storeRefusal(&out, "list", err); code != 2 {
		t.Fatalf("missing socket: code=%d %s", code, &out)
	}
}

func TestShellCommandFailureNamesLine(t *testing.T) {
	t.Parallel()
	var out, errs bytes.Buffer
	code := (&application{}).readCommands(strings.NewReader("# comment\nunknown-command\n"), &out, &errs, false, false)
	if code != 2 || !strings.Contains(errs.String(), "line 2") {
		t.Fatalf("%d %s", code, &errs)
	}
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
	if err := call(ctx, commands); !errors.Is(err, want) || !c.broken.Load() {
		t.Fatalf("pipeline: %v broken=%v", err, c.broken.Load())
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
	if code != 2 || out.Len() != 0 || !strings.Contains(errs.String(), "line 1") {
		t.Fatalf("partial input: %d %s %s", code, &out, &errs)
	}
}
