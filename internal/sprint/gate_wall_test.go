package sprint

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/cardcost"
	"github.com/mas-bandwidth/nova-tools/pkg/cardhdr"
)

// The gate's measured wall (gate_wall.go; docs/SPEC-SPRINT.md section 5, the gate's wall):
// at add, a card whose TEST package's measured wall, the median wall of the ok work takes
// the sprint record holds for that package, exceeds the flash bound is admitted on pro.

// gateBrief is a brief whose line 1 names tier and whose TEST line names the package pkg.
func gateBrief(tier, pkg string) string {
	return "c: a card tier: " + tier + "\nREPO: mas-bandwidth/nova-tools\nTEST: " + pkg + " TestSomething\n\nThe task."
}

// measured books one take per wall on the primary id: a work take ending end.
func measured(pr *Card, end string, walls ...string) {
	for i, wall := range walls {
		book(pr, Consumer{Kind: "work", Card: pr.ID + ".w1", Attempt: 1, Gen: i + 1, Who: "m1", Tier: cardhdr.RoutePro, End: end, At: stamp(t0),
			Key: pr.ID + ".w1#g" + itoa(i+1), Usage: cardcost.Usage{Wall: wall}})
	}
}

func TestTierFollowsTheGatesMeasuredWall(t *testing.T) {
	t.Parallel()
	require.Equal(t, 15*time.Minute, FlashGateBound, "the flash bound: a gate a flash member cannot run in 15 minutes is not dealt flash")

	// the sprint record: landed cards on ./internal/slow took 20m and 25m to an ok take
	// (a failed take of 3h is no measure), cards on ./internal/fast 4m, cards on
	// ./internal/edge exactly the bound
	w := newWorld(t)
	w.must(Add(w.s, AddReq{Stream: "past", Cards: []CardAdd{
		{ID: "past-1", Brief: gateBrief("pro", "./internal/slow")},
		{ID: "past-2", Brief: gateBrief("pro", "./internal/slow")},
		{ID: "past-3", Brief: gateBrief("pro", "./internal/fast")},
		{ID: "past-4", Brief: gateBrief("pro", "./internal/edge")},
	}}))
	measured(w.s.Primary("past-1"), "ok", "1200s")
	measured(w.s.Primary("past-1"), "failed", "", "10800s")
	measured(w.s.Primary("past-2"), "ok", "1500s")
	measured(w.s.Primary("past-3"), "ok", "240s")
	measured(w.s.Primary("past-4"), "ok", "900s")
	w.s.Routes = []Route{{Name: "f1", Tier: cardhdr.RouteFlash, Provider: "p", Model: "f", Enabled: true},
		{Name: "p1", Tier: cardhdr.RoutePro, Provider: "p", Model: "q", Enabled: true}}

	cases := []struct {
		name, brief string
		tierNow     string // the tier the card is admitted on, "" for none (flash first)
		wall        string // the gate_wall it records, "" for none
	}{
		{"a gate measured over the bound is admitted pro", gateBrief("pro", "./internal/slow"), cardhdr.RoutePro, "22m30s"},
		{"a flash ceiling does not deal flash a gate flash cannot run", gateBrief("flash", "./internal/slow"), cardhdr.RoutePro, "22m30s"},
		{"a gate measured under the bound starts flash", gateBrief("pro", "./internal/fast"), "", ""},
		{"a gate measured at the bound is not over it", gateBrief("pro", "./internal/edge"), "", ""},
		{"a gate never measured starts flash", gateBrief("pro", "./internal/new"), "", ""},
		{"a card with no test has no gate to measure", "c: a card tier: pro\nTEST: none a docs change\n", "", ""},
		{"a frontier card is the coordinator's", gateBrief("frontier", "./internal/slow"), "", ""},
		{"a pinned model runs on its pin", "c: a card tier: pro\nmodel: p/m\ntokens: 1000\ndeadline: 600\nTEST: ./internal/slow TestSomething\n", "", ""},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// not parallel: the cases share w.s, whose tables index lazily on read (Table.index)
			id := "new-" + itoa(i+1)
			p := Add(w.s, AddReq{Stream: "new", Cards: []CardAdd{{ID: id, Brief: tc.brief}}})
			require.Empty(t, p.Refused)
			require.Len(t, p.Units, 1)
			var fields map[string]string
			for _, c := range p.Units[0].Changes {
				if c.Entry.ID == id {
					fields = c.Entry.Set
				}
			}
			require.NotNil(t, fields, "the add writes the card")
			assert.Equal(t, tc.tierNow, fields[FieldTierNow])
			if tc.wall == "" {
				assert.Empty(t, fields[FieldGateWall])
				assert.NotContains(t, p.Units[0].Moved, "admitted pro")
				return
			}
			assert.Equal(t, tc.wall+" n=2 over 15m0s", fields[FieldGateWall], "the measurement and its source ride on the card")
			assert.Contains(t, p.Units[0].Moved, "admitted pro: gate ./internal/slow measured 22m30s (median of 2 ok takes) over the flash bound 15m0s")
			pr := &Card{ID: id, Row: "new", Col: Ready, Fields: fields}
			_, tier, _, _ := w.s.routeOf(pr, nil, nil)
			assert.Equal(t, cardhdr.RoutePro, tier, "its first deal draws pro, never flash")
			assert.Empty(t, w.s.NextTier(pr), "pro is the top of the ladder")
		})
	}
}
