//go:build functional

package ws_test

import (
	"context"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws/wstest"
)

// TestStreamNameWithAnEpochSegmentIsRefused (nova-tools#4238, item 8 of the
// #4377 read): a stream name that starts with digits and a colon would read
// as the epoch segment of the set names (ws:1:x:ready is stream `1:x` at
// epoch 0 and stream `x` at epoch 1), so a push into one and a move into one
// are refused by name, with the reason; digits elsewhere in the name are a
// stream like any other.
func TestStreamNameWithAnEpochSegmentIsRefused(t *testing.T) {
	t.Parallel()

	_, c := wstest.Start(t)
	ctx := context.Background()
	const why = "a leading <digits>: is the sprint epoch segment of the set names"
	for _, s := range []string{"1:x", "42: cards"} {
		_, err := taskcard.Push(ctx, c, taskcard.PushRequest{ID: "E-" + s[:1], Where: "waiting", Stream: s, By: "test", Title: "t"})
		if err == nil || !strings.Contains(err.Error(), "STREAM bad name "+s+": "+why) {
			t.Fatalf("push into stream %q: %v, want the epoch-segment refusal", s, err)
		}
	}
	if n, _ := c.Exists(ctx, "task:E-1", "task:E-4").Result(); n != 0 {
		t.Fatal("a refused push wrote a record")
	}
	if _, err := taskcard.Push(ctx, c, taskcard.PushRequest{ID: "F", Where: "waiting", Stream: "x:1", By: "test", Title: "t"}); err != nil {
		t.Fatalf("push into stream x:1: %v", err)
	}
	_, err := taskcard.Move(ctx, c, "F", "waiting", taskcard.Opts{By: "test", Stream: "7:y", SetStream: true})
	if err == nil || !strings.Contains(err.Error(), "STREAM bad name 7:y: "+why) {
		t.Fatalf("move into stream 7:y: %v, want the epoch-segment refusal", err)
	}
	if _, err := c.ZScore(ctx, ws.KeyAt(0, "x:1", "waiting"), "F").Result(); err != nil {
		t.Fatalf("F left stream x:1's waiting set on a refused move: %v", err)
	}
}
