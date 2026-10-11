package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"
)

// An unlimited route (no --budget-server) has no budget on its proxy: the proxy is as it
// was. A limited one takes before every request; a provider with no concurrency budget
// renews and releases nothing (no call is made for either).
func TestNativeBudgetOnlyForALimitedRoute(t *testing.T) {
	t.Parallel()
	if b := nativeBudget(nativeRunConfig{model: "inception/mercury-3-preview-1002"}, nil); b != nil {
		t.Fatal("an unlimited route has a budget")
	}
	var notes bytes.Buffer
	taken := 0
	cfg := nativeRunConfig{model: "inception/mercury-3-preview-1002", budgetServer: "127.0.0.1:1", budgetAs: "hetzner", budgetHolder: "c1.g2",
		budgetTake: func(context.Context, string) (bool, time.Duration, error) { taken++; return true, 0, nil }}
	b := nativeBudget(cfg, &notes)
	if b == nil || b.Holder != "c1.g2" {
		t.Fatalf("budget %+v", b)
	}
	if ok, _, err := b.Take(context.Background(), "c1.g2.r1"); !ok || err != nil || taken != 1 {
		t.Fatalf("take: %v %v (%d)", ok, err, taken)
	}
	if b.Renew != nil || b.Release != nil {
		t.Fatal("an rpm-only route renews or releases slots it never took")
	}
	cfg.budgetSlots = true
	if b := nativeBudget(cfg, &notes); b.Renew == nil || b.Release == nil {
		t.Fatal("a provider with a concurrency budget does not renew and release its slots")
	}
	b.Log("RATE BUDGET WAIT c1.g2.r1: spent")
	if !strings.Contains(notes.String(), "NATIVE NOTE: RATE BUDGET WAIT") {
		t.Fatalf("notes %q", notes.String())
	}
}

// The server cannot be reached: the take is an error, which the proxy grants open.
func TestNativeBudgetServerDownIsAnError(t *testing.T) {
	t.Parallel()
	b := nativeBudget(nativeRunConfig{model: "inception/m", budgetServer: "127.0.0.1:1", budgetAs: "m1", budgetHolder: "h"}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, _, err := b.Take(ctx, "h.r1"); err == nil {
		t.Fatal("an unreachable server answered a take")
	}
}
