package config

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fundingMem is a Mem store with the fake clock the funding tests use: it starts at
// 1000.0 seconds, never at 0 (the brief's clock rule).
func fundingMem(t *testing.T) *Mem {
	t.Helper()
	m := NewMem()
	m.Now = func() time.Time { return time.Unix(1000, 0).UTC() }
	return m
}

// fundingProvider is a provider row the tests add, funded=false so any actor may add it.
func fundingProvider(t *testing.T, m *Mem, name string) {
	t.Helper()
	k, ok := Lookup(KindProvider)
	require.True(t, ok)
	row, err := k.NewRow(name, map[string]string{"billing": "metered", "funded": "false"})
	require.NoError(t, err)
	_, err = m.Insert(context.Background(), KindProvider, row, "machine")
	require.NoError(t, err)
}

// fundingOwner declares the sprint owner as glenn, the first declaration.
func fundingOwner(t *testing.T, m *Mem) {
	t.Helper()
	_, _, err := m.Update(context.Background(), KindSprint, KindSprint, map[string]string{"owner": "glenn"}, "glenn")
	require.NoError(t, err)
}

// TestFundingRefusesANonOwnerRefund pins that a provider's funded flip to true is the
// owner's: machine and the coordinator's name are refused ErrOwnerOnly with the row
// unchanged, and glenn with owner glenn writes (tla/ProviderFunding.tla OnlyOwnerFunds;
// docs/SPEC-CONFIG.md, "Funding is the owner's").
func TestFundingRefusesANonOwnerRefund(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m := fundingMem(t)
	fundingProvider(t, m, "p1")

	_, _, err := m.Update(ctx, KindProvider, "p1", map[string]string{"funded": "true"}, "machine")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrOwnerOnly)
	assert.Contains(t, err.Error(), "none is declared")
	assert.Contains(t, err.Error(), "sprint set --owner")

	fundingOwner(t, m)
	_, _, err = m.Update(ctx, KindProvider, "p1", map[string]string{"funded": "true"}, "machine")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrOwnerOnly)
	assert.Contains(t, err.Error(), "funded=true is the owner's to write")
	assert.Contains(t, err.Error(), "provider set p1 --funded true --actor")

	_, _, err = m.Update(ctx, KindProvider, "p1", map[string]string{"funded": "true"}, "coordinator")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrOwnerOnly)

	row, found, err := m.Get(ctx, KindProvider, "p1")
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, "false", row.Fields["funded"], "the refused write left the row unchanged")

	after, _, err := m.Update(ctx, KindProvider, "p1", map[string]string{"funded": "true"}, "glenn")
	require.NoError(t, err)
	assert.Equal(t, "true", after.Fields["funded"])
}

// TestFundingRefusesAnEnabledRouteOnAnUnfundedProvider pins that enabling a route whose
// provider's row says funded=false is refused, naming the provider and that the owner
// funds it first (tla/ProviderFunding.tla NoEnableOnUnfunded).
func TestFundingRefusesAnEnabledRouteOnAnUnfundedProvider(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m := fundingMem(t)
	fundingProvider(t, m, "p1")

	kk, ok := Lookup(KindRoute)
	require.True(t, ok)
	route, err := kk.NewRow("r1", map[string]string{"tier": "pro", "provider": "p1", "model": "m", "deadline": "60", "enabled": "true"})
	require.NoError(t, err)
	_, err = m.Insert(ctx, KindRoute, route, "machine")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrOwnerOnly)
	assert.Contains(t, err.Error(), "p1")
	assert.Contains(t, err.Error(), "funds it first")

	_, found, err := m.Get(ctx, KindRoute, "r1")
	require.NoError(t, err)
	assert.False(t, found, "the refused route was not written")
}

// TestFundingFlipIsOneWritePerProvider pins the flip: funded=false is accepted from any
// actor, machine included, and a set that leaves funded false as it was is a no-op the
// store skips, so the flip writes once per provider (tla/ProviderFunding.tla
// OneWritePerProvider).
func TestFundingFlipIsOneWritePerProvider(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m := fundingMem(t)
	fundingOwner(t, m)

	k, ok := Lookup(KindProvider)
	require.True(t, ok)
	row, err := k.NewRow("p1", map[string]string{"billing": "metered", "funded": "true"})
	require.NoError(t, err)
	_, err = m.Insert(ctx, KindProvider, row, "glenn")
	require.NoError(t, err)

	_, _, err = m.Update(ctx, KindProvider, "p1", map[string]string{"funded": "false"}, "machine")
	require.NoError(t, err)
	_, _, err = m.Update(ctx, KindProvider, "p1", map[string]string{"funded": "false"}, "machine")
	require.NoError(t, err)

	changes, err := m.History(ctx, KindProvider, "p1")
	require.NoError(t, err)
	sets := 0
	for _, c := range changes {
		if c.Op == OpSet {
			sets++
		}
	}
	assert.Equal(t, 1, sets, "the flip wrote once, the repeated set was skipped")
}

// TestFundingOwnerIsDeclaredOnce pins the first declaration: the owner field is written
// only when it is empty and the actor names themself, or the actor is the current owner.
func TestFundingOwnerIsDeclaredOnce(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m := fundingMem(t)

	_, _, err := m.Update(ctx, KindSprint, KindSprint, map[string]string{"owner": "glenn"}, "machine")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrOwnerOnly)
	assert.Contains(t, err.Error(), "sprint owner is undeclared")

	_, _, err = m.Update(ctx, KindSprint, KindSprint, map[string]string{"owner": "glenn"}, "glenn")
	require.NoError(t, err)

	_, _, err = m.Update(ctx, KindSprint, KindSprint, map[string]string{"owner": "alice"}, "machine")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrOwnerOnly)

	after, _, err := m.Update(ctx, KindSprint, KindSprint, map[string]string{"owner": "alice"}, "glenn")
	require.NoError(t, err)
	assert.Equal(t, "alice", after.Fields["owner"], "the current owner may hand the owner role over")
}

// TestFundingReversedWitness pins the reversed witnesses with the refuser's call removed
// through the test-only seam (never a flag): the non-owner refund and the route enable on
// an unfunded provider are written, and the test says so.
func TestFundingReversedWitness(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m := fundingMem(t)
	m.RefuserOff = true
	fundingProvider(t, m, "p1")

	after, _, err := m.Update(ctx, KindProvider, "p1", map[string]string{"funded": "true"}, "machine")
	require.NoError(t, err)
	assert.Equal(t, "true", after.Fields["funded"], "with the refuser out the non-owner refund is written")

	kk, ok := Lookup(KindRoute)
	require.True(t, ok)
	route, err := kk.NewRow("r1", map[string]string{"tier": "pro", "provider": "p1", "model": "m", "deadline": "60", "enabled": "true"})
	require.NoError(t, err)
	_, err = m.Insert(ctx, KindRoute, route, "machine")
	require.NoError(t, err)
}
