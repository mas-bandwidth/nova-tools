package task_test

import (
	"context"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/task"
	"github.com/redis/go-redis/v9"
)

// takeReadFake answers TakeAvailable's one read pipeline without a server:
// the friend is a member with 4 desired slots, and the working count comes
// back as working (a reply that is not a count when it is a string). Any
// other pipeline is refused, so a take that went on to claim fails loudly.
type takeReadFake struct{ working any }

func (takeReadFake) DialHook(next redis.DialHook) redis.DialHook { return next }

func (takeReadFake) ProcessHook(next redis.ProcessHook) redis.ProcessHook { return next }

func (f takeReadFake) ProcessPipelineHook(redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(_ context.Context, cmds []redis.Cmder) error {
		for _, c := range cmds {
			switch cmd := c.(type) {
			case *redis.BoolCmd:
				cmd.SetVal(true)
			case *redis.StringCmd:
				cmd.SetVal("4")
			case *redis.Cmd:
				if len(cmd.Args()) > 1 && cmd.Args()[1] == "ns_cell_zcard" {
					cmd.SetVal(f.working)
				} else {
					cmd.SetVal([]any{})
				}
			default:
				c.SetErr(redis.ErrClosed)
			}
		}
		return nil
	}
}

// TestTakeAvailableFailsOnAnUnreadWorkingCount (nova-tools#4238, item 6 of the
// #4377 read): a working count the take could not read is an error naming the
// friend, never 0 (read as 0 the friend's every slot is free and the take
// claims past them); a count that reads takes nothing when the slots are full.
func TestTakeAvailableFailsOnAnUnreadWorkingCount(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		working any
		want    string
	}{
		{"junk", "task take: friend f1 working:"},
		{nil, "task take: friend f1 working:"},
	} {
		client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
		client.AddHook(takeReadFake{working: c.working})
		claims, err := task.TakeAvailable(context.Background(), store.New(client), "f1", "s1", "", 0, "f1", "")
		_ = client.Close()
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Fatalf("working reply %#v: claims=%v err=%v, want %q", c.working, claims, err, c.want)
		}
	}
	client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	defer client.Close()
	client.AddHook(takeReadFake{working: int64(4)})
	claims, err := task.TakeAvailable(context.Background(), store.New(client), "f1", "s1", "", 0, "f1", "")
	if err != nil || len(claims) != 0 {
		t.Fatalf("a full friend: claims=%v err=%v, want none and no error", claims, err)
	}
}
