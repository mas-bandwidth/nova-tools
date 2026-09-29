//go:build functional

package ntable_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
)

func TestMultiBatchNeverCreatedLastParticipantRefusesDefinitively(t *testing.T) {
	t.Parallel()
	_, c := live(t)
	ctx := context.Background()
	tables := multiTableFixture(t, c, "notable_a", "notable_b")
	tables = append(tables, ntable.MultiBatchTable{Name: "notable_missing", Epoch: "0", ExpectedTableRevision: "0"})
	m := ntable.MultiBatchManifest{Schema: 2, Scope: "test.notable", OperationID: "never-created", Tables: tables,
		Members: []ntable.MultiBatchMember{{RecordTable: "notable_a", ID: "card", Expect: &ntable.MultiMemberExpect{Absent: true,
			Places: []ntable.MultiPlaceExpect{{Table: "notable_a", Absent: true}, {Table: "notable_b", Absent: true}, {Table: "notable_missing", Absent: true}}},
			Placements: []ntable.MultiPlacement{{Table: "notable_a", Add: &ntable.MemberCreateOp{Row: "work", Col: "ready", Score: 1}},
				{Table: "notable_b", Add: &ntable.MemberCreateOp{Row: "work", Col: "ready", Score: 2}},
				{Table: "notable_missing", Add: &ntable.MemberCreateOp{Row: "work", Col: "ready", Score: 3}}}}}}
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ntable.ValidateMultiBatchManifestRaw(raw); err != nil {
		t.Fatalf("valid missing-participant request: %v", err)
	}
	image := storeImage(t, c)
	reply, err := c.FCall(ctx, ntable.FnApplyMulti, nil, m.Scope, string(raw)).Slice()
	if err != nil || len(reply) != 3 || reply[0] != "REFUSED" || reply[1] != "NOTABLE" || reply[2] != "notable_missing" {
		t.Fatalf("never-created participant wire: %#v, %v", reply, err)
	}
	assertMultiStoreUnchanged(t, c, image)
	if _, err := ntable.ApplyMultiBatch(ctx, c, m); !errors.Is(err, ntable.ErrNoTable) || errors.Is(err, ntable.ErrUnknownOutcome) ||
		!strings.Contains(err.Error(), `table "notable_missing"`) || !strings.Contains(err.Error(), "changed=no") {
		t.Fatalf("never-created participant must be a definite no-write refusal: %v", err)
	}
	assertMultiStoreUnchanged(t, c, image)
}
