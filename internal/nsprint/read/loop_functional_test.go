//go:build functional

package read_test

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/read"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws"
	"github.com/redis/go-redis/v9"
)

func TestRunLoopDaemonCancellation(t *testing.T) {
	t.Parallel()
	_, c := newTestRedis(t)

	stream := "friends"
	ctx, cancel := context.WithCancel(context.Background())

	var out, errOut bytes.Buffer
	done := make(chan struct{})
	started := make(chan struct{}, 1)
	go func() {
		defer close(done)
		_, _ = read.RunLoop(ctx, c, read.LoopOptions{
			Stream:       stream,
			Daemon:       true,
			PollInterval: 10 * time.Millisecond,
			OnPass: func() {
				select {
				case started <- struct{}{}:
				default:
				}
			},
		}, &out, &errOut)
	}()

	select {
	case <-started:
	case <-time.After(30 * time.Second):
		t.Fatal("timed out waiting for RunLoop daemon to start")
	}
	cancel()

	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("RunLoop daemon did not exit on context cancellation")
	}

	// Verify duty card removed from working set
	if _, err := c.ZScore(context.Background(), ws.KeyAt(0, stream, ws.Working), "duty:review-loop:friends").Result(); err != redis.Nil {
		t.Errorf("expected duty card to be cleared from working on exit")
	}
}
