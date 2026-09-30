//go:build functional

package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/redis/go-redis/v9"
)

// The stored view hides a stream with no cards in any column from the work and
// merge tables, as docs/SPEC-SPRINT.md says of the view: after init, an add of
// three streams and a clear, the drawn view has no work table and no merge
// table and still has the readers and the fleet; a stream that has a card is
// drawn again. The frame is the one `nova-table watch --view sprint` draws
// (ntable.RenderTables over the view's own HideZero).
func TestTheViewHidesAStreamWithNoCardsAndKeepsReadersAndFleet(t *testing.T) {
	t.Parallel()
	addr := testutil.Start(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	defer c.Close()
	ctx := context.Background()
	if err := fn.Load(ctx, c); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"NOVA_SPRINT_REDIS": addr, "NOVA_SPRINT_ACTOR": "coordinator"}
	a := newApp(func(k string) string { return env[k] })
	defer a.close()
	run := func(args ...string) {
		t.Helper()
		var out, errb bytes.Buffer
		if code := a.run(args, &out, &errb); code != 0 {
			t.Fatalf("%v: %d %s", args, code, errb.String())
		}
	}
	frame := func() string {
		t.Helper()
		v, err := ntable.ViewGet(ctx, c, "sprint")
		if err != nil {
			t.Fatal(err)
		}
		var tables []ntable.Table
		for _, name := range v.Tables {
			tb, err := ntable.Read(ctx, c, name)
			if err != nil {
				t.Fatal(err)
			}
			tables = append(tables, tb)
		}
		return ntable.RenderTables("", tables, ntable.RenderOpts{}, v.HideZero)
	}
	has := func(text, table string) bool {
		for _, line := range strings.Split(text, "\n") {
			if strings.HasPrefix(line, table+" ") || line == table {
				return true
			}
		}
		return false
	}

	run("init", "--readers", "reader-a,reader-b", "--members", "m1")
	run("add", "--stream", "a,b,c", "--count", "1")
	if got := frame(); !has(got, "work") || !strings.Contains(got, "\na ") {
		t.Fatalf("streams with a card each: the work table is drawn:\n%s", got)
	}
	run("clear", "--confirm", "sprint")
	got := frame()
	if has(got, "work") || has(got, "merge") {
		t.Fatalf("after a clear, no stream has a card: the work and merge tables are not drawn:\n%s", got)
	}
	if !has(got, "readers") || !has(got, "fleet") {
		t.Fatalf("readers and fleet keep their rows after a clear:\n%s", got)
	}
	run("add", "--stream", "b", "--count", "1")
	got = frame()
	if !has(got, "work") || strings.Contains(got, "\na ") || !strings.Contains(got, "\nb ") {
		t.Fatalf("one stream with a card is drawn, the others are not:\n%s", got)
	}
}
