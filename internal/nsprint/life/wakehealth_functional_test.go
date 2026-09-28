//go:build functional

package life_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/life"
)

// TestWakeHealthRecordsRepairInRedis: the repaired row lands on the friend's
// wakehealth hash and the bootstrap is one cap:log wake-repair receipt, so the
// table prints it from Redis and the repair is on the record.
func TestWakeHealthRecordsRepairInRedis(t *testing.T) {
	t.Parallel()

	st, client, _ := controlRedis(t)
	ctx := context.Background()
	host := newFakeWakeHost()
	d := walterDecl()
	host.files[d.UnitFile] = true

	h := life.CheckWake(ctx, host, beatsLive(true), d)
	if err := life.RecordWake(ctx, st, h, "", "rowan", "tick-1"); err != nil {
		t.Fatal(err)
	}
	row, err := client.HGet(ctx, life.WakeHealthKey("walter"), "row").Result()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(row, "wake:unit-missing") || !strings.Contains(row, "wake: repaired "+walterUnit) {
		t.Errorf("stored row %q", row)
	}
	if state := client.HGet(ctx, life.WakeHealthKey("walter"), "state").Val(); state != life.WakeRepaired {
		t.Errorf("stored state %q", state)
	}
	entries, err := client.XRange(ctx, "cap:log", "-", "+").Result()
	if err != nil {
		t.Fatal(err)
	}
	var receipts []string
	for _, e := range entries {
		if e.Values["kind"] == "wake-repair" {
			receipts = append(receipts, fmt.Sprint(e.Values["subject"], " ", e.Values["reason"]))
		}
	}
	if len(receipts) != 1 || receipts[0] != "walter bootstrap "+walterUnit {
		t.Errorf("cap:log wake-repair receipts %q, want exactly [walter bootstrap %s]", receipts, walterUnit)
	}
}
