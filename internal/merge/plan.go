package merge

import "strings"

// plan is classify with every write taken out: no build, no re-merge, no ready, no
// publication. It is what dry-run and status see.
//
// It is a SEPARATE function rather than a flag on classify, for the reason the spec gives
// for dry-run being a verb rather than a flag: a mode a caller can leave on or off by
// accident on the one command that merges is worth more than the duplication, and
// "nothing in dry-run's code path can reach the mutating helper" is a property a test can
// pin over a call graph and never over a boolean.
func (p *Pass) plan(e *Entry, baseSHA string) Classification {
	c := Classification{State: StateUnknown}
	for _, pr := range p.Problems {
		if pr.Entry == e.ID() {
			c.State, c.Detail = StateBlocked, "malformed_record "+pr.File
			return c
		}
	}
	if e.IsPR() {
		pr, err := p.Host.PR(e.PR)
		if err != nil {
			return c
		}
		c.Author, c.HeadRef, c.URL, c.Subject = pr.Author, pr.HeadRef, pr.URL, pr.Subject
		e.OID, e.Head = pr.HeadOID, pr.HeadRef
		switch {
		case pr.Base != p.State.Base:
			c.State = StateWrongBase
			return c
		case pr.Fork:
			c.State = StateFork
			return c
		case strings.EqualFold(pr.Mergeable, "CONFLICTING"):
			c.State = StateConflicting
			return c
		}
	} else {
		oid, err := p.Host.BranchOID(e.Branch)
		if err != nil {
			return c
		}
		e.OID, e.Head, c.HeadRef = oid, e.Branch, e.Branch
	}
	checks, err := p.Host.Checks(e.OID)
	if err != nil {
		return c
	}
	c.Checks = checks
	c.Reads = EvaluateReads(e, c.Author)
	c.Gate = StandOfGates(p.State.Gates, e.ID(), e.OID, baseSHA)
	c.State, c.Admitted = standing(p.State.Base, checks, c.Reads, c.Gate, p.basePending)
	return c
}

// standing is the state and the admission, as a function of the four things they are a
// function of: the base, the hosted checks, the reads and the gate records. classify's
// own switch is the same decision written out with its side effects around it, and this
// is the version status and dry-run share.
func standing(base string, checks Checks, reads Standing, gate GateStand, basePending bool) (state, admitted string) {
	if reads.Held {
		return StateHold, ""
	}
	onMain := base == "main"
	headGateGreen := (gate.Kind == "head" && gate.Record != nil && gate.Record.Verdict == "green") || gate.Green()
	switch {
	case onMain && checks.Verdict() == "RED":
		return StateRed, ""
	case onMain && checks.Verdict() == "GREEN":
		admitted = "hosted"
	case headGateGreen:
		admitted = "gate"
	}
	if gate.Red() {
		return StateRed, admitted
	}
	switch {
	case admitted == "":
		return StatePending, ""
	case !gate.Green():
		return StateNeedsGate, admitted
	case !reads.Satisfied:
		return StateNeedsRead, admitted
	case basePending:
		return StatePending, admitted
	}
	return StateMergeableGreen, admitted
}
