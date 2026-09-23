package deal_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/backpressure"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/deal"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/redis/go-redis/v9"
)

// TestControl48 is control 48 of nova-sprint (#3095, spec 4.10): at land_debt
// 21 above cap 20 a fix card and a ci card deal and a bulk nx card waits;
// after two landings the next pass deals that bulk card; a type at 0 of 40
// landed refuses a new cut until one lands.
//
// Antecedent: 285 PRs in about 8h against a land rate of about 5/h, then a
// recut of 471 cards that opened 54 PRs of which none landed.
func TestControl48(t *testing.T) {
	st, client := miniStore(t)
	ctx := context.Background()
	const sprint = "control-00000030"
	// More free slots than exempt cards, so a held card is the gate and not a full bench.
	const slots = 8

	product := backpressure.InterimLandRatePerHour * backpressure.InterimLandHorizonHours
	if product != 20 {
		t.Fatalf("interim rate × horizon = %d; want 20", product)
	}
	if backpressure.InterimLandCap != product {
		t.Fatalf("interim cap %d; want the product %d", backpressure.InterimLandCap, product)
	}
	if backpressure.InterimTypeGateAfter != 40 {
		t.Fatalf("interim type_gate_after %d; want 40", backpressure.InterimTypeGateAfter)
	}

	policy := backpressure.PolicyKey(sprint)
	if err := client.HSet(ctx, policy,
		backpressure.PolicyLandRate, "6",
		backpressure.PolicyLandHorizon, "4",
		backpressure.PolicyTypeGateAfter, "2",
	).Err(); err != nil {
		t.Fatal(err)
	}
	measured, err := backpressure.Measure(ctx, st, sprint)
	if err != nil {
		t.Fatal(err)
	}
	if measured.Cap != 24 || measured.RatePerHour != 6 || measured.HorizonHours != 4 || measured.Debt != 0 {
		t.Fatalf("measured = %+v; want 6/h × 4h = cap 24 and no debt", measured)
	}
	if err := deal.Cut(ctx, st, sprint, "cell3"); err != nil {
		t.Fatalf("a type with no PRs is not gated: %v", err)
	}
	sadd(t, ctx, client, backpressure.TypePRsKey(sprint, "cell3"), prIDs(t, 201, 202)...)
	err = deal.Cut(ctx, st, sprint, "cell3")
	if !errors.Is(err, backpressure.ErrCutRefused) || !strings.Contains(err.Error(), "0 of 2") {
		t.Fatalf("gate 2: %v; want ErrCutRefused at 0 of 2", err)
	}
	if err := client.HDel(ctx, policy,
		backpressure.PolicyLandRate, backpressure.PolicyLandHorizon, backpressure.PolicyTypeGateAfter,
	).Err(); err != nil {
		t.Fatal(err)
	}
	interim, err := backpressure.Measure(ctx, st, sprint)
	if err != nil {
		t.Fatal(err)
	}
	if interim.Cap != 20 || interim.RatePerHour != 5 || interim.HorizonHours != 4 {
		t.Fatalf("interim = %+v; want 5/h × 4h = 20", interim)
	}
	if err := deal.Cut(ctx, st, sprint, "cell3"); err != nil {
		t.Fatalf("2 PRs is under the interim gate of 40: %v", err)
	}

	// 11 reading + 10 landable = 21. One id in both sets still counts once.
	sadd(t, ctx, client, backpressure.ReadingKey(sprint), prIDs(t, 1, 11)...)
	sadd(t, ctx, client, backpressure.LandableKey(sprint), prIDs(t, 12, 21)...)
	debt, err := backpressure.Measure(ctx, st, sprint)
	if err != nil {
		t.Fatal(err)
	}
	if debt.Debt != 21 || debt.Cap != 20 || !debt.Above() {
		t.Fatalf("debt = %+v; want 21 > 20", debt)
	}
	overlap, err := backpressure.PRID("nova-tools", 1)
	if err != nil {
		t.Fatal(err)
	}
	sadd(t, ctx, client, backpressure.LandableKey(sprint), overlap)
	if again, err := backpressure.Measure(ctx, st, sprint); err != nil || again.Debt != 21 {
		t.Fatalf("overlap debt = %+v, %v; a PR in both sets counts once", again, err)
	}
	srem(t, ctx, client, backpressure.LandableKey(sprint), overlap)

	// nx-bulk is first on purpose: queue position is not the front flag, and a
	// held card does not take the slot the fix and ci cards need.
	queued := []backpressure.Card{
		{Label: "nx-bulk", Type: backpressure.TypeNX},
		{Label: "fix-a", Type: backpressure.TypeFix},
		{Label: "ci-12-abcdef01", Type: backpressure.TypeCI},
		{Label: "rebase-b", Type: backpressure.TypeRebase},
		{Label: "nx-front", Type: backpressure.TypeNX, Front: true},
	}
	first, err := deal.LandPass(ctx, st, sprint, slots, queued)
	if err != nil {
		t.Fatal(err)
	}
	if first.Debt != 21 || first.Cap != 20 || !first.Above {
		t.Fatalf("pass = debt %d cap %d above %v; want 21 > 20", first.Debt, first.Cap, first.Above)
	}
	if got, want := labels(first.Dealt), "fix-a,ci-12-abcdef01,rebase-b,nx-front"; got != want {
		t.Fatalf("dealt %s; want %s", got, want)
	}
	if got, want := labels(first.Waiting), "nx-bulk"; got != want {
		t.Fatalf("waiting %s; want %s", got, want)
	}
	if len(first.Dealt) >= slots {
		t.Fatalf("dealt %d with %d slots; the hold must leave a free slot", len(first.Dealt), slots)
	}
	if !backpressure.Flows(20, 20, backpressure.Card{Label: "nx-bulk", Type: backpressure.TypeNX}) {
		t.Fatal("debt equal to the cap is not above it; the bulk card flows at 20")
	}
	if !backpressure.Flows(21, 20, backpressure.Card{Label: "ci-9-abcdef02"}) {
		t.Fatal("a ci- label is a ci card and flows while debt is above the cap")
	}

	// Two landings take debt from 21 to 19. The bulk card is still queued.
	// The next pass is a new call; this pass does not change its mind.
	landed := prIDs(t, 1, 2)
	srem(t, ctx, client, backpressure.ReadingKey(sprint), landed...)
	next, err := deal.LandPass(ctx, st, sprint, slots, first.Waiting)
	if err != nil {
		t.Fatal(err)
	}
	if next.Debt != 19 || next.Cap != 20 || next.Above {
		t.Fatalf("after two landings debt %d cap %d above %v; want 19 under 20", next.Debt, next.Cap, next.Above)
	}
	if got, want := labels(next.Dealt), "nx-bulk"; got != want || len(next.Waiting) != 0 {
		t.Fatalf("next pass dealt %s waiting %s; want the bulk nx card", labels(next.Dealt), labels(next.Waiting))
	}

	nx := prIDs(t, 101, 140)
	if len(nx) != 40 {
		t.Fatalf("nx ids = %d; want 40", len(nx))
	}
	sadd(t, ctx, client, backpressure.TypePRsKey(sprint, backpressure.TypeNX), nx[:39]...)
	if err := deal.Cut(ctx, st, sprint, backpressure.TypeNX); err != nil {
		t.Fatalf("39 PRs with 0 landed is under the gate: %v", err)
	}
	sadd(t, ctx, client, backpressure.TypePRsKey(sprint, backpressure.TypeNX), nx[39])
	err = deal.Cut(ctx, st, sprint, backpressure.TypeNX)
	if !errors.Is(err, backpressure.ErrCutRefused) || !strings.Contains(err.Error(), "0 of 40") {
		t.Fatalf("0 of 40 = %v; want ErrCutRefused", err)
	}
	if n, err := client.SCard(ctx, backpressure.TypePRsKey(sprint, backpressure.TypeNX)).Result(); err != nil || n != 40 {
		t.Fatalf("prs after a refused cut = %d, %v; want 40, the cut writes nothing", n, err)
	}
	sadd(t, ctx, client, backpressure.TypeLandedKey(sprint, backpressure.TypeNX), nx[0])
	if err := deal.Cut(ctx, st, sprint, backpressure.TypeNX); err != nil {
		t.Fatalf("one landed PR must lift the pause: %v", err)
	}
}

func miniStore(t *testing.T) (*store.Store, *redis.Client) {
	t.Helper()
	m := miniredis.RunT(t)
	c := redis.NewClient(&redis.Options{Addr: m.Addr()})
	t.Cleanup(func() { _ = c.Close() })
	return store.New(c), c
}

func prIDs(t *testing.T, from, to int) []string {
	t.Helper()
	out := make([]string, 0, to-from+1)
	for n := from; n <= to; n++ {
		id, err := backpressure.PRID("nova-tools", n)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, id)
	}
	return out
}

func sadd(t *testing.T, ctx context.Context, c *redis.Client, key string, members ...string) {
	t.Helper()
	args := make([]any, len(members))
	for i, m := range members {
		args[i] = m
	}
	if err := c.SAdd(ctx, key, args...).Err(); err != nil {
		t.Fatalf("SADD %s: %v", key, err)
	}
}

func srem(t *testing.T, ctx context.Context, c *redis.Client, key string, members ...string) {
	t.Helper()
	args := make([]any, len(members))
	for i, m := range members {
		args[i] = m
	}
	if err := c.SRem(ctx, key, args...).Err(); err != nil {
		t.Fatalf("SREM %s: %v", key, err)
	}
}

func labels(cards []backpressure.Card) string {
	parts := make([]string, 0, len(cards))
	for _, c := range cards {
		parts = append(parts, c.Label)
	}
	return strings.Join(parts, ",")
}
