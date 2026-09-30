package redisconn

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/redis/go-redis/v9"
)

// command is a pipeline's command as it stands after Exec: with the error
// given, or with a value when the error is nil.
func command(err error) redis.Cmder {
	cmd := redis.NewStringCmd(context.Background(), "hget", "k", "f")
	if err != nil {
		cmd.SetErr(err)
	} else {
		cmd.SetVal("v")
	}
	return cmd
}

// TestFirstError: the first error that is not an absent value, in the order
// the commands were queued; the transport's error when no command carries
// one; nil when every command succeeded or found nothing.
func TestFirstError(t *testing.T) {
	t.Parallel()
	noperm := errors.New("NOPERM this user has no permissions to access one of the keys")
	wrongtype := errors.New("WRONGTYPE Operation against a key holding the wrong kind of value")
	down := errors.New("dial tcp: connection refused")
	for _, c := range []struct {
		name    string
		cmds    []redis.Cmder
		execErr error
		want    error
	}{
		{"nothing queued, nothing wrong", nil, nil, nil},
		{"every command succeeded", []redis.Cmder{command(nil), command(nil)}, nil, nil},
		{"only absent values, as Exec reports them", []redis.Cmder{command(redis.Nil), command(nil), command(redis.Nil)}, redis.Nil, nil},
		// The shape that was silent: Exec answers the Nil of the first
		// command, and the refusal of the third is never seen.
		{"an absent value and then a refusal", []redis.Cmder{command(redis.Nil), command(nil), command(noperm)}, redis.Nil, noperm},
		{"two refusals: the earlier", []redis.Cmder{command(nil), command(wrongtype), command(noperm)}, wrongtype, wrongtype},
		{"a refusal that Exec did not report", []redis.Cmder{command(nil), command(noperm)}, nil, noperm},
		{"no command carries an error: the transport's", nil, down, down},
		{"no command carries an error but absent values: the transport's", []redis.Cmder{command(redis.Nil)}, down, down},
		{"a command's refusal comes before the transport's", []redis.Cmder{command(noperm)}, down, noperm},
		{"an absent value, wrapped", []redis.Cmder{command(wrapped{redis.Nil})}, wrapped{redis.Nil}, nil},
	} {
		if got := FirstError(c.cmds, c.execErr); got != c.want {
			t.Errorf("%s: FirstError = %v; want %v", c.name, got, c.want)
		}
	}
}

type wrapped struct{ error }

func (w wrapped) Unwrap() error { return w.error }

// TestExec: a pipeline run through Exec is one round trip, its absent values
// are not an error, a refusal behind an absent value is, and every command
// keeps its own result.
func TestExec(t *testing.T) {
	t.Parallel()
	conn, store := opened(t, func(conn int, cmd []string) string {
		if cmd[0] == "get" && cmd[1] == "drop" {
			return hangUp
		}
		return accepting(conn, cmd)
	})
	trips := CountTrips(conn.Client())
	ctx := context.Background()

	pipe := conn.Client().Pipeline()
	absent, present := pipe.Get(ctx, "absent"), pipe.Get(ctx, "key")
	if err := Exec(ctx, pipe); err != nil {
		t.Errorf("a pipeline with an absent value: %v; want nil", err)
	}
	if !errors.Is(absent.Err(), redis.Nil) || present.Val() != "value" {
		t.Errorf("the commands read %v and %q; want the absent value and the value", absent.Err(), present.Val())
	}

	pipe = conn.Client().Pipeline()
	absent, refused := pipe.Get(ctx, "absent"), pipe.LPush(ctx, "key", "v")
	present = pipe.Get(ctx, "key")
	if _, bare := pipe.Exec(ctx); !errors.Is(bare, redis.Nil) {
		t.Fatalf("a bare Exec answered %v; this test is of the pipeline whose first failure is an absent value", bare)
	}
	pipe.Get(ctx, "absent")
	pipe.LPush(ctx, "key", "v")
	err := Exec(ctx, pipe)
	if err == nil || !strings.HasPrefix(err.Error(), "WRONGTYPE") {
		t.Errorf("a pipeline with a refusal behind an absent value: %v; want the refusal", err)
	}
	if !errors.Is(absent.Err(), redis.Nil) || refused.Err() == nil || present.Val() != "value" {
		t.Errorf("the commands read %v, %v and %q", absent.Err(), refused.Err(), present.Val())
	}

	pipe = conn.Client().Pipeline()
	pipe.Get(ctx, "drop")
	pipe.Get(ctx, "key")
	if err := Exec(ctx, pipe); !errors.Is(err, io.EOF) || Classify(err) != Unconfirmed {
		t.Errorf("a pipeline the store dropped: %v; want the end of the stream", err)
	}
	if err := Exec(ctx, conn.Client().Pipeline()); err != nil {
		t.Errorf("a pipeline with nothing in it: %v", err)
	}
	if trips.N() != 4 {
		t.Errorf("four pipelines took %d trips; want 4", trips.N())
	}
	want := []string{"1: get absent", "1: get key", "1: get absent", "1: lpush key v", "1: get key", "1: get absent", "1: lpush key v", "1: get drop"}
	if got := store.commands(); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("the store received %q; want %q", got, want)
	}
}
