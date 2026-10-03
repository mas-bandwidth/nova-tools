package main

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// `card <id>` prints the tier the card is on and its ceiling (docs/SPEC-SPRINT.md
// section 5). Before the first deal that tier is the ceiling. A deal on a route
// writes tier_now, flash first, so the line is then tier=flash under the pro
// ceiling, and tier=pro after the bound on flash. The ATTEMPT lines and --json
// print the same tier.
func TestCardPrintsTheTierAndItsCeiling(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.m.SetRoutes(costRoutes())
	ta.ok("add --stream s1 --count 1 --brief-file " + writeBrief(t, "s1: the work (s1) tier: pro"))
	tiers := func() (string, string) {
		t.Helper()
		var v struct{ Tier, Ceiling string }
		require.NoError(t, json.Unmarshal([]byte(ta.ok("card s1-1 --json")), &v))
		return v.Tier, v.Ceiling
	}
	assert.Contains(t, ta.ok("card s1-1"), " tier=pro ceiling=pro\n", "before the first deal the tier is the ceiling")
	now, ceiling := tiers()
	assert.Equal(t, []string{"pro", "pro"}, []string{now, ceiling}, "before the first deal")
	ta.deal(1)
	out := ta.ok("card s1-1")
	assert.Contains(t, out, " tier=flash ceiling=pro\n", "the CARD OK line")
	assert.Contains(t, out, "route=flash-a model=opencode/deepseek-v4-flash tier=flash member=m1")
	now, ceiling = tiers()
	assert.Equal(t, []string{"flash", "pro"}, []string{now, ceiling}, "--json")

	// attempt 1 at its bound on flash: the deal escalates it to pro
	gen := func() int {
		t.Helper()
		st, err := ta.a.store(common{redis: "mem:0", actor: "tester"})
		require.NoError(t, err)
		snap, err := st.Load(context.Background(), []string{sprint.Fleet}, nil)
		require.NoError(t, err)
		return snap.Fleet.Card("s1-1.w1").Int("gen")
	}
	for i := 0; i <= sprint.MaxRedeals; i++ {
		g := gen()
		ta.ok(fmt.Sprintf("take --as m1 s1-1.w1@%d", g))
		ta.ok(fmt.Sprintf("finish --as m1 s1-1.w1@%d --failed --report 'provider failure: 529'", g))
		ta.deal(1)
	}
	out = ta.ok("card s1-1")
	assert.Contains(t, out, " tier=pro ceiling=pro\n", "escalated")
	assert.Contains(t, out, "ATTEMPT 2 card=s1-1.w2 gen=1 route=pro-a model=opencode/deepseek-v4-pro tier=pro member=m1")
	now, ceiling = tiers()
	assert.Equal(t, []string{"pro", "pro"}, []string{now, ceiling}, "--json")
}

// tierNow puts the primary id on a tier, as the machine's escalation leaves it (flash
// first: every card's first deal is on flash): a test of the two-reader machinery
// (cost rule 4) on a store with routes means a card on pro.
func (ta *testApp) tierNow(id, tier string) {
	ta.t.Helper()
	ctx := context.Background()
	st, err := ta.a.store(common{redis: "mem:0", actor: "tester"})
	require.NoError(ta.t, err)
	snap, err := st.Load(ctx, []string{sprint.Work}, nil)
	require.NoError(ta.t, err)
	c := snap.Work.Card(id)
	require.NotNil(ta.t, c, id)
	_, err = ta.m.Apply(ctx, ntable.BatchManifest{Schema: 1, Table: st.Names.Table(sprint.Work), Epoch: fmt.Sprint(st.PinnedEpoch()),
		ExpectedTableRevision: fmt.Sprint(snap.Work.Revision), OperationID: "tier-now-" + id,
		Members: []ntable.BatchMemberEntry{{ID: c.ID, Expect: &ntable.MemberExpect{Revision: fmt.Sprint(c.Rev)}, Set: map[string]string{sprint.FieldTierNow: tier}}}})
	require.NoError(ta.t, err)
}
