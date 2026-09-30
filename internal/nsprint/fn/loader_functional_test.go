//go:build functional

package fn_test

import (
	"context"
	"strings"
	"testing"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/testredis"
)

func TestLoadRefusesNonDevSprintServer(t *testing.T) {
	t.Parallel()

	addr := testredis.Start(t)
	ctx := context.Background()
	c := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = c.Close() })

	const key = "prod-sprint:epoch"
	if err := c.Set(ctx, key, "1", 0).Err(); err != nil {
		t.Fatalf("set %s: %v", key, err)
	}

	err := fn.Load(ctx, c)
	if err == nil {
		t.Fatal("fn.Load succeeded on non-dev sprint server; want refusal")
	}
	want := "this server holds a sprint that is not a dev- sprint (" + key + "); nothing was loaded; load this library only on a bench server"
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("fn.Load error = %q, want it to contain %q", err.Error(), want)
	}

	libs, err := c.FunctionList(ctx, redis.FunctionListQuery{}).Result()
	if err != nil {
		t.Fatalf("FUNCTION LIST: %v", err)
	}
	if len(libs) != 0 {
		t.Fatalf("FUNCTION LIST returned %d libraries (%+v), want none loaded", len(libs), libs)
	}

	errMissing := fn.LoadMissing(ctx, c)
	if errMissing == nil {
		t.Fatal("fn.LoadMissing succeeded on non-dev sprint server; want refusal")
	}
	if !strings.Contains(errMissing.Error(), want) {
		t.Fatalf("fn.LoadMissing error = %q, want it to contain %q", errMissing.Error(), want)
	}

	libs, err = c.FunctionList(ctx, redis.FunctionListQuery{}).Result()
	if err != nil {
		t.Fatalf("FUNCTION LIST: %v", err)
	}
	if len(libs) != 0 {
		t.Fatalf("FUNCTION LIST returned %d libraries (%+v), want none loaded", len(libs), libs)
	}
}
