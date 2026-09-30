//go:build functional

package tset

import (
	"context"
	"testing"
)

// These faults corrupt actual stored definition fields after a valid fixture
// has been established. Both implementations must refuse the public Step and
// retain their complete pre-call state.
func TestRefuseCONFIG(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		field string
		value func(string) string
	}{
		{
			name:  "member prefix overlaps structural table namespace",
			field: "member_prefix",
			value: func(space string) string { return space + "table:" },
		},
		{
			name:  "definition epoch key differs from space epoch key",
			field: "epoch_key",
			value: func(space string) string { return space + "sprint:other" },
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			runNamedRefusal(t, namedRefusalCase{
				code: "CONFIG",
				step: func(space string) Step {
					return namedStep(space, namedCreate("work", "new", "r:c", "1"))
				},
				arrange: func(t *testing.T, fx *tsetFixture, mem *Mem) {
					t.Helper()
					configCorruptDefinition(t, fx, mem, tc.field, tc.value(fx.Space))
				},
			})
		})
	}
}

func configCorruptDefinition(t *testing.T, fx *tsetFixture, mem *Mem, field, value string) {
	t.Helper()
	ctx := context.Background()
	if err := fx.Client.HSet(ctx, fx.Space+"table:work", field, value).Err(); err != nil {
		t.Fatalf("corrupt Redis definition %s: %v", field, err)
	}
	if got, err := fx.Client.HGet(ctx, fx.Space+"table:work", field).Result(); err != nil || got != value {
		t.Fatalf("Redis definition %s = %q, %v; want %q", field, got, err, value)
	}
	mem.mu.Lock()
	space := mem.spaces[fx.Space]
	if space == nil || space.defs["work"] == nil {
		mem.mu.Unlock()
		t.Fatal("Mem fixture lacks work definition")
	}
	switch field {
	case "member_prefix":
		space.defs["work"].memberPrefix = value
	case "epoch_key":
		space.defs["work"].epochKey = value
	default:
		mem.mu.Unlock()
		t.Fatalf("unrecognized definition field %q", field)
	}
	mem.mu.Unlock()
	snapshot, err := mem.Snapshot(fx.Space)
	if err != nil {
		t.Fatalf("Mem definition snapshot: %v", err)
	}
	definition := snapshot.Definitions["work"]
	if (field == "member_prefix" && definition.MemberPrefix != value) ||
		(field == "epoch_key" && definition.EpochKey != value) {
		t.Fatalf("Mem definition %s did not acquire corrupt value %q: %+v", field, value, definition)
	}
}
