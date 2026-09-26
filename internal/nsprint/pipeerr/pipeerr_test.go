package pipeerr_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/pipeerr"
)

// TestFirstSkipsNilAndFindsTheLaterFailure is the shape that was silent: a
// pipeline whose first command answered redis.Nil (an absent field) and
// whose third command was refused. Exec reports the Nil, a caller that
// tolerates Nil reads every Val() as empty, and the refusal is never seen.
// First walks past the Nil to the refusal.
func TestFirstSkipsNilAndFindsTheLaterFailure(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	absent := redis.NewStringCmd(ctx, "hget", "k", "absent")
	absent.SetErr(redis.Nil)
	fine := redis.NewStringCmd(ctx, "hget", "k", "fine")
	fine.SetVal("v")
	refused := redis.NewStringCmd(ctx, "hget", "cfg", "checks")
	refused.SetErr(errors.New("NOPERM this user has no permissions to access one of the keys"))
	cmds := []redis.Cmder{absent, fine, refused}

	// What go-redis's Exec answers for this pipeline: the first failure, the Nil.
	err := pipeerr.First(cmds, redis.Nil)
	if err == nil || !strings.Contains(err.Error(), "NOPERM") {
		t.Fatalf("First = %v, want the NOPERM of the third command", err)
	}
	if errors.Is(err, redis.Nil) {
		t.Fatalf("First answered the Nil, the one error that means nothing: %v", err)
	}
}

// TestFirstIsNilWhenOnlyFieldsAreAbsent: a pipeline of absent fields is not
// a failure; the transport error is one when no command carries an error.
func TestFirstIsNilWhenOnlyFieldsAreAbsent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	absent := redis.NewStringCmd(ctx, "hget", "k", "absent")
	absent.SetErr(redis.Nil)
	if err := pipeerr.First([]redis.Cmder{absent}, redis.Nil); err != nil {
		t.Fatalf("First = %v, want nil for absent fields", err)
	}
	down := errors.New("dial tcp: connection refused")
	if err := pipeerr.First(nil, down); !errors.Is(err, down) {
		t.Fatalf("First = %v, want the transport error %v", err, down)
	}
}
