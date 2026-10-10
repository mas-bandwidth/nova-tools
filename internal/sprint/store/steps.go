package store

import (
	"slices"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// The steps of the verbs: each names the tables its plan reads and whether
// the display cells change. The command line and the tests run these.

func tables(ts ...string) []string { return ts }

// AddStep admits primaries; it reads the named needs as well, placed or not,
// and the stream's control card, kept unplaced when the stream was removed in
// this epoch (sprint.RemovedStream), which the add places again (sprint.ComeBack). With Replaces (add --replaces) it is the
// twin's step (sprint.Replace): it reads every table, as the drop of the old
// cards does, and the old cards' records, placed or not.
func AddStep(r sprint.AddReq) Step {
	load := tables(sprint.Work, sprint.Merge, sprint.Fleet)
	if len(r.Replaces) > 0 {
		load = All
	}
	return Step{Named: len(r.IDs) > 0 || len(r.Cards) > 0 || len(r.Replaces) > 0, Args: ArgsOf(r), Verb: "add", Load: load, Mirrors: true,
		Extras: func(s *sprint.Snapshot) map[string][]string {
			ids := append(append([]string(nil), sprint.AddIDs(s, r)...), r.Replaces...)
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
	return Step{Named: len(r.IDs) > 0, Args: ArgsOf(r), Verb: "resolve", Load: tables(sprint.Work),
		Extras: func(s *sprint.Snapshot) map[string][]string {
			return map[string][]string{sprint.Work: sprint.ResolveExtras(s)}
		},
		Plan: func(s *sprint.Snapshot) sprint.Plan { return sprint.Resolve(s, r) }}
}

// TakeStep is a worker taking work cards.
func TakeStep(r sprint.TakeReq) Step {
	// a take for a friend's row reads the friends' seats: her status is FriendStatus, never
	// a control card's (sprint's takeSeat)
	friends := slices.ContainsFunc(sprint.Split(r.As), sprint.IsFriendRow)
	return Step{Named: len(r.IDs) > 0, Args: ArgsOf(r), Verb: "take", Load: tables(sprint.Fleet), Extras: sprint.NamedExtras(sprint.Fleet, r.IDs), Friends: friends, StartsWork: true,
		Plan: func(s *sprint.Snapshot) sprint.Plan { return sprint.Take(s, r) }}
}

// FinishStep is a worker finishing work cards.
func FinishStep(r sprint.FinishReq) Step {
	// a finish that reports what the run spent prices it with the routes alone, the keys
	// a member may read (sprint's cost.go; Step.Prices); a failed finish reads them too,
	// with or without its usage: the second identical failure below its ceiling escalates
	// the card in the finish, by the tiers its routes serve (sprint.NextTier)
	return Step{Named: len(r.IDs) > 0, Args: ArgsOf(r), Verb: "finish", Load: tables(sprint.Fleet, sprint.Readers, sprint.Work), Mirrors: true, Prices: r.Usage != "" || r.Failed, ReportsWork: true,
		Friends: true,
		Extras:  sprint.NamedExtras(sprint.Fleet, r.IDs),
		Plan:    func(s *sprint.Snapshot) sprint.Plan { return sprint.Finish(s, r) }}
}

// ProgressStep is a holder stamping progress on the work cards it works (sprint.Progress).
func ProgressStep(r sprint.ProgressReq) Step {
	return Step{Named: true, Args: ArgsOf(r), Verb: "progress", Load: tables(sprint.Fleet), Extras: sprint.NamedExtras(sprint.Fleet, r.IDs),
		Plan: func(s *sprint.Snapshot) sprint.Plan { return sprint.Progress(s, r) }}
}

// AskStep deals primaries in review to readers.
func AskStep(r sprint.AskReq) Step {
	return Step{Answers: r.Answers, Named: len(r.IDs) > 0, Args: ArgsOf(r), Verb: "ask", Load: tables(sprint.Work, sprint.Readers, sprint.Merge, sprint.Fleet), Readers: true, Routes: true,
		// Every read card id each reader could get at the primaries' attempts,
		// placed or retired: a reader who already has one is not free.
		Extras: func(s *sprint.Snapshot) map[string][]string {
			var ids []string
			for _, c := range s.Work.Column(sprint.Review) {
				for _, rd := range s.Readers.Rows() {
					ids = append(ids, ReadCardIDs(c.ID, c.Int("attempt"), rd)...)
				}
			}
			return map[string][]string{sprint.Readers: ids}
		},
		Plan: func(s *sprint.Snapshot) sprint.Plan { return sprint.Ask(s, r) }}
}

// ReadStep is a reader recording its reads.
func ReadStep(r sprint.ReadReq) Step {
	// a read asked of a friend is a card on her fleet row (the sprint's friendReadAsk): the
	// verb reads the fleet table and closes it as her outbox report does, and brings
	// her row's display cells up to date
	for _, rd := range sprint.Split(r.As) {
		// a read card on a member's row (sprint read_cards.go) is read as hers is: a name
		// that is no reader-<m> is a fleet row
		if _, isReader := sprint.ReaderMachine(rd); sprint.IsFriendRow(rd) || !isReader {
			return Step{Named: len(r.IDs) > 0, Args: ArgsOf(r), Verb: "read", Load: tables(sprint.Fleet, sprint.Work, sprint.Readers), Extras: sprint.NamedExtras(sprint.Fleet, r.IDs), Mirrors: true, Prices: r.Usage != "", StartsWork: r.Begin, ReportsWork: !r.Begin && !r.Return,
				Plan: func(s *sprint.Snapshot) sprint.Plan { return sprint.Read(s, r) }}
		}
	}
	// a read that reports what it spent prices it with the routes alone, the keys a
	// reader may read (sprint's cost.go; Step.Prices)
	return Step{Named: len(r.IDs) > 0, Args: ArgsOf(r), Verb: "read", Load: tables(sprint.Readers, sprint.Work), Extras: sprint.NamedExtras(sprint.Readers, r.IDs), Prices: r.Usage != "", StartsWork: r.Begin, ReportsWork: !r.Begin && !r.Return,
		Plan: func(s *sprint.Snapshot) sprint.Plan { return sprint.Read(s, r) }}
}

// StopReturnStep is an owner's acknowledgement after its process was cancelled.
// It preserves the card's placement and attempt, returning it to that owner.
func StopReturnStep(r sprint.StopReturnReq) Step {
	return Step{Named: true, Args: ArgsOf(r), Verb: "stop-return", Load: tables(sprint.Fleet, sprint.Readers),
		Extras: func(s *sprint.Snapshot) map[string][]string {
			return map[string][]string{sprint.Fleet: r.IDs, sprint.Readers: r.IDs}
		}, RequiresStopped: true, Mirrors: true,
		Plan: func(s *sprint.Snapshot) sprint.Plan { return sprint.StopReturn(s, r) }}
}

// AcceptStep is the coordinator accepting.
func AcceptStep(r sprint.AcceptReq) Step {
	return Step{Answers: r.Answers, Named: len(r.IDs) > 0, Args: ArgsOf(r), Verb: "accept", Load: tables(sprint.Work, sprint.Readers, sprint.Merge, sprint.Fleet),
		Extras: func(s *sprint.Snapshot) map[string][]string {
			return map[string][]string{sprint.Fleet: sprint.ReadCardExtras(s)}
		},
		Plan: func(s *sprint.Snapshot) sprint.Plan { return sprint.Accept(s, r) }}
}

// ReworkStep is the coordinator sending work back with a fix.
func ReworkStep(r sprint.ReworkReq) Step {
	return Step{Answers: r.Answers, Named: len(r.IDs) > 0, Args: ArgsOf(r), Verb: "rework", Load: tables(sprint.Work, sprint.Readers, sprint.Fleet, sprint.Merge), Mirrors: true, Routes: true, Friends: true,
		Extras: func(s *sprint.Snapshot) map[string][]string {
			return map[string][]string{sprint.Fleet: sprint.ReadCardExtras(s)}
		},
		Plan: func(s *sprint.Snapshot) sprint.Plan { return sprint.Rework(s, r) }}
}

// ReturnStep is the coordinator sending merging primaries back to review.
func ReturnStep(r sprint.ReturnReq) Step {
	return Step{Answers: r.Answers, Named: len(r.IDs) > 0, Args: ArgsOf(r), Verb: "return", Load: tables(sprint.Work, sprint.Readers, sprint.Merge),
		Plan: func(s *sprint.Snapshot) sprint.Plan { return sprint.Return(s, r) }}
}

// RedoStep is the coordinator atomically returning, reworking and resuming conflicted cards.
func RedoStep(r sprint.RedoReq) Step {
	return Step{Named: len(r.Sel.IDs) > 0, Args: ArgsOf(r), Verb: "redo", Load: tables(sprint.Work, sprint.Readers, sprint.Fleet, sprint.Merge), Mirrors: true, Routes: true, Friends: true,
		Plan: func(s *sprint.Snapshot) sprint.Plan { return sprint.Redo(s, r) }}
}

// DropStep is the coordinator taking primaries off the table.
func DropStep(r sprint.DropReq) Step {
	return Step{Answers: r.Answers, Named: len(r.IDs) > 0, Args: ArgsOf(r), Verb: "drop", Load: All, Mirrors: true,
		Plan: func(s *sprint.Snapshot) sprint.Plan { return sprint.Drop(s, r) }}
}

// BriefStep is the coordinator replacing the briefs of primaries in place, on a
// running machine as on a stopped one (sprint.Brief); all or none. It reads what a
// rework reads: a card in review edited in place opens its next attempt.
func BriefStep(r sprint.BriefReq) Step {
	return Step{Answers: r.Answers, Named: true, Args: ArgsOf(r), Verb: "brief", Load: tables(sprint.Work, sprint.Readers, sprint.Fleet, sprint.Merge), Mirrors: true,
		Plan: func(s *sprint.Snapshot) sprint.Plan { return sprint.Brief(s, r) }}
}

// MoveStep is the coordinator moving unstarted primaries to another stream,
// on a STOPPED machine (sprint.MoveCards): it reads what add reads, the moved
// cards' needs, placed or not, and the destination's control card.
func MoveStep(r sprint.MoveReq) Step {
	return Step{Named: true, Args: ArgsOf(r), Verb: "move", Load: tables(sprint.Work, sprint.Merge, sprint.Fleet), Mirrors: true,
		Extras: func(s *sprint.Snapshot) map[string][]string {
			ids := append([]string(nil), r.IDs...)
			for _, id := range r.IDs {
				ids = append(ids, sprint.Split(s.Work.Placed(id).F("needs"))...)
			}
			return map[string][]string{sprint.Work: ids, sprint.Merge: {sprint.CtlID(r.Stream)}}
		},
		Plan: func(s *sprint.Snapshot) sprint.Plan { return sprint.MoveCards(s, r) }}
}

// RankStep is the coordinator changing scores.
func RankStep(r sprint.RankReq) Step {
	return Step{Answers: r.Answers, Named: len(r.IDs) > 0, Args: ArgsOf(r), Verb: "rank", Load: All,
		Plan: func(s *sprint.Snapshot) sprint.Plan { return sprint.Rank(s, r) }}
}

// MergeStep is one mechanical merge step of a stream.
func MergeStep(r sprint.MergeReq) Step {
	// a landing takes each card's total from the card itself (sprint's cost.go)
	load := tables(sprint.Merge, sprint.Work)
	if r.Conflict != "" {
		// a conflict reworks the card at the tip: its read cards retire (sprint's landRefused)
		load = tables(sprint.Merge, sprint.Work, sprint.Readers)
	}
	return Step{Args: ArgsOf(r), Verb: "merge", Load: load, Mirrors: true,
		Plan: func(s *sprint.Snapshot) sprint.Plan { return sprint.MergeStep(s, r) }}
}

// SetStep is the coordinator's settings: the sprint's dealt bound and read tier, or
// a stream's read tier (sprint.Set).
func SetStep(r sprint.SetReq) Step {
	return Step{Args: ArgsOf(r), Verb: "set", Load: tables(sprint.Work, sprint.Merge),
		Plan: func(s *sprint.Snapshot) sprint.Plan { return sprint.Set(s, r) }}
}

// PromotedStep records a promotion of the sprint branch into dev (sprint.Promoted).
func PromotedStep(r sprint.PromotedReq) Step {
	return Step{Args: ArgsOf(r), Verb: "promoted", Load: tables(sprint.Work),
		Plan: func(s *sprint.Snapshot) sprint.Plan { return sprint.Promoted(s, r) }}
}

// LandedStep is the coordinator recording work found on the base landed, at a commit git
// put there (sprint.RecordLanded); it reads the named cards' records, placed or not, so a
// dropped one is refused by name.
func LandedStep(r sprint.LandedReq) Step {
	ids := make([]string, len(r.Pins))
	for i, pin := range r.Pins {
		ids[i] = pin.ID
	}
	return Step{Named: true, Args: ArgsOf(r), Verb: "landed", Load: tables(sprint.Merge, sprint.Work), Mirrors: true,
		Extras: func(*sprint.Snapshot) map[string][]string { return map[string][]string{sprint.Work: ids} },
		Plan:   func(s *sprint.Snapshot) sprint.Plan { return sprint.RecordLanded(s, r) }}
}

// MergeWindowStep opens the merge window: landing pauses for its duration, its reason
// shown (sprint.MergeWindowOpen; docs/SPEC-SPRINT.md section 7, the lander's pause).
func MergeWindowStep(r sprint.MergeWindowReq) Step {
	return Step{Args: ArgsOf(r), Verb: "merge-window open", Load: tables(sprint.Merge),
		Plan: func(s *sprint.Snapshot) sprint.Plan { return sprint.MergeWindowOpen(s, r) }}
}

// BalanceStep writes the providers' balances the run loop's poll read, each with its spend
// over the last hour from the work table's cost records, and ends a refused take's rest on
// a payment seen; it rests nothing (sprint.Balance; nova-tools#5199).
func BalanceStep(r sprint.BalanceReq) Step {
	return Step{Args: ArgsOf(r), Verb: "balance", Load: tables(sprint.Work, sprint.Fleet), Routes: true,
		Plan: func(s *sprint.Snapshot) sprint.Plan { return sprint.Balance(s, r) }}
}

// RouteRestStep is the coordinator's rest of a provider's routes or of one route
// (sprint.RestRoutes), and RouteWakeStep its end of one (sprint.WakeRoutes).
func RouteRestStep(r sprint.RouteRestReq) Step {
	return Step{Named: true, Args: ArgsOf(r), Verb: "routes rest", Load: tables(sprint.Fleet), Routes: true,
		Plan: func(s *sprint.Snapshot) sprint.Plan { return sprint.RestRoutes(s, r) }}
}

func RouteWakeStep(r sprint.RouteWakeReq) Step {
	return Step{Named: true, Args: ArgsOf(r), Verb: "routes wake", Load: tables(sprint.Fleet), Routes: true,
		Plan: func(s *sprint.Snapshot) sprint.Plan { return sprint.WakeRoutes(s, r) }}
}

// CostReconcileStep is one cost reconciliation (sprint.CostReconcile): each provider's own
// count of a UTC day beside the sprint's records of it, read from the work table and the
// routes, written to the fleet table's record, and the provider's one gap judgment.
func CostReconcileStep(r sprint.CostReconcileReq) Step {
	return Step{Args: ArgsOf(r), Verb: "cost reconcile", Load: tables(sprint.Work, sprint.Fleet), Routes: true,
		Plan: func(s *sprint.Snapshot) sprint.Plan { return sprint.CostReconcile(s, r) }}
}

// FundedStep is the coordinator's word that a provider was paid: every rest of its funds
// ends (sprint.Funded; nova-tools#5199).
func FundedStep(r sprint.FundedReq) Step {
	return Step{Named: true, Args: ArgsOf(r), Verb: "funded", Load: tables(sprint.Fleet), Routes: true,
		Plan: func(s *sprint.Snapshot) sprint.Plan { return sprint.Funded(s, r) }}
}

// ResumeStep moves a stopped stream again.
func ResumeStep(r sprint.ResumeReq) Step {
	return Step{Answers: r.Answers, Args: ArgsOf(r), Verb: "resume", Load: tables(sprint.Merge, sprint.Work), Mirrors: true,
		Plan: func(s *sprint.Snapshot) sprint.Plan { return sprint.Resume(s, r) }}
}

// FleetStep is a member up or down, the ready queues levelled, or the fleet
// synced to the inventory.
func FleetStep(r sprint.FleetReq) Step {
	// a sync names every member it writes: it applies all or none
	return Step{Named: r.Op == "sync", Args: ArgsOf(r), Verb: "fleet " + r.Op, Load: tables(sprint.Fleet, sprint.Work), Mirrors: true,
		// up, down, hold and level deal and level: a member under its floor is given nothing
		ReadNoRoom: true,
		Plan:       func(s *sprint.Snapshot) sprint.Plan { return sprint.FleetStep(s, r) }}
}

// CIStep records a CI observation.
func CIStep(r sprint.CIReq) Step {
	return Step{Named: len(r.IDs) > 0, Args: ArgsOf(r), Verb: "ci", Load: tables(sprint.Work, sprint.Readers),
		Plan: func(s *sprint.Snapshot) sprint.Plan { return sprint.RecordCI(s, r) }}
}

// ScoreStep is a landed batch's scores (sprint.RecordScores): it reads the sprint row's
// bars with the routes, the landed score's bar among them.
func ScoreStep(r sprint.ScoreReq) Step {
	return Step{Named: len(r.Scores) > 0, Args: ArgsOf(r), Verb: "score", Load: tables(sprint.Work), Routes: true,
		Plan: func(s *sprint.Snapshot) sprint.Plan { return sprint.RecordScores(s, r) }}
}

// ReleaseStep is the coordinator releasing sentinels. The fleet table and the
// records of needs off the table are read so that release tells a wait in flight
// (taken, dropped) from one not started (nova-tools#5096 item c13).
func ReleaseStep(r sprint.ReleaseReq) Step {
	return Step{Answers: r.Answers, Named: len(r.IDs) > 0, Args: ArgsOf(r), Verb: "release", Load: tables(sprint.Work, sprint.Merge, sprint.Fleet), Mirrors: true,
		Extras: func(s *sprint.Snapshot) map[string][]string {
			return map[string][]string{sprint.Work: sprint.ResolveExtras(s)}
		},
		Plan: func(s *sprint.Snapshot) sprint.Plan { return sprint.Release(s, r) }}
}

// AckStep is the coordinator closing judgments it looked at.
func AckStep(r sprint.AckReq) Step {
	// every table: ack is judged by the no-stall rule on the state after it
	return Step{Named: len(r.Notes) > 0, Args: ArgsOf(r), Verb: "ack", Load: All, Extras: tickExtras, Routes: true, Readers: true,
		Plan: func(s *sprint.Snapshot) sprint.Plan { return sprint.Ack(s, r) }}
}

// WaitStep holds a condition the tick keeps until a time (sprint.Wait).
func WaitStep(r sprint.WaitReq) Step {
	if _, ok := sprint.StaleStream(r.Note); ok {
		// a stream's stale judgment: the stream's control card, on the merge table
		return Step{Args: ArgsOf(r), Verb: "wait", Load: []string{sprint.Merge},
			Plan: func(s *sprint.Snapshot) sprint.Plan { return sprint.Wait(s, r) }}
	}
	return Step{Args: ArgsOf(r), Verb: "wait",
		Plan: func(s *sprint.Snapshot) sprint.Plan { return sprint.Wait(s, r) }}
}

// GradeStep is the server's decide lane writing the grade decisions it made on the cards
// still ungraded and never dealt (sprint.Grade): the machine's, as a tick's part is.
func GradeStep(r sprint.GradeReq) Step {
	ids := make([]string, 0, len(r.Grades))
	for id := range r.Grades {
		ids = append(ids, id)
	}
	return Step{Named: true, Args: ArgsOf(r), Verb: "grade", Actor: sprint.MachineActor, Load: tables(sprint.Work), Extras: sprint.NamedExtras(sprint.Work, ids),
		Plan: func(s *sprint.Snapshot) sprint.Plan { return sprint.Grade(s, r) }}
}

// FriendTakeStep takes back a friend's cards she has not started (friend take), or every
// one of them for her hold (friend down).
func FriendTakeStep(r sprint.FriendTakeReq) Step {
	verb := "friend take"
	if r.Hold {
		verb = "friend down"
	}
	return Step{Named: len(r.IDs) > 0, Args: ArgsOf(r), Verb: verb, Load: tables(sprint.Fleet, sprint.Work), Mirrors: true,
		Plan: func(s *sprint.Snapshot) sprint.Plan { return sprint.FriendTake(s, r) }}
}

// FriendGiveStep clears the take-back mark of a friend on the cards named (friend give).
func FriendGiveStep(r sprint.FriendGiveReq) Step {
	return Step{Args: ArgsOf(r), Verb: "friend give", Load: tables(sprint.Fleet, sprint.Work), Mirrors: true,
		Plan: func(s *sprint.Snapshot) sprint.Plan { return sprint.FriendGive(s, r) }}
}

// FriendLevelStep evens the friends' ready queues within each class (friend level).
func FriendLevelStep(r sprint.FriendLevelReq) Step {
	return Step{Args: ArgsOf(r), Verb: "friend level", Load: tables(sprint.Fleet, sprint.Work), Mirrors: true,
		Plan: func(s *sprint.Snapshot) sprint.Plan { return sprint.FriendLevel(s, r) }}
}

// NoteStep writes one happened note and nothing else (friend sync: a friend
// not told of her card): its commit appends it to the log and the inbox.
func NoteStep(verb string, n sprint.Note) Step {
	return Step{Verb: verb, Args: ArgsOf(n), Plan: func(s *sprint.Snapshot) sprint.Plan {
		n.At = s.Now
		return sprint.Plan{Notes: []sprint.Note{n}}
	}}
}

// FriendSyncStateStep records the friend sync loop's standing failure, or its
// clearing, on the fleet table (sprint.FriendSyncStateStep): what the tick's
// one judgment of it is raised on and closed by.
func FriendSyncStateStep(r sprint.FriendSyncStateReq) Step {
	return Step{Args: ArgsOf(r), Verb: "friend sync", Load: tables(sprint.Fleet),
		Plan: func(s *sprint.Snapshot) sprint.Plan { return sprint.FriendSyncStateStep(s, r) }}
}

// UnpinStep drops stored WHO pins atomically with their audit notes. The fleet
// table is loaded so a first attempt returned by friend take can be recognised.
func UnpinStep(r sprint.UnpinReq) Step {
	return Step{Args: ArgsOf(r), Verb: "unpin", Load: tables(sprint.Work, sprint.Fleet),
		Plan: func(s *sprint.Snapshot) sprint.Plan { return sprint.Unpin(s, r) }}
}

// PriorityStep sets a card's priority, or every card of a stream's and the stream's default
// (sprint.SetPriority): it reads the work table and the streams' control cards.
func PriorityStep(r sprint.PriorityReq) Step {
	return Step{Args: ArgsOf(r), Verb: "priority", Load: tables(sprint.Work, sprint.Merge),
		Plan: func(s *sprint.Snapshot) sprint.Plan { return sprint.SetPriority(s, r) }}
}

// RelinkStep re-points the needs of an old card to its twin (sprint.Relink): it reads the
// work table and the old cards' records, placed or not.
func RelinkStep(r sprint.RelinkReq) Step {
	return Step{Named: true, Args: ArgsOf(r), Verb: "relink", Load: tables(sprint.Work),
		Extras: func(*sprint.Snapshot) map[string][]string {
			return map[string][]string{sprint.Work: append(append([]string(nil), r.Old...), r.New)}
		},
		Plan: func(s *sprint.Snapshot) sprint.Plan { return sprint.Relink(s, r) }}
}

// RecutStep re-cuts a card as its twin (sprint.Recut, add --replaces with the tier or the
// brief changed): it reads every table, as the replace does, and the records of the old
// card's needs, the brief's and every id the twin may take, placed or not.
func RecutStep(r sprint.RecutReq) Step {
	return Step{Named: true, Args: ArgsOf(r), Verb: "recut", Load: All, Mirrors: true,
		Extras: func(s *sprint.Snapshot) map[string][]string {
			ids := append([]string{r.ID}, r.Needs...)
			if r.New != "" {
				ids = append(ids, r.New)
			}
			c := s.Work.Placed(r.ID)
			if c != nil && r.New == "" {
				ids = append(ids, sprint.TwinIDs(c)...)
			}
			out := map[string][]string{sprint.Work: ids}
			if c != nil {
				out[sprint.Work] = append(out[sprint.Work], sprint.Split(c.F("needs"))...)
				out[sprint.Merge] = []string{sprint.CtlID(c.Row)}
			}
			return out
		},
		Plan: func(s *sprint.Snapshot) sprint.Plan { return sprint.Recut(s, r) }}
}

// FriendReturnStep is friend reconcile returning a friend's abandoned cards to ready
// (sprint.FriendReturn; docs/SPEC-SPRINT.md section 1, friend reconcile): all or none.
func FriendReturnStep(r sprint.FriendReturnReq) Step {
	ids := make([]string, len(r.Cards))
	for i, c := range r.Cards {
		ids[i] = c.ID
	}
	return Step{Named: true, Args: ArgsOf(r), Verb: "friend reconcile", Load: tables(sprint.Fleet, sprint.Work), Mirrors: true,
		Extras: sprint.NamedExtras(sprint.Fleet, ids),
		Plan:   func(s *sprint.Snapshot) sprint.Plan { return sprint.FriendReturn(s, r) }}
}

// ServerRestartStep is the server restart plan (sprint.ServerRestart; docs/SPEC-SPRINT.md
// section 6): on server start, keep every in-flight read whose lease is live,
// and only take back reads whose lease has lapsed.
func ServerRestartStep() Step {
	return Step{
		Verb: "restart",
		Load: tables(sprint.Readers, sprint.Work),
		Plan: sprint.ServerRestart,
	}
}
