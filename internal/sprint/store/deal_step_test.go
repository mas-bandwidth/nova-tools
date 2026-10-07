package store

import "github.com/nova-tools/internal/sprint"

// DealStep cuts and deals work cards by hand, the step the tick's deal replaced
// (sprint.TickDeal): a test that needs exact queues deals with it.
func DealStep(r sprint.DealReq) Step {
	return Step{Args: ArgsOf(r), Verb: "deal", Load: tables(sprint.Work, sprint.Fleet, sprint.Merge), Mirrors: true, Routes: true,
		Plan: func(s *sprint.Snapshot) sprint.Plan { return sprint.Deal(s, r) }}
}
