//go:build functional

package store_test

import (
	"context"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws"
	"github.com/redis/go-redis/v9"
)

// TestACLSeatsReadTheSprintEpoch (nova-tools#4238, item 7 of the #4377 read):
// every seat whose verbs key a table set by the epoch reads sprint:epoch n
// under its own ACL user (ns-consumer, ns-table, ns-coordinator, ns-friend),
// the friend seat counts its working set through ns_cell_zcard (task take and
// width) and the coordinator's lander reads a stream's members through
// ns_ws_zrange. The table seat without its sprint:epoch grant is refused, so
// the control can fail.
func TestACLSeatsReadTheSprintEpoch(t *testing.T) {
	t.Parallel()
	rules := map[string]string{}
	for _, rule := range store.ACLRules {
		name, body, _ := strings.Cut(rule, " ")
		rules[name] = body
	}
	var bare []string
	for _, tok := range strings.Split(rules["ns-table"], " ") {
		if tok != "%R~"+ws.EpochKey {
			bare = append(bare, tok)
		}
	}
	users := map[string][]string{"ns-table-bare": bare}
	for _, name := range []string{"ns-deploy", "ns-consumer", "ns-table", "ns-coordinator", "ns-friend"} {
		if rules[name] == "" {
			t.Fatalf("store.ACLRules has no %s", name)
		}
		users[name] = strings.Split(rules[name], " ")
	}
	var extra []string
	for name, toks := range users {
		extra = append(extra, "--user", name, "on", ">"+name+"-pass")
		extra = append(extra, toks...)
	}
	addr := testutil.Start(t, extra...)
	ctx := context.Background()
	as := func(name string) *redis.Client {
		c := redis.NewClient(&redis.Options{Addr: addr, Username: name, Password: name + "-pass"})
		t.Cleanup(func() { _ = c.Close() })
		return c
	}
	if err := fn.Load(ctx, as("ns-deploy")); err != nil {
		t.Fatalf("ns-deploy loads the library: %v", err)
	}
	admin := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = admin.Close() })
	// one clear has run: the current sets are epoch 1's
	admin.HSet(ctx, ws.EpochKey, ws.EpochField, "1")
	admin.ZAdd(ctx, ws.ConsumerKeyAt(1, "friend:f", "working"), redis.Z{Score: 1, Member: "a"}, redis.Z{Score: 2, Member: "b"})
	admin.ZAdd(ctx, ws.ConsumerKeyAt(0, "friend:f", "working"), redis.Z{Score: 1, Member: "old"})
	admin.ZAdd(ctx, ws.KeyAt(1, "s", "merging"), redis.Z{Score: 1, Member: "t1"})

	for _, seat := range []string{"ns-consumer", "ns-table", "ns-coordinator", "ns-friend"} {
		e, err := ws.Epoch(ctx, as(seat))
		if err != nil || e != 1 {
			t.Errorf("%s reads sprint:epoch n: %d %v, want 1", seat, e, err)
		}
	}
	if n, err := ws.CellCard(ctx, as("ns-friend"), "friend:f", "working").Int64(); err != nil || n != 2 {
		t.Errorf("ns-friend ns_cell_zcard friend:f working = %d %v, want 2 (epoch 1's set)", n, err)
	}
	if ids, err := ws.IDs(ws.StreamRange(ctx, as("ns-coordinator"), "s", "merging")); err != nil || len(ids) != 1 || ids[0] != "t1" {
		t.Errorf("ns-coordinator ns_ws_zrange s merging = %v %v, want [t1]", ids, err)
	}
	if _, err := ws.Epoch(ctx, as("ns-table-bare")); err == nil || !strings.Contains(err.Error(), "NOPERM") {
		t.Errorf("ns-table without %%R~%s read it: %v", ws.EpochKey, err)
	}
}
