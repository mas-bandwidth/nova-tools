package gh

import (
	"context"
	"fmt"
	"go/parser"
	"go/token"
	"net"
	"reflect"
	"strings"
	"testing"

	"github.com/redis/go-redis/v9"
)

// TestGhDoesNotImportGhevent ensures that the gh reader depends only on the
// wire stream contract, never compiling the webhook decoder or ingestion logic.
func TestGhDoesNotImportGhevent(t *testing.T) {
	t.Parallel()
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", nil, parser.ImportsOnly)
	if err != nil {
		t.Fatal(err)
	}
	for _, pkg := range pkgs {
		for filename, file := range pkg.Files {
			for _, imp := range file.Imports {
				path := strings.Trim(imp.Path.Value, `"`)
				if path == "github.com/mas-bandwidth/nova-tools/internal/ghevent" {
					t.Fatalf("file %s imports %s; gh must not depend on internal/ghevent", filename, path)
				}
			}
		}
	}
}

// Check the established key independently of the reader's constant. The hook
// answers Redis commands in memory, with no connection, clock or listener.
// Command arguments pin the stream, cursor, count and nonblocking behavior.
func TestAwaitReadsTheEstablishedStream(t *testing.T) {
	t.Parallel()
	rdb := redis.NewClient(&redis.Options{Addr: "unused.invalid:1"})
	t.Cleanup(func() { _ = rdb.Close() })

	message := func(id, kind, repo, number, head string) redis.XMessage {
		return redis.XMessage{ID: id, Values: map[string]any{
			"repo": repo, "number": number, "kind": kind,
			"action": "completed", "head": head, "sender": "reader", "at": "timestamp",
		}}
	}

	steps := []awaitReply{
		// Tip on empty stream
		{args: []any{"xrevrange", "ev:github", "+", "-", "count", int64(1)}},
		// Tip on populated stream
		{args: []any{"xrevrange", "ev:github", "+", "-", "count", int64(1)}, messages: []redis.XMessage{message("2-0", "check_run", "o/r", "1", "abc")}},
		// Await read from 0-0 with miss
		{
			args: []any{"xread", "count", int64(100), "streams", "ev:github", "0-0"},
			streams: []redis.XStream{{
				Stream:   "ev:github",
				Messages: []redis.XMessage{message("1-0", "check_run", "o/r", "1", "other")},
			}},
		},
		// Await read from 1-0 with hit
		{
			args: []any{"xread", "count", int64(100), "streams", "ev:github", "1-0"},
			streams: []redis.XStream{{
				Stream:   "ev:github",
				Messages: []redis.XMessage{message("2-0", "check_run", "o/r", "1", "target")},
			}},
		},
		// Await read drained
		{
			args: []any{"xread", "count", int64(100), "streams", "ev:github", "2-0"},
			err:  redis.Nil,
		},
	}
	hook := &awaitHook{steps: steps}
	rdb.AddHook(hook)
	t.Cleanup(func() {
		if len(hook.steps) != 0 {
			t.Errorf("%d expected Redis reads were omitted", len(hook.steps))
		}
	})

	ctx := context.Background()
	tip, err := Tip(ctx, rdb)
	if err != nil || tip != "0-0" {
		t.Fatalf("empty tip = %q, %v", tip, err)
	}
	tip, err = Tip(ctx, rdb)
	if err != nil || tip != "2-0" {
		t.Fatalf("populated tip = %q, %v", tip, err)
	}

	// Miss on "target"
	tip, hit, err := Await(ctx, rdb, "0-0", 0, HeadEvent("target"))
	if err != nil || hit || tip != "1-0" {
		t.Fatalf("miss = %q, %v, %v; want 1-0, false, nil", tip, hit, err)
	}

	// Hit on "target"
	tip, hit, err = Await(ctx, rdb, tip, 0, HeadEvent("target"))
	if err != nil || !hit || tip != "2-0" {
		t.Fatalf("hit = %q, %v, %v; want 2-0, true, nil", tip, hit, err)
	}

	// Drained
	tip, hit, err = Await(ctx, rdb, tip, 0, HeadEvent("target"))
	if err != nil || hit || tip != "2-0" {
		t.Fatalf("drained = %q, %v, %v; want 2-0, false, nil", tip, hit, err)
	}
}

type awaitReply struct {
	args     []any
	messages []redis.XMessage
	streams  []redis.XStream
	err      error
}
type awaitHook struct{ steps []awaitReply }

func (h *awaitHook) DialHook(redis.DialHook) redis.DialHook {
	return func(context.Context, string, string) (net.Conn, error) {
		return nil, fmt.Errorf("unexpected Redis dial in unit test")
	}
}
func (h *awaitHook) ProcessHook(redis.ProcessHook) redis.ProcessHook {
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

func (h *awaitHook) ProcessPipelineHook(redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(context.Context, []redis.Cmder) error {
		return fmt.Errorf("unexpected Redis pipeline in unit test")
	}
}
