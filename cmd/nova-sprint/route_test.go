package main

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
)

// TestRoutePRToReadCutsCI is the route control of Rowan's hold 4 item 3 on
// #3532: the pr-to-read rule built by routePRToRead, the one runRoute wires,
// cuts the ci card before it adopts. Without the CICut wiring the adoption
// prints cut=0 and no ci record exists, and this test fails. The store is a
// throwaway redis-server with the nova_sprint library loaded.
func TestRoutePRToReadCutsCI(t *testing.T) {
	_, client := sprintRedis(t)
	ctx := context.Background()
	const S, repo, n = "route-3532", "nova-tools", 7
	head := strings.Repeat("7", 40)
	tip := strings.Repeat("b", 40)
	for _, c := range []error{
		client.HSet(ctx, "s:"+S, "status", "open").Err(),
		client.HSet(ctx, "s:"+S+":policy", "readers", "1").Err(),
		client.SAdd(ctx, "benches", "rt-b1").Err(),
		client.HSet(ctx, "bench:rt-b1:desired", "slots", "4", "paused", "0", "legs", "go").Err(),
		client.SAdd(ctx, "friends", "rt-a", "rt-b").Err(),
		client.HSet(ctx, "friend:rt-a:desired", "slots", "4", "paused", "0").Err(),
		client.HSet(ctx, "friend:rt-b:desired", "slots", "4", "paused", "0").Err(),
		client.HSet(ctx, "friend:rt-a:beat", "harness", "ctl", "at", "1").Err(),
		client.HSet(ctx, "friend:rt-b:beat", "harness", "ctl", "at", "1").Err(),
		client.HSet(ctx, fmt.Sprintf("s:%s:pr:%s:%d", S, repo, n),
			"head", head, "state", "opened", "draft", "false", "author", "rt-a", "base", "dev").Err(),
		client.SAdd(ctx, "s:"+S+":prs", fmt.Sprintf("%s#%d", repo, n)).Err(),
	} {
		if c != nil {
			t.Fatal(c)
		}
	}

	out := &bytes.Buffer{}
	rule := routePRToRead(store.New(client), S, "route-control", "route", out)
	rule.Block = -1
	if err := rule.Start(ctx); err != nil {
		t.Fatal(err)
	}
	pass := func() string {
		t.Helper()
		out.Reset()
		if _, err := rule.Pass(ctx); err != nil {
			t.Fatalf("pass: %v", err)
		}
		return out.String()
	}

	id := fmt.Sprintf("%s#%d", repo, n)
	if got := pass(); !strings.Contains(got, "WAIT "+id+" no base tip") || strings.Contains(got, "ADOPT") {
		t.Fatalf("no base tip: pass printed %q, want WAIT %s no base tip and no ADOPT", got, id)
	}
	if err := client.HSet(ctx, "land:"+repo+":dev:tip", "sha", tip).Err(); err != nil {
		t.Fatal(err)
	}
	if got := pass(); !strings.Contains(got, fmt.Sprintf("ADOPT %s@%s cut=1 reads=rt-b", id, head[:12])) {
		t.Fatalf("tip recorded: pass printed %q, want ADOPT %s@%s cut=1 reads=rt-b", got, id, head[:12])
	}
	rec, err := client.HGetAll(ctx, "ci:"+repo+":"+head).Result()
	if err != nil {
		t.Fatal(err)
	}
	if rec["verdict"] != "PENDING" || rec["base"] != tip {
		t.Fatalf("ci:%s:%s = %v, want the route's cut PENDING at base %s", repo, head, rec, tip)
	}
}
