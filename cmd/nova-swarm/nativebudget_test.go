package main

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// An unlimited route (no --budget-server) has no budget on its proxy: the proxy is as it
// was. A limited one takes before every request; a provider with no concurrency budget
// renews and releases nothing (no call is made for either).
func TestNativeBudgetOnlyForALimitedRoute(t *testing.T) {
	t.Parallel()
	assert.Nil(t, nativeBudget(nativeRunConfig{model: "inception/mercury-3-preview-1002"}, nil), "an unlimited route has no budget")
	var notes bytes.Buffer
	taken := 0
	cfg := nativeRunConfig{model: "inception/mercury-3-preview-1002", budgetServer: "127.0.0.1:1", budgetAs: "hetzner", budgetHolder: "c1.g2",
		budgetTake: func(context.Context, string) (bool, time.Duration, error) { taken++; return true, 0, nil }}
	b := nativeBudget(cfg, &notes)
	require.NotNil(t, b)
	assert.Equal(t, "c1.g2", b.Holder)
	ok, _, err := b.Take(context.Background(), "c1.g2.r1")
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, 1, taken)
	assert.Nil(t, b.Renew, "an rpm-only route renews no slot")
	assert.Nil(t, b.Release, "an rpm-only route releases no slot")
	cfg.budgetSlots = true
	slots := nativeBudget(cfg, &notes)
	require.NotNil(t, slots)
	assert.NotNil(t, slots.Renew, "a provider with a concurrency budget renews its slots")
	assert.NotNil(t, slots.Release, "a provider with a concurrency budget releases its slots")
	b.Log("RATE BUDGET WAIT c1.g2.r1: spent")
	assert.Contains(t, notes.String(), "NATIVE NOTE: RATE BUDGET WAIT")
}

// The server cannot be reached: the take is an error, which the proxy grants open.
func TestNativeBudgetServerDownIsAnError(t *testing.T) {
	t.Parallel()
	b := nativeBudget(nativeRunConfig{model: "inception/m", budgetServer: "127.0.0.1:1", budgetAs: "m1", budgetHolder: "h"}, nil)
	require.NotNil(t, b)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, _, err := b.Take(ctx, "h.r1")
	assert.Error(t, err, "an unreachable server answered a take")
}
