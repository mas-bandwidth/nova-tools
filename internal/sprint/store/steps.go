package store

import "github.com/mas-bandwidth/nova-tools/internal/sprint"

// The steps of the verbs: each names the tables its plan reads and whether
// the display cells change. The command line and the tests run these.

func tables(ts ...string) []string { return ts }

// AddStep admits primaries; it reads the named needs as well, placed or not,
// and the stream's control card, kept unplaced when the stream was removed in
// this epoch (sprint.RemovedStream).
func AddStep(r sprint.AddReq) Step {
	return Step{Named: len(r.IDs) > 0 || len(r.Cards) > 0, Args: ArgsOf(r), Verb: "add", Load: tables(sprint.Work, sprint.Merge, sprint.Fleet), Mirrors: true,
		Extras: func(s *sprint.Snapshot) map[string][]string {
			ids := append([]string(nil), sprint.AddIDs(s, r)...)
			needs := append([]string(nil), r.Needs...)
			for _, c := range r.Cards {
				needs = append(needs, c.Needs...)
			}
			return map[string][]string{sprint.Work: append(ids, needs...), sprint.Merge: {sprint.CtlID(r.Stream)}}
		},
		Plan: func(s *sprint.Snapshot) sprint.Plan { return sprint.Add(s, r) }}
}

// AddEachStep admits primaries into several streams in one step; named cards
// (ids, or cards with their briefs) are all or none across every stream.
func AddEachStep(rs []sprint.AddReq) Step {
	named := false
	for _, r := range rs {
		named = named || len(r.IDs) > 0 || len(r.Cards) > 0
	}
	return Step{Named: named, Args: ArgsOf(rs), Verb: "add", Load: tables(sprint.Work, sprint.Merge, sprint.Fleet), Mirrors: true,
		Extras: func(s *sprint.Snapshot) map[string][]string {
			var ids, ctls []string
			for _, r := range rs {
				ids = append(append(ids, sprint.AddIDs(s, r)...), r.Needs...)
				ctls = append(ctls, sprint.CtlID(r.Stream))
			}
			return map[string][]string{sprint.Work: ids, sprint.Merge: ctls}
		},
		Plan: func(s *sprint.Snapshot) sprint.Plan { return sprint.AddEach(s, rs) }}
}

// ResolveStep moves waiting primaries whose needs landed.
func ResolveStep(r sprint.ResolveReq) Step {
	return Step{Named: len(r.Sel.IDs) > 0, Args: ArgsOf(r), Verb: "resolve", Load: tables(sprint.Work),
		Extras: func(s *sprint.Snapshot) map[string][]string {
			return map[string][]string{sprint.Work: sprint.ResolveExtras(s)}
		},
		Plan: func(s *sprint.Snapshot) sprint.Plan { return sprint.Resolve(s, r) }}
}

// DealStep cuts and deals work cards.
func DealStep(r sprint.DealReq) Step {
	return Step{Args: ArgsOf(r), Verb: "deal", Load: tables(sprint.Work, sprint.Fleet, sprint.Merge), Mirrors: true, Routes: true,
		Plan: func(s *sprint.Snapshot) sprint.Plan { return sprint.Deal(s, r) }}
}

// TakeStep is a worker taking work cards.
func TakeStep(r sprint.TakeReq) Step {
	return Step{Named: len(r.Sel.IDs) > 0, Args: ArgsOf(r), Verb: "take", Load: tables(sprint.Fleet), Extras: sprint.NamedExtras(sprint.Fleet, r.IDs),
		Plan: func(s *sprint.Snapshot) sprint.Plan { return sprint.Take(s, r) }}
}

// FinishStep is a worker finishing work cards.
func FinishStep(r sprint.FinishReq) Step {
	// a finish that reports what the run spent prices it with the routes alone, the keys
	// a member may read (sprint's cost.go; Step.Prices)
	return Step{Named: len(r.Sel.IDs) > 0, Args: ArgsOf(r), Verb: "finish", Load: tables(sprint.Fleet, sprint.Readers, sprint.Work), Mirrors: true, Prices: r.Usage != "",
		Extras: sprint.NamedExtras(sprint.Fleet, r.IDs),
		Plan:   func(s *sprint.Snapshot) sprint.Plan { return sprint.Finish(s, r) }}
}

// AskStep deals primaries in review to readers.
func AskStep(r sprint.AskReq) Step {
	return Step{Named: len(r.Sel.IDs) > 0, Args: ArgsOf(r), Verb: "ask", Load: tables(sprint.Work, sprint.Readers, sprint.Merge, sprint.Fleet), Readers: true, Routes: true,
		// Every read card id each reader could get at the primaries' attempts,
		// placed or retired: a reader who already has one is not free.
		Extras: func(s *sprint.Snapshot) map[string][]string {
			var ids []string
			for _, c := range s.Work.Column(sprint.Review) {
				for _, rd := range s.Readers.Rows() {
					ids = append(ids, sprint.ReadCardID(c.ID, c.Int("attempt"), rd))
				}
			}
			return map[string][]string{sprint.Readers: ids}
		},
		Plan: func(s *sprint.Snapshot) sprint.Plan { return sprint.Ask(s, r) }}
}

// ReadStep is a reader recording its reads.
func ReadStep(r sprint.ReadReq) Step {
	// a read that reports what it spent prices it with the routes alone, the keys a
	// reader may read (sprint's cost.go; Step.Prices)
	return Step{Named: len(r.Sel.IDs) > 0, Args: ArgsOf(r), Verb: "read", Load: tables(sprint.Readers, sprint.Work), Extras: sprint.NamedExtras(sprint.Readers, r.IDs), Prices: r.Usage != "",
		Plan: func(s *sprint.Snapshot) sprint.Plan { return sprint.Read(s, r) }}
}

// AcceptStep is the coordinator accepting.
func AcceptStep(r sprint.AcceptReq) Step {
	return Step{Named: len(r.Sel.IDs) > 0, Args: ArgsOf(r), Verb: "accept", Load: tables(sprint.Work, sprint.Readers, sprint.Merge),
		Plan: func(s *sprint.Snapshot) sprint.Plan { return sprint.Accept(s, r) }}
}

// ReworkStep is the coordinator sending work back with a fix.
func ReworkStep(r sprint.ReworkReq) Step {
	return Step{Named: len(r.Sel.IDs) > 0, Args: ArgsOf(r), Verb: "rework", Load: tables(sprint.Work, sprint.Readers, sprint.Fleet, sprint.Merge), Mirrors: true, Routes: true,
		Plan: func(s *sprint.Snapshot) sprint.Plan { return sprint.Rework(s, r) }}
}

// ReturnStep is the coordinator sending merging primaries back to review.
func ReturnStep(r sprint.ReturnReq) Step {
	return Step{Named: len(r.Sel.IDs) > 0, Args: ArgsOf(r), Verb: "return", Load: tables(sprint.Work, sprint.Readers, sprint.Merge),
		Plan: func(s *sprint.Snapshot) sprint.Plan { return sprint.Return(s, r) }}
}

// DropStep is the coordinator taking primaries off the table.
func DropStep(r sprint.DropReq) Step {
	return Step{Named: len(r.Sel.IDs) > 0, Args: ArgsOf(r), Verb: "drop", Load: All, Mirrors: true,
		Plan: func(s *sprint.Snapshot) sprint.Plan { return sprint.Drop(s, r) }}
}

// BriefStep is the coordinator replacing the brief of a primary that has not
// started, on a STOPPED machine (sprint.Brief).
func BriefStep(r sprint.BriefReq) Step {
	return Step{Named: true, Args: ArgsOf(r), Verb: "brief", Load: tables(sprint.Work),
		Plan: func(s *sprint.Snapshot) sprint.Plan { return sprint.Brief(s, r) }}
}

// RankStep is the coordinator changing scores.
func RankStep(r sprint.RankReq) Step {
	return Step{Named: len(r.IDs) > 0, Args: ArgsOf(r), Verb: "rank", Load: All,
		Plan: func(s *sprint.Snapshot) sprint.Plan { return sprint.Rank(s, r) }}
}

// MergeStep is one mechanical merge step of a stream.
func MergeStep(r sprint.MergeReq) Step {
	// a landing takes each card's total from the card itself (sprint's cost.go)
	return Step{Args: ArgsOf(r), Verb: "merge", Load: tables(sprint.Merge, sprint.Work), Mirrors: true,
		Plan: func(s *sprint.Snapshot) sprint.Plan { return sprint.MergeStep(s, r) }}
}

// ResumeStep moves a stopped stream again.
func ResumeStep(r sprint.ResumeReq) Step {
	return Step{Args: ArgsOf(r), Verb: "resume", Load: tables(sprint.Merge, sprint.Work), Mirrors: true,
		Plan: func(s *sprint.Snapshot) sprint.Plan { return sprint.Resume(s, r) }}
}

// FleetStep is a member up or down, the ready queues levelled, or the fleet
// synced to the inventory.
func FleetStep(r sprint.FleetReq) Step {
	// a sync names every member it writes: it applies all or none
	return Step{Named: r.Op == "sync", Args: ArgsOf(r), Verb: "fleet " + r.Op, Load: tables(sprint.Fleet, sprint.Work), Mirrors: true,
		Plan: func(s *sprint.Snapshot) sprint.Plan { return sprint.FleetStep(s, r) }}
}

// CIStep records a CI observation.
func CIStep(r sprint.CIReq) Step {
	return Step{Named: len(r.Sel.IDs) > 0, Args: ArgsOf(r), Verb: "ci", Load: tables(sprint.Work, sprint.Readers),
		Plan: func(s *sprint.Snapshot) sprint.Plan { return sprint.RecordCI(s, r) }}
}

// ReleaseStep is the coordinator releasing reached sentinels.
func ReleaseStep(r sprint.ReleaseReq) Step {
	return Step{Named: len(r.IDs) > 0, Args: ArgsOf(r), Verb: "release", Load: tables(sprint.Work, sprint.Merge), Mirrors: true,
		Plan: func(s *sprint.Snapshot) sprint.Plan { return sprint.Release(s, r) }}
}

// SentinelsDueStep marks reached every sentinel whose needs have all landed.
func SentinelsDueStep(who string) Step {
	return Step{Args: ArgsOf(who), Verb: "sentinels", Actor: who, Load: tables(sprint.Work),
		Plan: func(s *sprint.Snapshot) sprint.Plan { return sprint.SentinelsDue(s, who) }}
}

// AckStep is the coordinator closing judgments it looked at.
func AckStep(r sprint.AckReq) Step {
	// every table: ack is judged by the no-stall rule on the state after it
	return Step{Named: len(r.Notes) > 0, Args: ArgsOf(r), Verb: "ack", Load: All, Extras: tickExtras, Routes: true, Readers: true,
		Plan: func(s *sprint.Snapshot) sprint.Plan { return sprint.Ack(s, r) }}
}

// WaitStep holds a condition the tick keeps until a time (sprint.Wait).
func WaitStep(r sprint.WaitReq) Step {
	return Step{Args: ArgsOf(r), Verb: "wait",
		Plan: func(s *sprint.Snapshot) sprint.Plan { return sprint.Wait(s, r) }}
}
