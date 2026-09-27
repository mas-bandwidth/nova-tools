package wake

import (
	"context"
	"fmt"
	"net"
	"reflect"
	"testing"

	"github.com/redis/go-redis/v9"
)

// Check the established key independently of the reader's constant. The hook
// answers Redis commands in memory, with no connection, clock or listener.
// Command arguments pin the stream, cursor, count and nonblocking behavior.
func TestEvGithubReadsTheEstablishedStream(t *testing.T) {
	t.Parallel()
	e := OpenEvGithub("unused.invalid:1", "", "")
	message := func(id string) redis.XMessage {
		return redis.XMessage{ID: id, Values: map[string]any{
			"repo": " o/r ", "number": " 17 ", "kind": " check_run ",
			"action": " completed ", "head": " abc ", "sender": " reader ", "at": " timestamp ",
		}}
	}
	steps := []evGithubReply{
		{args: []any{"xrevrange", "ev:github", "+", "-", "count", int64(1)}},
		{args: []any{"xrevrange", "ev:github", "+", "-", "count", int64(1)}, messages: []redis.XMessage{message("2-0")}},
		{args: []any{"xread", "count", int64(1), "streams", "ev:github", "0-0"}, streams: []redis.XStream{{Stream: "ev:github", Messages: []redis.XMessage{message("1-0")}}}},
		{args: []any{"xread", "count", int64(1), "streams", "ev:github", "1-0"}, streams: []redis.XStream{{Stream: "ev:github", Messages: []redis.XMessage{message("2-0")}}}},
		{args: []any{"xread", "count", int64(1), "streams", "ev:github", "2-0"}, err: redis.Nil},
	}
	hook := &evGithubHook{steps: steps}
	e.rdb.AddHook(hook)
	t.Cleanup(func() {
		if len(hook.steps) != 0 {
			t.Errorf("%d expected Redis reads were omitted", len(hook.steps))
		}
	})
	t.Cleanup(func() {
		if err := e.Close(); err != nil {
			t.Error(err)
		}
	})
	ctx := context.Background()
	if tip, err := e.Tip(ctx); err != nil || tip != "0-0" {
		t.Fatalf("empty tip = %q, %v", tip, err)
	}
	if tip, err := e.Tip(ctx); err != nil || tip != "2-0" {
		t.Fatalf("populated tip = %q, %v", tip, err)
	}
	cursor := "0-0"
	for _, id := range []string{"1-0", "2-0"} {
		got, err := e.Read(ctx, cursor, 1, -1) // no BLOCK and no wall-clock wait
		want := []Event{{ID: id, Repo: "o/r", Number: "17", Kind: "check_run", Action: "completed", Head: "abc", Sender: "reader", At: "timestamp"}}
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("after %s = %#v, %v; want %#v", cursor, got, err, want)
		}
		cursor = id
	}
	if got, err := e.Read(ctx, cursor, 1, -1); err != nil || len(got) != 0 {
		t.Fatalf("drained = %#v, %v", got, err)
	}
}

// Each command consumes one independently specified response. Nothing forwards
// to the Redis transport; an unexpected dial or pipeline fails immediately.
type evGithubReply struct {
	args     []any
	messages []redis.XMessage
	streams  []redis.XStream
	err      error
}
type evGithubHook struct{ steps []evGithubReply }

func (h *evGithubHook) DialHook(redis.DialHook) redis.DialHook {
	return func(context.Context, string, string) (net.Conn, error) {
		return nil, fmt.Errorf("unexpected Redis dial in unit test")
	}
}
func (h *evGithubHook) ProcessHook(redis.ProcessHook) redis.ProcessHook {
	return func(_ context.Context, cmd redis.Cmder) error {
		if len(h.steps) == 0 {
			return fmt.Errorf("unexpected Redis command %v", cmd.Args())
		}
		want := h.steps[0]
		if !reflect.DeepEqual(cmd.Args(), want.args) {
			return fmt.Errorf("Redis command %v, want %v", cmd.Args(), want.args)
		}
		h.steps = h.steps[1:]
		switch c := cmd.(type) {
		case *redis.XMessageSliceCmd:
			c.SetVal(want.messages)
		case *redis.XStreamSliceCmd:
			c.SetVal(want.streams)
		default:
			return fmt.Errorf("unexpected Redis command type %T", cmd)
		}
		return want.err
	}
}
func (h *evGithubHook) ProcessPipelineHook(redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(context.Context, []redis.Cmder) error {
		return fmt.Errorf("unexpected Redis pipeline in unit test")
	}
}
