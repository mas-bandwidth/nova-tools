//go:build functional

package tset

import (
	"context"
	"fmt"
	"reflect"
	"testing"
)

func TestAdvanceRequiresStableIdentityLua(t *testing.T) {
	t.Parallel()
	fx := newTSetFixture(t)
	fx.Define(t, "work", "c")
	fx.Activate(t)
	entry := `{"kind":"advance","from":"0"}`
	for _, tc := range []struct {
		name, identity string
	}{
		{"anonymous", ""},
		{"op only", `,"op":"clear-0"`},
		{"intent only", `,"intent":"stable clear intent"`},
	} {
		raw := fmt.Sprintf(`{"epoch":"0","space":%q%s,"entries":[%s]}`, fx.Space, tc.identity, entry)
		before := commitProbeImage(t, fx.Client)
		reply := commitProbeRawCall(t, fx.Client, raw)
		if reply.Status != "refused" || reply.Code != "REQUEST" {
			t.Fatalf("%s: reply=%+v, want REQUEST", tc.name, reply)
		}
		if after := commitProbeImage(t, fx.Client); !reflect.DeepEqual(before, after) {
			t.Fatalf("%s: refused advance changed Redis state", tc.name)
		}
	}
	// A named generic advance remains admitted and records its original-epoch
	// receipt before the new epoch becomes active.
	raw := fmt.Sprintf(`{"epoch":"0","space":%q,"op":"clear-0","intent":"stable clear intent","entries":[%s]}`, fx.Space, entry)
	reply := commitProbeRawCall(t, fx.Client, raw)
	if reply.Status != "ok" {
		t.Fatalf("named generic advance: %+v", reply)
	}
	ctx := context.Background()
	if got := fx.Client.HGet(ctx, fx.Space+"sprint:epoch", "n").Val(); got != "1" {
		t.Fatalf("active epoch=%q, want 1", got)
	}
	if n := fx.Client.HExists(ctx, fixtureDoneKey(fx.Space, "0"), "clear-0").Val(); !n {
		t.Fatal("named advance left no original-epoch receipt")
	}
}
