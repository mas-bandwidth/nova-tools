package tablemodel

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"testing"

	"github.com/redis/go-redis/v9"
	tassert "github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The tests here reach the helpers of redis.go the unit tier had never run:
// the error wrapper, the log sink, the startup file reader, the store's
// lifecycle helpers, the connection seam and the reply normalizer. None of
// them starts a child process, opens a socket or waits on a clock.

func TestRedisCoverCannotRunUnwrapRevealsTheUnderlyingError(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("the program is not there")
	cases := []struct {
		name string
		err  error
	}{
		{name: "a refusal unwraps to what it carries", err: cannotRun(sentinel)},
		{name: "a refusal inside another error still reaches its cause", err: fmt.Errorf("while starting: %w", cannotRun(sentinel))},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tassert.ErrorIs(t, tc.err, sentinel, "errors.Is could not reach the inner error")
			var c *CannotRun
			require.ErrorAs(t, tc.err, &c, "no *CannotRun in %v", tc.err)
			got := errors.Unwrap(c)
			tassert.Equal(t, sentinel, got, "Unwrap() = %v, want the inner error", got)
		})
	}
	t.Run("an empty wrapper unwraps to nothing", func(t *testing.T) {
		got := errors.Unwrap(&CannotRun{})
		tassert.Nil(t, got, "Unwrap() of an empty wrapper = %v, want nil", got)
	})
}

func TestRedisCoverQuietPrintfKeepsSilenceWithAndWithoutArgs(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cases := []struct {
		name   string
		ctx    context.Context
		format string
		args   []any
	}{
		{name: "a bare line", ctx: context.Background(), format: "connecting"},
		{name: "a line with fields", ctx: ctx, format: "%s %s: %d errors", args: []any{"redis", "dial", 3}},
		{name: "an empty format", ctx: context.Background(), format: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := guard(func() { quiet{}.Printf(tc.ctx, tc.format, tc.args...) })
			tassert.NoError(t, err, "Printf panicked instead of keeping silence: %v", err)
		})
	}
}

func TestRedisCoverLastLineReportsTheFinalNonEmptyLineOrNothing(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cases := []struct {
		name    string
		content *string
		want    string
	}{
		{name: "the last line of several", content: ptr("one\ntwo\nthree\n"), want: "three"},
		{name: "blank lines between do not count", content: ptr("head\nbody\r\n \n\n"), want: "body"},
		{name: "a line with no newline is whole", content: ptr("solo"), want: "solo"},
		{name: "an empty file has no output", content: ptr(""), want: "no output"},
		{name: "whitespace only has no output", content: ptr("  \n\t\n"), want: "no output"},
		{name: "a line over two hundred bytes is cut at two hundred", content: ptr("a\n" + strings.Repeat("y", 250)), want: strings.Repeat("y", 200)},
		{name: "a line of exactly two hundred bytes is whole", content: ptr(strings.Repeat("z", 200)), want: strings.Repeat("z", 200)},
		{name: "a file that is not there has no output", content: nil, want: "no output"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(dir, "gone", "redis.log")
			if tc.content != nil {
				path = filepath.Join(dir, "case", "redis.log")
				require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
				require.NoError(t, os.WriteFile(path, []byte(*tc.content), 0o600))
			}
			got := lastLine(path)
			tassert.Equal(t, tc.want, got, "lastLine(%q) = %q", path, got)
		})
	}
}

func ptr(s string) *string { return &s }

func TestRedisCoverKillTakesTheExitedStoreAndDeletesItsDirectory(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		make bool
	}{
		{name: "a store directory with contents goes", make: true},
		{name: "a store directory already gone stays gone", make: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			dir := filepath.Join(root, "tablemodel-store")
			sibling := filepath.Join(root, "sibling")
			require.NoError(t, os.Mkdir(sibling, 0o700))
			if tc.make {
				require.NoError(t, os.Mkdir(dir, 0o700))
				require.NoError(t, os.WriteFile(filepath.Join(dir, "redis.log"), []byte("x"), 0o600))
			}
			exited := make(chan struct{})
			close(exited)
			s := &Server{Dir: dir, cmd: &exec.Cmd{}}
			s.kill(exited)
			tassert.NoDirExists(t, dir, "kill left the store directory")
			tassert.DirExists(t, sibling, "kill took more than the store directory")
		})
	}
}

func TestRedisCoverRemoveDeletesOnlyTheStoresOwnDirectory(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		make bool
	}{
		{name: "the store's own directory goes", make: true},
		{name: "a missing directory is nothing to remove", make: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			dir := filepath.Join(root, "tablemodel-store")
			if tc.make {
				require.NoError(t, os.MkdirAll(filepath.Join(dir, "nested"), 0o700))
			}
			(&Server{Dir: dir}).remove()
			tassert.NoDirExists(t, dir, "remove left the store directory")
			tassert.DirExists(t, root, "remove took the directory the store sits in")
		})
	}
}

func TestRedisCoverCloseInterruptsTheServerAndRemovesItsDirectory(t *testing.T) {
	t.Parallel()
	// Close signals the server's process and waits on its exit. The test hands
	// Close this process's own pid with an interrupt handler registered, so the
	// signal is caught where a child process would have caught it, and the
	// already-closed exit channel answers the wait: no child is started and no
	// clock is waited on.
	self, err := os.FindProcess(os.Getpid())
	require.NoError(t, err, "cannot find this process")
	interrupts := make(chan os.Signal, 1)
	signal.Notify(interrupts, os.Interrupt)
	defer signal.Stop(interrupts)

	cases := []struct {
		name       string
		withClient bool
	}{
		{name: "a server with no client yet"},
		{name: "a server closes its connection first", withClient: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			dir := filepath.Join(root, "tablemodel-store")
			require.NoError(t, os.Mkdir(dir, 0o700))
			require.NoError(t, os.WriteFile(filepath.Join(dir, "redis.log"), []byte("ready"), 0o600))
			exited := make(chan struct{})
			close(exited)
			s := &Server{Dir: dir, cmd: &exec.Cmd{Process: self}, exited: exited}
			if tc.withClient {
				s.client = redis.NewClient(&redis.Options{
					Network: "unix", Addr: filepath.Join(root, "never-dialed.sock"),
				})
			}
			s.Close()
			tassert.NoDirExists(t, dir, "Close left the store directory")
			tassert.DirExists(t, root, "Close took more than the store directory")
		})
	}
}

func TestRedisCoverStoreBindsTheCallersContextToTheServersClient(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		withClient bool
	}{
		{name: "a bare server hands out a context-bound store"},
		{name: "the store carries the server's client", withClient: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			s := &Server{}
			if tc.withClient {
				s.client = redis.NewClient(&redis.Options{
					Network: "unix", Addr: filepath.Join(t.TempDir(), "never-dialed.sock"),
				})
			}
			st := s.Store(ctx)
			require.NotNil(t, st, "Store returned nothing")
			tassert.Equal(t, ctx, st.ctx, "Store did not bind the caller's context")
			tassert.Equal(t, s.client, st.c, "Store did not bind the server's client")
		})
	}
}

func TestRedisCoverCmdRefusesWhenTheChecksContextIsDone(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := guard(func() { (&Store{ctx: ctx}).Cmd("GET", "key") })
	tassert.Error(t, err, "a command on a finished context ran")
	tassert.ErrorContains(t, err, "ran out of time", "error = %v, want the refusal naming the finished context", err)
}

// coverRedisError answers the redis.Error interface a store replies with;
// normalize switches on that interface, so this stands in for the store.
type coverRedisError string

func (e coverRedisError) Error() string { return string(e) }
func (e coverRedisError) RedisError()   {}

func TestRedisCoverNormalizePassesRepliesAndRefusesErrorReplies(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		in       any
		want     any
		refuse   bool
		contains string
	}{
		{name: "a status reply passes", in: "OK", want: "OK"},
		{name: "an integer reply passes", in: int64(7), want: int64(7)},
		{name: "a nil reply passes", in: nil, want: nil},
		{name: "a flat list keeps its elements", in: []any{"a", int64(1), nil}, want: []any{"a", int64(1), nil}},
		{name: "a nested list normalizes at every depth", in: []any{[]any{"x", []any{int64(2)}}}, want: []any{[]any{"x", []any{int64(2)}}}},
		{
			name:     "an error reply is a failed check",
			in:       coverRedisError("ERR unknown command"),
			refuse:   true,
			contains: "redis replied with an error: ERR unknown command",
		},
		{
			name:     "an error reply inside a list is a failed check",
			in:       []any{"a", coverRedisError("WRONGTYPE")},
			refuse:   true,
			contains: "redis replied with an error: WRONGTYPE",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.refuse {
				err := guard(func() { normalize(tc.in) })
				tassert.Error(t, err, "an error reply was normalized")
				tassert.ErrorContains(t, err, tc.contains, "error = %v", err)
				return
			}
			got := normalize(tc.in)
			tassert.Equal(t, tc.want, got, "normalize(%v) = %v", tc.in, got)
		})
	}
}
