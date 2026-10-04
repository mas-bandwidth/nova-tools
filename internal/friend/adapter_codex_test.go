package friend

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type receiptAnswer struct {
	found bool
	next  int64
	err   error
}

func receiptCodex(c *Codex, answers ...receiptAnswer) *int {
	c.Resolve = func(_, _, session string) (string, string, error) { return session, "/rollout", nil }
	calls := 0
	c.Receipt = func(string, string, string, int64) (bool, int64, error) {
		r := answers[calls]
		calls++
		return r.found, r.next, r.err
	}
	return &calls
}

func TestCodexDeliversIntoTheOpenChatOrDefers(t *testing.T) {
	t.Parallel()
	fe := &fakeExec{out: "queued\n"}
	var record strings.Builder
	c := &Codex{Dir: "/project", Session: "thread-1", Run: fe.run, Held: func(string) bool { return true }, Out: &record}
	calls := receiptCodex(c, receiptAnswer{next: 40}, receiptAnswer{next: 40}, receiptAnswer{found: true, next: 90})
	pauses := 0
	c.Pause = func(context.Context, time.Duration) { pauses++ }
	text := "literal `text` [and] $shell"
	exit, err := c.Deliver(context.Background(), text)
	require.NoError(t, err)
	assert.Zero(t, exit)
	assert.Equal(t, 3, *calls)
	assert.Equal(t, 1, pauses)
	assert.Equal(t, [][]string{{"/project", "codex", "queue", "--thread", "thread-1", "--message", text}}, fe.calls)
	assert.Contains(t, record.String(), "received by open chat")
}

func TestCodexAcceptedOrAmbiguousQueueNeverFallsBackToResume(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		runErr  error
		readErr error
	}{
		{name: "canceled waiting"},
		{name: "command result unknown", runErr: errors.New("connection lost")},
		{name: "receipt read fails", readErr: errors.New("rollout unreadable")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithCancel(context.Background())
			fe := &fakeExec{}
			c := &Codex{Dir: "/project", Session: "thread-1", Held: func(string) bool { return true }}
			c.Run = func(ctx context.Context, dir, name string, args []string, stdin string) (string, int, error) {
				out, _, _ := fe.run(ctx, dir, name, args, stdin)
				return out, 0, tc.runErr
			}
			if tc.readErr != nil {
				receiptCodex(c, receiptAnswer{next: 10}, receiptAnswer{next: 10, err: tc.readErr})
			} else {
				receiptCodex(c, receiptAnswer{next: 10}, receiptAnswer{next: 10})
			}
			c.Pause = func(context.Context, time.Duration) { cancel() }
			exit, err := c.Deliver(ctx, "message")
			assert.Zero(t, exit)
			var deferred Deferred
			require.ErrorAs(t, err, &deferred)
			assert.Contains(t, deferred.Reason, "no alternate route")
			require.Len(t, fe.calls, 1)
			assert.Equal(t, "queue", fe.calls[0][2])
		})
	}
}

func TestCodexQueueRefusalTriesResumeAndPreservesProviderRefusal(t *testing.T) {
	t.Parallel()
	for _, provider := range []bool{false, true} {
		fe := &fakeExec{}
		calls := 0
		c := &Codex{Dir: "/project", Session: "thread-1", Held: func(string) bool { return true }}
		c.Run = func(ctx context.Context, dir, name string, args []string, stdin string) (string, int, error) {
			_, _, _ = fe.run(ctx, dir, name, args, stdin)
			calls++
			if calls == 1 {
				return "queue refused", 1, nil
			}
			if provider {
				return `{"type":"error","error":{"type":"invalid_request_error","message":"bad input"}}`, 1, nil
			}
			return "answered", 0, nil
		}
		receiptCodex(c, receiptAnswer{next: 10})
		exit, err := c.Deliver(context.Background(), "message")
		if provider {
			var refused ProviderRefused
			require.ErrorAs(t, err, &refused)
			assert.Equal(t, 1, exit)
		} else {
			require.NoError(t, err)
			assert.Zero(t, exit)
		}
		require.Len(t, fe.calls, 2)
		assert.Equal(t, "queue", fe.calls[0][2])
		assert.Equal(t, "exec", fe.calls[1][2])
	}
}

func TestCodexClosedProviderRefusalIsReturnedOnlyAfterQueueAlsoRefuses(t *testing.T) {
	t.Parallel()
	fe := &fakeExec{}
	calls := 0
	c := &Codex{Dir: "/project", Session: "thread-1", Held: func(string) bool { return false }}
	c.Run = func(ctx context.Context, dir, name string, args []string, stdin string) (string, int, error) {
		_, _, _ = fe.run(ctx, dir, name, args, stdin)
		calls++
		if calls == 1 {
			return `{"type":"error","error":{"type":"invalid_request_error","message":"bad input"}}`, 1, nil
		}
		return "queue refused", 1, nil
	}
	receiptCodex(c, receiptAnswer{next: 10})
	exit, err := c.Deliver(context.Background(), "message")
	var refused ProviderRefused
	require.ErrorAs(t, err, &refused)
	assert.Equal(t, 1, exit)
	require.Len(t, fe.calls, 2)
	assert.Equal(t, "exec", fe.calls[0][2])
	assert.Equal(t, "queue", fe.calls[1][2])
}

func TestCodexOldIdenticalProseNeverConfirmsANewQueue(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fe := &fakeExec{}
	c := &Codex{Dir: "/project", Session: "thread-1", Run: fe.run, Held: func(string) bool { return true }}
	receiptCodex(c, receiptAnswer{found: true, next: 10}, receiptAnswer{next: 20})
	c.Pause = func(context.Context, time.Duration) { cancel() }
	exit, err := c.Deliver(ctx, "ordinary repeated prose")
	assert.Zero(t, exit)
	var deferred Deferred
	require.ErrorAs(t, err, &deferred)
	assert.Contains(t, deferred.Reason, "no exact user receipt")
	assert.Len(t, fe.calls, 1, "a pre-boundary match never acknowledges this submission")
}

func TestCodexHomeIsCodexHomeThenTheUsersDotCodex(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "/elsewhere", (&Codex{Env: func(string) string { return "/elsewhere" }}).home())
	h, _ := os.UserHomeDir()
	assert.Equal(t, filepath.Join(h, ".codex"), (&Codex{Env: func(string) string { return "" }}).home())
	assert.Equal(t, "/given", (&Codex{Home: "/given"}).home())
}
