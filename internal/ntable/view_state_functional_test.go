//go:build functional

package ntable_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/redis/go-redis/v9"
)

// TestAViewsStateIsItsSummaryLineAlone: ns_view_state sets and clears a view's
// state; while set, the summary line is the state alone; a view set leaves it
// as it is; a view that is not there is refused ErrNoView and nothing is
// written; a state that is not one short line is refused by the store; and the
// state can be written in the same MULTI/EXEC as a record of the caller's.
func TestAViewsStateIsItsSummaryLineAlone(t *testing.T) {
	t.Parallel()
	_, c := live(t)
	ctx := context.Background()
	if err := ntable.Create(ctx, c, demo(), now); err != nil {
		t.Fatal(err)
	}
	if _, err := ntable.RowAdd(ctx, c, "demo", "build", ntable.RowSpec{}); err != nil {
		t.Fatal(err)
	}
	if _, err := ntable.CellAdd(ctx, c, "demo", "build", "done", "a", 1); err != nil {
		t.Fatal(err)
	}
	if _, err := ntable.CellAdd(ctx, c, "demo", "build", "ready", "b", 1); err != nil {
		t.Fatal(err)
	}
	def := ntable.View{Name: "v", Tables: []string{"demo"}, Title: "T", Summary: "done"}
	if err := ntable.ViewSet(ctx, c, def); err != nil {
		t.Fatal(err)
	}
	line := func() string {
		t.Helper()
		v, err := ntable.ViewGet(ctx, c, "v")
		if err != nil {
			t.Fatal(err)
		}
		tb, err := ntable.Read(ctx, c, v.Tables[0])
		if err != nil {
			t.Fatal(err)
		}
		return ntable.SummaryLine(v, tb)
	}
	if got := line(); got != "1/2 50.0% -> ETA" {
		t.Fatalf("no state: %q", got)
	}
	if err := ntable.ViewState(ctx, c, "v", "STOPPED"); err != nil {
		t.Fatal(err)
	}
	if got := line(); got != "STOPPED" {
		t.Fatalf("with a state: %q, want STOPPED alone", got)
	}
	if err := ntable.ViewSet(ctx, c, def); err != nil {
		t.Fatal(err)
	}
	if got := line(); got != "STOPPED" {
		t.Fatalf("a view set dropped the state: %q", got)
	}
	if err := ntable.ViewState(ctx, c, "v", ""); err != nil {
		t.Fatal(err)
	}
	if got := line(); got != "1/2 50.0% -> ETA" {
		t.Fatalf("cleared: %q", got)
	}
	if n, err := c.HExists(ctx, "view:v", "state").Result(); err != nil || n {
		t.Fatalf("a cleared state is still stored: %v %v", n, err)
	}

	// A view that is not there: refused, and nothing is created.
	if err := ntable.ViewState(ctx, c, "gone", "STOPPED"); !errors.Is(err, ntable.ErrNoView) {
		t.Fatalf("missing view: %v", err)
	}
	if n, err := c.Exists(ctx, "view:gone").Result(); err != nil || n != 0 {
		t.Fatalf("a refused state created the view: %d %v", n, err)
	}
	// The store refuses a state that is not one short line, whoever calls.
	for _, bad := range []string{"two\nlines", strings.Repeat("x", ntable.MaxViewState+1)} {
		reply, err := c.FCall(ctx, "ns_view_state", []string{"view:v"}, "v", bad).Slice()
		if err != nil || len(reply) < 2 || reply[0] != "REFUSED" || reply[1] != "ARGS" {
			t.Fatalf("state %q: %v %v", bad, reply, err)
		}
		if ntable.ValidViewState(bad) {
			t.Fatalf("state %q passes the client's check", bad)
		}
	}

	// In one transaction with a record of the caller's: both are written.
	var shown *redis.Cmd
	if _, err := c.TxPipelined(ctx, func(p redis.Pipeliner) error {
		p.Set(ctx, "record", "stopped", 0)
		shown = ntable.QueueViewState(ctx, p, "v", "STOPPED")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := ntable.ViewStateResult("v", shown); err != nil {
		t.Fatal(err)
	}
	if got := line(); got != "STOPPED" || c.Get(ctx, "record").Val() != "stopped" {
		t.Fatalf("transaction: line %q record %q", got, c.Get(ctx, "record").Val())
	}
	var missing *redis.Cmd
	if _, err := c.TxPipelined(ctx, func(p redis.Pipeliner) error {
		missing = ntable.QueueViewState(ctx, p, "gone", "STOPPED")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := ntable.ViewStateResult("gone", missing); !errors.Is(err, ntable.ErrNoView) {
		t.Fatalf("queued on a missing view: %v", err)
	}
}
