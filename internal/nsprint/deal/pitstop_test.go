package deal

import (
	"testing"
	"time"
)

// TestPlanSkipsOnlyThePitStoppedSprint: a stop on one sprint leaves the
// other open sprints' cards flowing to the whole fleet.
func TestPlanSkipsOnlyThePitStoppedSprint(t *testing.T) {
	t.Parallel()

	in := Input{Now: time.Now(), Benches: []Bench{{Name: "b", Up: true, Slots: 4}}, Sprints: []Sprint{
		{Name: "stopped", Share: 1, Pitstop: true, Pool: poolCards("stopped", 4)},
		{Name: "running", Share: 1, Pool: poolCards("running", 2)},
	}}
	plan := Plan(in, DefaultRefusedHold)
	if len(plan) != 1 || len(plan[0].Cards) != 2 {
		t.Fatalf("plan = %+v, want the 2 running cards only", plan)
	}
	for _, c := range plan[0].Cards {
		if c.Sprint != "running" {
			t.Fatalf("planned %s/%s from the pit-stopped sprint", c.Sprint, c.Label)
		}
	}
}
