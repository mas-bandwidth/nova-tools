package merge

import (
	"fmt"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// Classification is one entry's standing this pass: what the host said, what the records
// said, and the one state out of the closed set it lands in.
type Classification struct {
	State    string
	Detail   string
	Checks   Checks
	Reads    Standing
	Gate     GateStand
	Admitted string // hosted | gate | "" -- WHICH CANDIDATE EVIDENCE let it reach the gate
	Author   string
	HeadRef  string
	URL      string
	Subject  string
	Built    *Built
}

// classify places one entry in the closed state set. A pass that cannot place an entry in
// one of them prints RUN STOPPED rather than inventing a fourteenth.
func (p *Pass) classify(e *Entry, baseSHA string, res *Result) Classification {
	c := Classification{State: StateUnknown}

	// The fold's refusals first: an unreadable record may be the hold or the newer red,
	// so its entry is blocked for the pass whatever its other records say. Never skipped
	// and never repaired.
	for _, pr := range p.Problems {
		if pr.Entry == e.ID() {
			c.State, c.Detail = StateBlocked, "malformed_record "+pr.File
			fmt.Fprintf(p.Stderr, "MERGE BLOCKED entry=%s reason=malformed_record file=%s: %s\n",
				oneline.Field(e.ID()), oneline.Field(pr.File), oneline.Escape(oneline.Cap(pr.Reason, oneline.TailBytes)))
			p.record(e, c)
			return c
		}
	}

	if e.IsPR() {
		pr, err := p.Host.PR(e.PR)
		if err != nil {
			c.State, c.Detail = StateUnknown, oneline.Cap(err.Error(), oneline.TailBytes)
			fmt.Fprintf(p.Stderr, "RUN STOPPED entry=%s: %s\n", oneline.Field(e.ID()), oneline.Escape(c.Detail))
			res.Stopped = true
			p.record(e, c)
			return c
		}
		c.Author, c.HeadRef, c.URL, c.Subject = pr.Author, pr.HeadRef, pr.URL, pr.Subject
		e.OID = pr.HeadOID
		e.Head = pr.HeadRef
		switch {
		case pr.Base != p.State.Base:
			c.State = StateWrongBase
			c.Detail = fmt.Sprintf("its base is %s and this lane's base is %s; a merge into the wrong branch is not recoverable by a revert", pr.Base, p.State.Base)
			p.stopped(e, c, res)
			return c
		case pr.Fork:
			c.State = StateFork
			c.Detail = "its head branch lives in a fork this lane cannot push to"
			p.stopped(e, c, res)
			return c
		case strings.EqualFold(pr.Mergeable, "CONFLICTING"):
			return p.remerge(e, pr, baseSHA, res)
		}
	} else {
		oid, err := p.Host.BranchOID(e.Branch)
		if err != nil {
			c.State, c.Detail = StateUnknown, oneline.Cap(err.Error(), oneline.TailBytes)
			fmt.Fprintf(p.Stderr, "RUN STOPPED entry=%s: %s\n", oneline.Field(e.ID()), oneline.Escape(c.Detail))
			res.Stopped = true
			p.record(e, c)
			return c
		}
		e.OID = oid
		e.Head = e.Branch
		c.HeadRef = e.Branch
	}

	checks, err := p.Host.Checks(e.OID)
	if err != nil {
		c.State, c.Detail = StateUnknown, oneline.Cap(err.Error(), oneline.TailBytes)
		p.stopped(e, c, res)
		return c
	}
	c.Checks = checks
	e.Green, e.Pending, e.Red = checks.Green, checks.Pending, checks.Red
	c.Reads = EvaluateReads(e, c.Author)
	c.Gate = StandOfGates(p.State.Gates, e.ID(), e.OID, baseSHA)

	// ONE STATEMENT of the decision, shared with dry-run and status (lesson 113: a
	// matching rule has exactly one statement). What is here and not in standing() is
	// the side effects: the detail sentences, the build, and the record.
	state, admitted := standing(p.State.Base, checks, c.Reads, c.Gate, p.basePending)
	c.Admitted = admitted
	c.State = state
	switch state {
	case StateHold:
		// A hold blocks, and nothing outvotes it. Not three approves, not a green gate,
		// not a deadline. It is removed by the line that recorded it recording an
		// approve for the same head.
		c.Detail = "a hold is recorded for this head; it is removed by the line that recorded it recording an approve for the same head"
	case StateRed:
		if c.Gate.Red() {
			c.Detail = fmt.Sprintf("the newest gate record for head %s against base %s is red (%s)",
				Short(e.OID), Short(baseSHA), c.Gate.Record.Summary)
		} else {
			// On main a hosted RED stops the entry whatever any gate says. A local
			// green is permission to STOP WAITING, never permission to IGNORE.
			c.Detail = fmt.Sprintf("hosted red on %s: %s", Short(e.OID), checks.Names())
		}
	case StatePending:
		c.Detail = "waiting on evidence for this head"
	case StateNeedsRead:
		c.Detail = "needs an approve, for this head, by a line that is not its author"
	}
	// Candidate evidence and no record for (oid, base sha): build the integration commit
	// once, keep it under refs/nova-merge/integration/, and name the gate command. THIS
	// IS THE WHOLE OF WHAT #942 TAUGHT: the head was proven, the merge was not. It runs
	// for NEEDS-READ as well as NEEDS-GATE, so that the gate round trip and the reader's
	// round trip happen beside each other rather than one after the other.
	if (state == StateNeedsGate || state == StateNeedsRead) && !c.Gate.Green() {
		p.build(e, &c, baseSHA, res)
	}
	p.record(e, c)
	return c
}

// build makes the integration commit, once, in the lane's clone, and prints RUN BUILT
// with all three shas. The object built here is the object the gate proves and the object
// publication moves the base to; nothing rebuilds it on the way to the push.
func (p *Pass) build(e *Entry, c *Classification, baseSHA string, res *Result) {
	// A build that cannot happen SAYS SO on its own line. It used to set a detail
	// nothing printed, which is a pass that waits and never tells the reader what it is
	// waiting on -- the failure ONBOARDING point 2 is about, inside the pass.
	if _, err := p.Clone.Run("fetch", p.Remote, c.HeadRef); err != nil {
		c.Detail = oneline.Cap(err.Error(), oneline.TailBytes)
		p.stopped(e, *c, res)
		return
	}
	built, err := BuildIntegration(p.Clone, EntryDirName(e.ID()), baseSHA, e.OID,
		IntegrationMessage(e.ID(), p.State.Base, c.Subject))
	if err != nil {
		c.Detail = oneline.Cap(err.Error(), oneline.TailBytes)
		p.stopped(e, *c, res)
		return
	}
	if len(built.Conflicts) > 0 {
		// A build that would need a resolved file is aborted and the entry is BLOCKED
		// exactly as a host CONFLICTING is (rule 7).
		c.State = StateBlocked
		c.Detail = BlockedDetail(p.State.Base, built.Conflicts)
		p.blockedLine(e, built.Conflicts)
		res.Blocked++
		res.Stopped = true
		return
	}
	c.Built = built
	fmt.Fprintf(p.Stdout, "RUN BUILT entry=%s head=%s base=%s merge=%s ref=%s\n",
		oneline.Field(e.ID()), oneline.Field(Short(e.OID)), oneline.Field(Short(baseSHA)),
		oneline.Field(Short(built.Merge)), oneline.Field(built.Ref))
	res.Note = fmt.Sprintf("gate this: --head %s --base-sha %s --merge %s --from %s/repo, then nova-merge gate --lane %s %s --head %s --base-sha %s --merge %s --verdict green|red --summary <path>",
		e.OID, baseSHA, built.Merge, p.Lane, p.Lane, selector(e), e.OID, baseSHA, built.Merge)
}

// selector is how a verb names this entry on a command line: --pr <n> or --branch <name>.
func selector(e *Entry) string {
	if e.IsPR() {
		return fmt.Sprintf("--pr %d", e.PR)
	}
	return "--branch " + e.Branch
}

// blockedLine is rule 17: one RUN BLOCKED line carrying the exact hand command -- the
// clone, the fetch, the merge, the conflicting files and the push, in the order a hand
// runs them. Four Java conflicts each cost a child an afternoon working out that command.
func (p *Pass) blockedLine(e *Entry, files []string) {
	fmt.Fprintf(p.Stderr, "RUN BLOCKED entry=%s head=%s files=%d: %s\n",
		oneline.Field(e.ID()), oneline.Field(Short(e.OID)), len(files),
		oneline.Escape(oneline.Cap(HandCommand(p.State.Repo, p.State.Base, e.Head, files), oneline.TailBytes)))
}

// remerge is rule 3: after a merge the base has moved, and an entry that CONFLICTS with
// it gets no CI at all -- the host will not run checks on a pull request it cannot merge
// -- so it is stuck until somebody moves it. The base is re-merged into exactly the
// entries that conflict, and into nothing else.
//
// The storm of 2026-09-11 12:40Z is the alternative: 26 entries each got a new commit,
// each new commit cancelled and restarted that entry's whole check matrix, 70 jobs per
// entry were queued at once, and the runner pool was jammed for an hour.
func (p *Pass) remerge(e *Entry, pr PR, baseSHA string, res *Result) Classification {
	c := Classification{State: StateConflicting, Author: pr.Author, HeadRef: pr.HeadRef, URL: pr.URL}
	e.OID, e.Head = pr.HeadOID, pr.HeadRef
	if e.State == StateBlocked && e.OID != "" && strings.Contains(e.Detail, "conflicts with") {
		// The entry stays BLOCKED until a NEW head arrives; no pass retries it.
		c.State, c.Detail = StateBlocked, e.Detail
		res.Blocked++
		res.Stopped = true
		p.record(e, c)
		return c
	}
	if _, err := p.Clone.Run("fetch", p.Remote, pr.HeadRef); err != nil {
		c.Detail = oneline.Cap(err.Error(), oneline.TailBytes)
		p.stopped(e, c, res)
		return c
	}
	if _, err := p.Clone.Run("reset", "--hard"); err != nil {
		c.Detail = oneline.Cap(err.Error(), oneline.TailBytes)
		p.stopped(e, c, res)
		return c
	}
	if _, err := p.Clone.Run("checkout", "-B", "nova-merge-remerge", pr.HeadOID); err != nil {
		c.Detail = oneline.Cap(err.Error(), oneline.TailBytes)
		p.stopped(e, c, res)
		return c
	}
	pushed := false
	files := []string(nil)
	result := "clean"
	if _, err := p.Clone.Run("merge", "--no-ff", "-m", "Merge "+p.State.Base+" into "+pr.HeadRef, baseSHA); err != nil {
		var listErr error
		files, listErr = ConflictFiles(p.Clone)
		if listErr != nil || len(files) == 0 {
			c.Detail = oneline.Cap(err.Error(), oneline.TailBytes)
			p.stopped(e, c, res)
			return c
		}
		if abortErr := AbortMerge(p.Clone); abortErr != nil {
			c.Detail = oneline.Cap(abortErr.Error(), oneline.TailBytes)
			p.stopped(e, c, res)
			return c
		}
		result = "blocked"
	} else if _, err := p.Clone.Run("push", p.Remote, "HEAD:refs/heads/"+pr.HeadRef); err == nil {
		pushed = true
	}
	fmt.Fprintf(p.Stdout, "RUN REMERGE entry=%s base=%s result=%s pushed=%t files=%d\n",
		oneline.Field(e.ID()), oneline.Field(p.State.Base), result, pushed, len(files))
	if result == "blocked" {
		c.State = StateBlocked
		c.Detail = BlockedDetail(p.State.Base, files)
		p.blockedLine(e, files)
		res.Blocked++
		res.Stopped = true
		p.record(e, c)
		return c
	}
	// The lane MOVES ON: one entry that needs a hand does not stop the other thirty-two,
	// and a re-merged entry waits for the checks its new commit starts.
	c.State = StateRemerged
	p.record(e, c)
	return c
}

func (p *Pass) stopped(e *Entry, c Classification, res *Result) {
	fmt.Fprintf(p.Stderr, "RUN STOPPED entry=%s: %s\n", oneline.Field(e.ID()), oneline.Escape(oneline.Cap(c.Detail, oneline.TailBytes)))
	res.Stopped = true
	p.record(e, c)
}

// record writes this pass's verdict onto the entry, which is what STATUS prints without
// running a pass.
func (p *Pass) record(e *Entry, c Classification) {
	e.State = c.State
	e.Detail = c.Detail
	e.Last = p.Now.UTC().Format(Stamp)
}
