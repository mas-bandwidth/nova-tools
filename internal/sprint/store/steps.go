package store

import "github.com/mas-bandwidth/nova-tools/internal/sprint"

// The steps of the verbs: each names the tables its plan reads and whether
// the display cells change. The command line and the tests run these.

func tables(ts ...string) []string { return ts }

// AddStep admits primaries.
func AddStep(r sprint.AddReq) Step {
	return Step{Verb: "add", Load: tables(sprint.Work, sprint.Merge), Mirrors: true,
		Extras: func(s *sprint.Snapshot) map[string][]string {
			return map[string][]string{sprint.Work: sprint.AddIDs(s, r)}
		},
		Plan: func(s *sprint.Snapshot) sprint.Plan { return sprint.Add(s, r) }}
}

// ResolveStep moves waiting primaries whose needs landed.
func ResolveStep(r sprint.ResolveReq) Step {
	return Step{Verb: "resolve", Load: tables(sprint.Work),
		Extras: func(s *sprint.Snapshot) map[string][]string {
			return map[string][]string{sprint.Work: sprint.ResolveExtras(s)}
		},
		Plan: func(s *sprint.Snapshot) sprint.Plan { return sprint.Resolve(s, r) }}
}

// StartStep cuts and deals work cards.
func StartStep(r sprint.StartReq) Step {
	return Step{Verb: "start", Load: tables(sprint.Work, sprint.Fleet), Mirrors: true,
		Plan: func(s *sprint.Snapshot) sprint.Plan { return sprint.Start(s, r) }}
}

// TakeStep is a worker taking work cards.
func TakeStep(r sprint.TakeReq) Step {
	return Step{Verb: "take", Load: tables(sprint.Fleet), Extras: sprint.NamedExtras(sprint.Fleet, r.IDs),
		Plan: func(s *sprint.Snapshot) sprint.Plan { return sprint.Take(s, r) }}
}

// FinishStep is a worker finishing work cards.
func FinishStep(r sprint.FinishReq) Step {
	return Step{Verb: "finish", Load: tables(sprint.Fleet, sprint.Readers, sprint.Work), Mirrors: true,
		Extras: sprint.NamedExtras(sprint.Fleet, r.IDs),
		Plan:   func(s *sprint.Snapshot) sprint.Plan { return sprint.Finish(s, r) }}
}

// AskStep deals primaries in review to readers.
func AskStep(r sprint.AskReq) Step {
	return Step{Verb: "ask", Load: tables(sprint.Work, sprint.Readers),
		Plan: func(s *sprint.Snapshot) sprint.Plan { return sprint.Ask(s, r) }}
}

// ReadStep is a reader recording its reads.
func ReadStep(r sprint.ReadReq) Step {
	return Step{Verb: "read", Load: tables(sprint.Readers, sprint.Work), Extras: sprint.NamedExtras(sprint.Readers, r.IDs),
		Plan: func(s *sprint.Snapshot) sprint.Plan { return sprint.Read(s, r) }}
}

// AcceptStep is the coordinator accepting.
func AcceptStep(r sprint.AcceptReq) Step {
	return Step{Verb: "accept", Load: tables(sprint.Work, sprint.Readers, sprint.Merge),
		Plan: func(s *sprint.Snapshot) sprint.Plan { return sprint.Accept(s, r) }}
}

// ReworkStep is the coordinator sending work back with a fix.
func ReworkStep(r sprint.ReworkReq) Step {
	return Step{Verb: "rework", Load: tables(sprint.Work, sprint.Readers, sprint.Fleet), Mirrors: true,
		Plan: func(s *sprint.Snapshot) sprint.Plan { return sprint.Rework(s, r) }}
}

// ReturnStep is the coordinator sending merging primaries back to review.
func ReturnStep(r sprint.ReturnReq) Step {
	return Step{Verb: "return", Load: tables(sprint.Work, sprint.Merge),
		Plan: func(s *sprint.Snapshot) sprint.Plan { return sprint.Return(s, r) }}
}

// DropStep is the coordinator taking primaries off the table.
func DropStep(r sprint.DropReq) Step {
	return Step{Verb: "drop", Load: All, Mirrors: true,
		Plan: func(s *sprint.Snapshot) sprint.Plan { return sprint.Drop(s, r) }}
}

// RankStep is the coordinator changing scores.
func RankStep(r sprint.RankReq) Step {
	return Step{Verb: "rank", Load: All,
		Plan: func(s *sprint.Snapshot) sprint.Plan { return sprint.Rank(s, r) }}
}

// MergeStep is one mechanical merge step of a stream.
func MergeStep(r sprint.MergeReq) Step {
	return Step{Verb: "merge", Load: tables(sprint.Merge, sprint.Work), Mirrors: true,
		Plan: func(s *sprint.Snapshot) sprint.Plan { return sprint.MergeStep(s, r) }}
}

// ResumeStep moves a stopped stream again.
func ResumeStep(r sprint.ResumeReq) Step {
	return Step{Verb: "resume", Load: tables(sprint.Merge, sprint.Work), Mirrors: true,
		Plan: func(s *sprint.Snapshot) sprint.Plan { return sprint.Resume(s, r) }}
}

// FleetStep is a member up or down, or the ready queues levelled.
func FleetStep(r sprint.FleetReq) Step {
	return Step{Verb: "fleet " + r.Op, Load: tables(sprint.Fleet, sprint.Work), Mirrors: true,
		Plan: func(s *sprint.Snapshot) sprint.Plan { return sprint.FleetStep(s, r) }}
}

// CIStep records a CI observation.
func CIStep(r sprint.CIReq) Step {
	return Step{Verb: "ci", Load: tables(sprint.Work),
		Plan: func(s *sprint.Snapshot) sprint.Plan { return sprint.RecordCI(s, r) }}
}

// AckStep is the coordinator closing judgments it looked at.
func AckStep(r sprint.AckReq) Step {
	return Step{Verb: "ack", Load: tables(sprint.Work, sprint.Readers, sprint.Merge),
		Plan: func(s *sprint.Snapshot) sprint.Plan { return sprint.Ack(s, r) }}
}
