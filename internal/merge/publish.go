package merge

import (
	"errors"
	"fmt"

	"github.com/mas-bandwidth/nova-tools/internal/bounded"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// Rule 21: THE SAFETY CHECK PRECEDES PUBLICATION. The object gated is the object
// published, under a base precondition, and a race is a refusal BEFORE the write.
//
// A base re-read "immediately before the merge" still leaves the window between the read
// and the host's write, and a hand at another keyboard fits in it. So the window is
// closed by the REMOTE, as a precondition on the write: the lease. Two parents equal to
// (base, head) are not the test -- two merges of the same parents can differ in their
// trees -- the sha is.
//
// merge is the ONE call site of the publication helper, and it is inside the function
// that evaluates the predicate. A source test asserts there is exactly one.

// merge publishes the gated object for one entry. It returns true when the base moved to
// that object; every other answer stops the pass.
func (p *Pass) merge(e *Entry, c Classification, baseSHA string, res *Result) bool {
	rec := c.Gate.Record

	// The predicate's second line: the gate's merge is an object IN THIS CLONE whose
	// first parent is the base sha and whose second is the entry's oid. A record naming
	// a merge this clone does not hold names an object nobody can publish, and this tool
	// never builds a replacement on the way to the push.
	if !HasObject(p.Clone, rec.Merge) {
		p.mergeFail(e, res, fmt.Sprintf("the gate record names merge %s, which is not in the lane's clone; nothing was published and nothing was rebuilt", Short(rec.Merge)))
		return false
	}
	parents, err := Parents(p.Clone, rec.Merge)
	if err != nil {
		p.mergeFail(e, res, oneline.Cap(err.Error(), oneline.TailBytes))
		return false
	}
	if len(parents) != 2 || parents[0] != baseSHA || parents[1] != e.OID {
		p.mergeFail(e, res, fmt.Sprintf("the gated object %s has parents %v; an integration commit's first parent is the base %s and its second is the head %s",
			Short(rec.Merge), shorts(parents), Short(baseSHA), Short(e.OID)))
		return false
	}

	// The same re-read that catches the author pushing mid-pass: the entry's oid is read
	// once more immediately before the merge, and a change refuses.
	if oid, err := p.currentOID(e); err != nil || oid != e.OID {
		p.mergeFail(e, res, fmt.Sprintf("the entry's head moved from %s to %s while this pass was reading it", Short(e.OID), Short(oid)))
		return false
	}

	// A draft that is in the lane is readied when its turn comes: a mutation, logged as
	// one. An entry that is a draft and is NOT in the lane cannot be reached at all.
	if e.IsPR() {
		if pr, err := p.Host.PR(e.PR); err == nil && pr.Draft {
			Appendf(p.Lane, p.Now, "RUN gh pr ready %d", e.PR)
			if err := p.Host.Ready(e.PR); err != nil {
				p.mergeFail(e, res, oneline.Cap(err.Error(), oneline.TailBytes))
				return false
			}
		}
	}

	published := "push"
	Appendf(p.Lane, p.Now, "RUN publish entry=%s merge=%s onto %s expecting %s", e.ID(), rec.Merge, p.State.Base, baseSHA)
	if p.Host.AtomicMerge() && e.IsPR() {
		// (a) The host offers a merge primitive taking BOTH an expected head and an
		// expected base. Use it, with both, and its merge commit must be the gated
		// object.
		published = "host"
		if err := p.Host.Merge(e.PR, e.OID, baseSHA, rec.Merge); err != nil {
			p.mergeFail(e, res, oneline.Cap(err.Error(), oneline.TailBytes))
			return false
		}
	} else {
		// (b) It does not -- gh takes --match-head-commit and nothing about the base --
		// so the gated object itself is pushed under the lease. A plain push checks
		// fast-forward ANCESTRY, not equality with the expected sha, so it is not used
		// for anything.
		if _, err := p.Clone.Publish(p.Remote, p.State.Base, baseSHA, rec.Merge); err != nil {
			var raced *RacedError
			var refused *PublishRefusedError
			switch {
			case errors.As(err, &raced):
				found, _ := p.BaseSHA()
				fmt.Fprintf(p.Stderr, "MERGE RACED entry=%s base=%s expected=%s found=%s merge=%s: the base moved after the gate; nothing was published; this pass stops\n",
					oneline.Field(e.ID()), oneline.Field(p.State.Base), oneline.Field(Short(baseSHA)),
					oneline.Field(Short(found)), oneline.Field(Short(rec.Merge)))
				Appendf(p.Lane, p.Now, "MERGE RACED entry=%s expected=%s merge=%s", e.ID(), baseSHA, rec.Merge)
				res.Stopped = true
				res.Note = fmt.Sprintf("nova-merge run --lane %s --once  # the next pass builds a fresh integration commit on the moved base and asks for its gate", p.Lane)
				return false
			case errors.As(err, &refused):
				fmt.Fprintf(p.Stderr, "MERGE BLOCKED entry=%s base=%s missing=atomic_publication: %s; nothing was published; this pass stops\n",
					oneline.Field(e.ID()), oneline.Field(p.State.Base), oneline.Escape(oneline.Cap(refused.Output, oneline.TailBytes)))
				res.Stopped = true
				res.Blocked++
				res.Note = fmt.Sprintf("the base %s admits no direct push: give this lane a host primitive that takes both an expected head and an expected base, or change the branch setting that refuses it -- nova-merge never falls back to a push with no base precondition", p.State.Base)
				return false
			default:
				p.mergeFail(e, res, oneline.Cap(err.Error(), oneline.TailBytes))
				return false
			}
		}
	}

	// The read-back is ADDITIONAL EVIDENCE FOR A PERSON, never the guard: the guard
	// already ran on the remote.
	verified, err := p.BaseSHA()
	if err != nil || verified != rec.Merge {
		fmt.Fprintf(p.Stderr, "MERGE FAIL entry=%s: published object not at base; the lease was recorded as sent and the base reads back as %s\n",
			oneline.Field(e.ID()), oneline.Field(Short(verified)))
		res.Stopped = true
		return false
	}
	fmt.Fprintf(p.Stdout, "MERGE OK entry=%s base=%s base_sha=%s head=%s merge=%s gate=%s admitted=%s read=%s hosted_red=%s published=%s verified=%s\n",
		oneline.Field(e.ID()), oneline.Field(p.State.Base), oneline.Field(Short(baseSHA)),
		oneline.Field(Short(e.OID)), oneline.Field(Short(rec.Merge)), oneline.Field(rec.Summary),
		oneline.Field(c.Admitted), oneline.Field(c.Reads.ReadNames(e.NeedsRead == "yes")),
		oneline.Field(c.Checks.Names()), published, oneline.Field(Short(verified)))
	Appendf(p.Lane, p.Now, "MERGE OK entry=%s merge=%s verified=%s", e.ID(), rec.Merge, verified)
	// The entry LEAVES THE LANE: the lane's authority is exactly its own list, and
	// dropped= on RUN OK is how many entries this pass took out of it.
	p.drop(e)
	res.Dropped++
	return true
}

func (p *Pass) mergeFail(e *Entry, res *Result, why string) {
	fmt.Fprintf(p.Stderr, "MERGE FAIL entry=%s: %s\n", oneline.Field(e.ID()), oneline.Escape(why))
	Appendf(p.Lane, p.Now, "MERGE FAIL entry=%s: %s", e.ID(), why)
	res.Stopped = true
}

// currentOID re-reads the entry's head from the host.
func (p *Pass) currentOID(e *Entry) (string, error) {
	if e.IsPR() {
		pr, err := p.Host.PR(e.PR)
		return pr.HeadOID, err
	}
	return p.Host.BranchOID(e.Branch)
}

// drop removes a merged entry from the lane. The lane's authority is exactly its own
// list, and an entry that has landed is no longer in it.
func (p *Pass) drop(e *Entry) {
	keep := func(list []*Entry) []*Entry {
		out := list[:0]
		for _, x := range list {
			if x != e {
				out = append(out, x)
			}
		}
		return out
	}
	p.State.PRs = keep(p.State.PRs)
	p.State.Branches = keep(p.State.Branches)
}

func shorts(list []string) []string {
	out := make([]string, 0, len(list))
	for _, s := range list {
		out = append(out, Short(s))
	}
	return out
}

// survey is dry-run: every read run performs, the WHOLE plan rather than a stop at the
// first merge, and no path from here to the mutating helper at all. It writes neither
// state.json nor the checkout: its fold is in memory over the lane tip it fetched, which
// is a property a test can pin and a flag never is.
func (p *Pass) survey(res *Result) *Result {
	fmt.Fprintf(p.Stdout, "DRY PLAN lane_tip=%s pulled=%d\n", oneline.Field(dashIfEmpty(Short(p.LaneTip))), p.Pulled)
	// dry-run REPORTS and exits 0 whatever the lane holds -- the spec's own sentence for
	// status, dry-run and packet. It said the same thing and exited 1, so a caller could
	// not tell a report from a wall.
	for _, pr := range p.Problems {
		if pr.Entry == "" {
			fmt.Fprintf(p.Stderr, "RUN STOPPED reason=malformed_record file=%s: %s\n",
				oneline.Field(pr.File), oneline.Escape(oneline.Cap(pr.Reason, oneline.TailBytes)))
			res.Blocked++
			fmt.Fprintf(p.Stdout, "DRY OK surveyed=0 would_merge=- stopped=%d waiting=0\n", res.Blocked)
			return res
		}
	}
	baseSHA, err := p.BaseSHA()
	if err != nil {
		fmt.Fprintf(p.Stderr, "RUN REFUSED: the lane's base %s could not be read: %s\n", oneline.Field(p.State.Base), oneline.Err(err))
		res.Stopped = true
		return res
	}
	list := bounded.Capped(p.Stdout, p.Max, "DRY", "entry", fmt.Sprintf("nova-merge dry-run --lane %s --max 0", p.Lane))
	would := "-"
	pos := 0
	for _, e := range p.State.Entries() {
		pos++
		st := p.plan(e, baseSHA)
		list.Line(fmt.Sprintf("DRY PLAN pos=%d entry=%s admitted=%s gate=%s read=%s",
			pos, oneline.Field(e.ID()), oneline.Field(dashIfEmpty(st.Admitted)), dashIfEmpty(st.Gate.Kind), st.Reads.Field()))
		switch st.State {
		case StateMergeableGreen:
			if would == "-" {
				would = e.ID()
			}
		case StateBlocked, StateRed, StateHold, StateWrongBase, StateFork:
			res.Blocked++
		default:
			res.Waiting++
		}
	}
	list.More()
	fmt.Fprintf(p.Stdout, "DRY OK surveyed=%d would_merge=%s stopped=%d waiting=%d\n",
		len(p.State.Entries()), oneline.Field(would), res.Blocked, res.Waiting)
	return res
}
