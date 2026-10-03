package main

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// `card <id>` shows the tier a card's next deal draws from and its ceiling (flash first:
// docs/SPEC-SPRINT.md section 5; the owner, 2026-10-02: "Flash first on every card; pro
// only on escalation"): a pro card is on flash under a pro ceiling until its first attempt
// reaches its bound, then on pro, on its CARD OK line, its ATTEMPT lines and in --json.
func TestCardPrintsTheTierAndItsCeiling(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.m.SetRoutes(costRoutes())
	ta.ok("add --stream s1 --count 1 --brief-file " + writeBrief(t, "s1: the work (s1) tier: pro"))
	ta.deal(1)
	tiers := func() (string, string) {
		t.Helper()
		var v struct{ Tier, Ceiling string }
		require.NoError(t, json.Unmarshal([]byte(ta.ok("card s1-1 --json")), &v))
		return v.Tier, v.Ceiling
	}
	out := ta.ok("card s1-1")
	assert.Contains(t, out, " tier=flash ceiling=pro\n", "the CARD OK line")
	assert.Contains(t, out, "route=flash-a model=opencode/deepseek-v4-flash tier=flash member=m1")
	now, ceiling := tiers()
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
