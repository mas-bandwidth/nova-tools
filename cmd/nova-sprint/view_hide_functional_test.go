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

// The stored view shows a stream with no cards in any column: after init, an
// add of three streams and a clear, the drawn view still has the work and merge
// tables with their three stream rows at zero, and the readers and the fleet. The
// frame is the one `nova-table watch --view sprint` draws (ntable.RenderTables).
func TestTheViewShowsAStreamWithNoCards(t *testing.T) {
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
	a.newPath = false // the present path's view, behind the switch
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
		return ntable.RenderTables("", tables, ntable.RenderOpts{})
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
	for _, table := range []string{"work", "merge", "readers", "fleet"} {
		if !has(got, table) {
			t.Fatalf("after a clear the %s table is drawn:\n%s", table, got)
		}
	}
	for _, stream := range []string{"\na ", "\nb ", "\nc "} {
		if strings.Count(got, stream) < 2 {
			t.Fatalf("after a clear the stream row %q is in the work and merge tables, at zero:\n%s", stream, got)
		}
	}
	run("add", "--stream", "b", "--count", "1")
	got = frame()
	if !has(got, "work") || !strings.Contains(got, "\na ") || !strings.Contains(got, "\nb ") || !strings.Contains(got, "\nc ") {
		t.Fatalf("every stream is drawn, with a card or none:\n%s", got)
	}
}
