package backpressure_test

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/backpressure"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/land"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/redis/go-redis/v9"
)

// TestDebtFromUnitRecords is nova-tools#3491: land_debt is counted from the
// unit records (#3139 2.2), s:<S>:units and each s:<S>:u:<unit> state, not
// from the retired PR keys s:<S>:idx:pr:reading and the global
// s:<S>:landable, which nothing writes after #3139 B1. The retired keys are
// filled with ids that are not units; a Measure that still reads them counts
// them and fails here.
func TestDebtFromUnitRecords(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	client := redis.NewClient(&redis.Options{Addr: testutil.Start(t)})
	t.Cleanup(func() { _ = client.Close() })
	st := store.New(client)
	const sprint = "debt-3491"

	// Retired keys, spelled out: no helper names them any more.
	retiredReading := "s:" + sprint + ":idx:pr:reading"
	retiredLandable := "s:" + sprint + ":landable"
	for n := 1; n <= 30; n++ {
		id := "nova-tools#" + strconv.Itoa(n)
		if err := client.SAdd(ctx, retiredReading, id).Err(); err != nil {
			t.Fatal(err)
		}
		if err := client.SAdd(ctx, retiredLandable, id).Err(); err != nil {
			t.Fatal(err)
		}
	}
	empty, err := backpressure.Measure(ctx, st, sprint)
	if err != nil {
		t.Fatal(err)
	}
	if empty.Debt != 0 {
		t.Fatalf("debt with only the retired keys = %d; want 0, Measure must not read %s or %s",
			empty.Debt, retiredReading, retiredLandable)
	}

	// One unit per state. Debt is every unit between its first head record
	// and landing: opened, reading, landable, batched, gating, green, landing.
	// landed is terminal; dropped and settled are exits.
	states := map[string]bool{
		"opened": true, "reading": true, "landable": true, "batched": true,
		"gating": true, "green": true, "landing": true,
		"landed": false, "dropped": false, "settled": false,
	}
	want := 0
	for state, debt := range states {
		unitRecord(t, ctx, client, sprint, "u-"+state, state)
		if debt {
			want++
		}
	}
	got, err := backpressure.Measure(ctx, st, sprint)
	if err != nil {
		t.Fatal(err)
	}
	if got.Debt != want || want != 7 {
		t.Fatalf("debt = %d; want %d, one per unit in opened..landing", got.Debt, want)
	}
	if got.Cap != backpressure.InterimLandCap {
		t.Fatalf("cap = %d; want the interim %d", got.Cap, backpressure.InterimLandCap)
	}

	// A landing moves a unit's state; the debt follows the record.
	if err := client.HSet(ctx, land.UnitKey(sprint, "u-landing"), "state", "landed").Err(); err != nil {
		t.Fatal(err)
	}
	after, err := backpressure.Measure(ctx, st, sprint)
	if err != nil || after.Debt != want-1 {
		t.Fatalf("after one landing debt = %+v, %v; want %d", after, err, want-1)
	}

	// An indexed unit with a state outside the contract is refused, never
	// guessed into or out of the debt.
	unitRecord(t, ctx, client, sprint, "u-odd", "limbo")
	if _, err := backpressure.Measure(ctx, st, sprint); err == nil || !strings.Contains(err.Error(), "u-odd") {
		t.Fatalf("unknown unit state: err %v; want a refusal naming u-odd", err)
	}
	if err := client.HSet(ctx, land.UnitKey(sprint, "u-odd"), "state", "dropped").Err(); err != nil {
		t.Fatal(err)
	}
	// An indexed unit with no record is refused too: the index and the record
	// are written in one call (ns_unit_head), so a gap is not zero debt.
	if err := client.SAdd(ctx, land.UnitsSetKey(sprint), "u-ghost").Err(); err != nil {
		t.Fatal(err)
	}
	if _, err := backpressure.Measure(ctx, st, sprint); err == nil || !strings.Contains(err.Error(), "u-ghost") {
		t.Fatalf("indexed unit with no record: err %v; want a refusal naming u-ghost", err)
	}
}

func unitRecord(t *testing.T, ctx context.Context, c *redis.Client, sprint, unit, state string) {
	t.Helper()
	if err := c.HSet(ctx, land.UnitKey(sprint, unit), "repo", "nova-tools", "base", "dev", "state", state).Err(); err != nil {
		t.Fatal(err)
	}
	if err := c.SAdd(ctx, land.UnitsSetKey(sprint), unit).Err(); err != nil {
		t.Fatal(err)
	}
}
