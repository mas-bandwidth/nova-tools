//go:build functional

package store_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws"
	"github.com/redis/go-redis/v9"
)

// TestACLSeatsWriteTheWorkOrder is the #4322 fix round's grant (item 3):
// the three seats whose doors write a stream's work order, ns-coordinator
// (task push, card push, card cut), ns-friend (the friend-queue task push)
// and ns-reconciler (the waiting-resolve pass), each run ws.Reorder, its
// two reads and its FCALL ns_ws_reorder, under their own ACL user from
// store.ACLRules, and ws.ProbeReorder (the push doors' grant check). The
// same seat without +fcall|ns_ws_reorder is refused NOPERM by the probe, so
// the control can fail.
func TestACLSeatsWriteTheWorkOrder(t *testing.T) {
	t.Parallel()
	start := time.Now()
	rules := map[string][]string{}
	for _, rule := range store.ACLRules {
		name, body, _ := strings.Cut(rule, " ")
		rules[name] = strings.Split(body, " ")
	}
	seats := []string{"ns-coordinator", "ns-friend", "ns-reconciler"}
	users := map[string][]string{"ns-deploy": rules["ns-deploy"]}
	for _, s := range seats {
		if !contains(rules[s], "+fcall|"+ws.FnReorder) {
			t.Fatalf("%s: store.ACLRules holds no +fcall|%s", s, ws.FnReorder)
		}
		users[s] = rules[s]
		var bare []string
		for _, tok := range rules[s] {
			if tok != "+fcall|"+ws.FnReorder {
				bare = append(bare, tok)
			}
		}
		users[s+"-bare"] = bare
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
	const stream = "acl: order"
	for i, on := range []string{"", "a0"} {
		id := fmt.Sprintf("a%d", i)
		if _, err := taskcard.Push(ctx, admin, taskcard.PushRequest{ID: id, Where: "waiting", Stream: stream,
			Sprint: "acl", Kind: "build", Ref: fmt.Sprintf("nova-tools#%d", 6000+i), Title: "order " + id,
			DependsOn: on, By: "rowan"}); err != nil {
			t.Fatalf("push %s: %v", id, err)
		}
	}
	for _, s := range seats {
		c := as(s)
		if err := ws.ProbeReorder(ctx, c); err != nil {
			t.Fatalf("%s probe: %v", s, err)
		}
		r, err := ws.Reorder(ctx, c, stream, s)
		if err != nil || r.Ranked != 3 {
			t.Fatalf("%s ws.Reorder: ranked=%d %v", s, r.Ranked, err)
		}
		err = ws.ProbeReorder(ctx, as(s+"-bare"))
		if err == nil || !strings.Contains(err.Error(), "NOPERM") {
			t.Fatalf("%s without the grant: probe %v, want NOPERM", s, err)
		}
		t.Logf("%s: probe ok, reorder ranked=%d; without +fcall|%s: %v", s, r.Ranked, ws.FnReorder, err)
	}
	t.Logf("wall %s", time.Since(start).Round(time.Millisecond))
}

func contains(toks []string, want string) bool {
	for _, t := range toks {
		if t == want {
			return true
		}
	}
	return false
}
