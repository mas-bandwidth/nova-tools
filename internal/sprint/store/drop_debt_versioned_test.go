package store

import (
	"context"
	"slices"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/hostload"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDropDebt_FormatAndParse validates that debt incarnation tokens format
// monotonically as "<member>:<rev>" and that parse extracts the constituent
// member and revision. Unversioned legacy tokens parse with revision 0.
func TestDropDebt_FormatAndParse(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "m1:1001", formatDebtItem("m1", 1001))
	assert.Equal(t, "worker-4:0", formatDebtItem("worker-4", 0))

	m, rev := parseDebtItem("m1:1001")
	assert.Equal(t, "m1", m)
	assert.Equal(t, uint64(1001), rev)

	// Legacy unversioned token without colon
	m, rev = parseDebtItem("m1")
	assert.Equal(t, "m1", m)
	assert.Equal(t, uint64(0), rev)

	// Malformed or empty token
	m, rev = parseDebtItem("")
	assert.Equal(t, "", m)
	assert.Equal(t, uint64(0), rev)
}

// TestDropDebt_AckDoesNotEraseNewerIncarnation witnesses the fix for the ABA race
// identified in stella-de36ad0d3103 and audit-pr5097-debt-incarnation-freshness.md:
// when member M is dropped at rev 1001, rejoins, and is dropped again at rev 1003,
// an acknowledgement for Drop #1 (rev 1001) MUST NOT erase the obligation for
// Drop #2 (rev 1003).
func TestDropDebt_AckDoesNotEraseNewerIncarnation(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ctx := context.Background()
	h.live = nil
	_, _ = h.m.DeleteKeys(ctx, []string{h.st.Names.Key("beat:m1"), h.st.Names.Key("beat:m2")})

	// t1: Drop #1 occurs at rev 1001
	item1 := formatDebtItem("m1", 1001)
	require.NoError(t, h.st.addDropDebt(ctx, []string{item1}))

	tokens, err := h.st.dropDebtTokens(ctx)
	require.NoError(t, err)
	assert.Equal(t, []string{item1}, tokens)

	// t2: Member rejoins and is dropped again at rev 1003
	item2 := formatDebtItem("m1", 1003)
	require.NoError(t, h.st.addDropDebt(ctx, []string{item2}))

	tokens, err = h.st.dropDebtTokens(ctx)
	require.NoError(t, err)
	assert.Equal(t, []string{item1, item2}, tokens, "debt stores both distinct incarnation obligations")

	// t3: In-flight worker acknowledging Drop #1 finishes and calls ackDropDebt with item1
	require.NoError(t, h.st.ackDropDebt(ctx, []string{item1}))

	// Verify that Drop #2 obligation is PRESERVED
	tokens, err = h.st.dropDebtTokens(ctx)
	require.NoError(t, err)
	assert.Equal(t, []string{item2}, tokens, "acknowledging Drop #1 must NOT erase Drop #2 obligation")
	assert.False(t, slices.Contains(tokens, item1), "Drop #1 is acknowledged and removed")
	assert.True(t, slices.Contains(tokens, item2), "Drop #2 remains active for subsequent cleanup")

	// Verify that bare member name query also returns m1
	members, err := h.st.dropDebt(ctx)
	require.NoError(t, err)
	assert.Equal(t, []string{"m1"}, members)
}

// TestDropDebt_FinishDropsSupersededByRejoin tests that if a member rejoins
// while an older drop debt exists, FinishDrops retires the superseded debt
// and keeps the rejoined member's beat.
func TestDropDebt_FinishDropsSupersededByRejoin(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ctx := context.Background()

	h.must(FleetStep(sprint.FleetReq{Op: "up", Member: "m1", Width: 1}))
	before := h.table()
	rec := before.MemberCtl("m1")
	require.NotNil(t, rec)
	require.True(t, rec.Placed())

	// Member is placed at rec.Rev >= 1.
	// Seed an older drop debt obligation (rev 0 or an older rev)
	oldDebt := formatDebtItem("m1", rec.Rev-1)
	require.NoError(t, h.st.addDropDebt(ctx, []string{oldDebt}))

	// FinishDrops should see rec.Placed() and rec.Rev > rev, retiring the debt
	require.NoError(t, h.st.FinishDrops(ctx))

	debt, err := h.st.dropDebt(ctx)
	require.NoError(t, err)
	assert.Empty(t, debt, "superseded drop obligation must be retired")

	// Verify m1's beat is preserved
	beats, err := h.st.Beats(ctx, []string{"m1"})
	require.NoError(t, err)
	assert.Contains(t, beats, "m1", "rejoined member's beat must be preserved")
}

// TestDropDebt_FinishDropsRetiresOlderAndDeletesMatching tests that FinishDrops
// simultaneously supersedes an older drop debt and executes the matching drop debt.
func TestDropDebt_FinishDropsRetiresOlderAndDeletesMatching(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ctx := context.Background()

	h.must(FleetStep(sprint.FleetReq{Op: "up", Member: "m1", Width: 1}))
	h.must(FleetStep(sprint.FleetReq{
		Op: "sync", Who: "tester", Sync: []sprint.SyncMember{{Name: "m2", Width: 1}}, Machines: []string{"m2"},
	}))

	// DropMembers drops m1's row and executes FinishDrops
	dropped, err := h.st.DropMembers(ctx, []string{"m2"})
	require.NoError(t, err)
	require.Equal(t, []string{"m1"}, dropped)

	// Load snapshot with extras to find m1's control card revision
	snap, err := h.st.Load(ctx, All, sprint.NamedExtras(sprint.Fleet, []string{sprint.CtlID("m1")}))
	require.NoError(t, err)
	rec := snap.Fleet.Card(sprint.CtlID("m1"))
	require.NotNil(t, rec)
	dropRev := rec.Rev

	// Re-add beat for m1 to simulate state before beat deletion
	zero := 0.0
	_, err = h.st.Beat(ctx, "m1", &zero, hostload.Source{})
	require.NoError(t, err)

	// Seed debt with both an older revision and the matching revision
	oldDebt := formatDebtItem("m1", dropRev-1)
	matchingDebt := formatDebtItem("m1", dropRev)
	require.NoError(t, h.st.addDropDebt(ctx, []string{oldDebt, matchingDebt}))

	// Run FinishDrops
	require.NoError(t, h.st.FinishDrops(ctx))

	// Debt should be completely empty (oldDebt superseded, matchingDebt deleted)
	debt, err := h.st.dropDebt(ctx)
	require.NoError(t, err)
	assert.Empty(t, debt, "both superseded and executed debts must be cleared")

	// Beat should be deleted
	beats, err := h.st.Beats(ctx, []string{"m1"})
	require.NoError(t, err)
	assert.NotContains(t, beats, "m1", "matching beat delete must be executed")
}

// TestDropDebt_TeardownExtractsBareMemberNames ensures that Teardown parses
// versioned debt tokens into bare member names so epochs.Beating names real members.
func TestDropDebt_TeardownExtractsBareMemberNames(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ctx := context.Background()

	// Seed drop debt with versioned tokens
	require.NoError(t, h.st.addDropDebt(ctx, []string{
		formatDebtItem("m1", 1001),
		formatDebtItem("worker-2", 450),
	}))

	// Run Teardown
	_, err := h.st.Teardown(ctx)
	require.NoError(t, err)
}
