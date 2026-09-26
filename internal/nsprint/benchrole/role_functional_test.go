//go:build functional

package benchrole_test

import (
	"context"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/benchrole"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/redis/go-redis/v9"
)

func TestListReadsTheRoleColumn(t *testing.T) {
	t.Parallel()

	addr := testutil.Start(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = c.Close() })
	ctx := context.Background()
	if rows, err := benchrole.List(ctx, c); err != nil || len(rows) != 0 {
		t.Fatalf("empty registry: %v %v", rows, err)
	}
	c.SAdd(ctx, "benches", "studio", "hulk", "odd")
	c.HSet(ctx, "bench:studio:desired", "role", "friends", "machine", "studio", "slots", "0")
	c.HSet(ctx, "bench:hulk:desired", "machine", "hulk", "slots", "64", "legs", "go,schema")
	c.HSet(ctx, "bench:odd:desired", "role", "ci")
	rows, err := benchrole.List(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, r := range rows {
		lines = append(lines, r.Line())
	}
	want := []string{
		"BENCH hulk role=fleet machine=hulk slots=64 legs=go,schema paused=-",
		"BENCH odd role=ci machine=- slots=- legs=- paused=-",
		"BENCH studio role=friends machine=studio slots=0 legs=- paused=-",
	}
	if strings.Join(lines, "\n") != strings.Join(want, "\n") {
		t.Fatalf("rows:\n%s\nwant:\n%s", strings.Join(lines, "\n"), strings.Join(want, "\n"))
	}
	if !rows[0].Valid || rows[1].Valid || !rows[2].Valid {
		t.Fatalf("validity %v %v %v; want only odd invalid", rows[0].Valid, rows[1].Valid, rows[2].Valid)
	}
}
