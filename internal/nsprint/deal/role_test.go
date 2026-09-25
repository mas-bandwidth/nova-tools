package deal

import (
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/benchrole"
)

// nova-tools #3634: the pass plans no swarm card onto a bench whose registry
// role is friends, however free it is; the fleet bench beside it takes them.
func TestPlanDealsNothingToAFriendsBench(t *testing.T) {
	now := time.Unix(1000, 0)
	in := Input{
		Now: now,
		Benches: []Bench{
			{Name: "studio", Up: true, Slots: 32, Role: benchrole.Friends},
			{Name: "hulk", Up: true, Slots: 2, Role: benchrole.Fleet},
			{Name: "vision", Up: true, Slots: 1},
		},
		Sprints: []Sprint{{Name: "s1", Pool: []Card{
			{Sprint: "s1", Label: "a", Age: 1}, {Sprint: "s1", Label: "b", Age: 2},
			{Sprint: "s1", Label: "c", Age: 3}, {Sprint: "s1", Label: "d", Age: 4, Bench: "studio"},
		}}},
	}
	got := map[string]int{}
	for _, b := range Plan(in, time.Minute) {
		got[b.Bench.Name] += len(b.Cards)
	}
	if got["studio"] != 0 || got["hulk"] != 2 || got["vision"] != 1 {
		t.Fatalf("plan %v; want studio 0 (friends), hulk 2, vision 1 (no role reads as fleet)", got)
	}
}
