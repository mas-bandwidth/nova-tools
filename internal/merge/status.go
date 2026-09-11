package merge

import (
	"fmt"

	"github.com/mas-bandwidth/nova-tools/internal/bounded"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// status REPORTS and exits 0 whatever the lane holds. run is the verb that acts.
//
// STATUS OK carries the same base verdict as RUN BASE, so a lane can be read without a
// pass, and reads are COUNTS per entry -- never the list, which is behind --reads <entry>
// (rule 19: the coordinator's tokens are spent on decisions, not on transcription).

// Status prints the lane. It writes nothing and it does not shell into the clone to
// resolve a head the lane has not read yet: an entry with no head prints head=- and says
// the lane has not run, because a read-only verb that depends on a clone's freshness is a
// read-only verb that can be wrong.
func (p *Pass) Status(reads string) int {
	// A TOOL THAT COULD NOT LOOK SAYS SO. base_state=UNKNOWN with no other line let a
	// reader take STATUS OK as a report over a lane the tool could not see; the two calls
	// that can fail are named, with what they said, and the exit code is untouched
	// because status REPORTS.
	baseSHA, baseState := "", "UNKNOWN"
	sha, err := p.BaseSHA()
	switch {
	case err != nil:
		fmt.Fprintf(p.Stderr, "STATUS NOTE the lane's base %s could not be read from %s, so base_state is UNKNOWN: %s\n",
			oneline.Field(p.State.Base), oneline.Field(p.Remote), oneline.Escape(oneline.Cap(err.Error(), oneline.TailBytes)))
	default:
		baseSHA = sha
		if checks, err := p.Host.Checks(sha); err != nil {
			fmt.Fprintf(p.Stderr, "STATUS NOTE the base's checks could not be read from the host, so base_state is UNKNOWN: %s\n",
				oneline.Escape(oneline.Cap(err.Error(), oneline.TailBytes)))
		} else {
			baseState = checks.Verdict()
			if p.baseGateGreen(sha) && baseState != "RED" {
				baseState = "GREEN"
			}
		}
	}
	p.basePending = baseState == "PENDING"

	// THE FOLD REFUSES, IT NEVER SKIPS -- AND `status` IS THE VERB THE REMEDY LINE POINTS
	// AT. Status performed the fold and dropped every problem: an entry named by a refusal
	// came out BLOCKED from plan, but the FILE was never named here, and a refusal whose
	// path names no entry matches no entry at all and so vanished completely. `run`,
	// `dry-run` and `packet` announce both. A record file that does not decode is never
	// skipped, and a lane read without a pass is still a lane that must say so.
	//
	// The output grammar has no STATUS line for either, so these are run's own forms --
	// FOLD REFUSED verbatim, and run's `STOPPED reason=malformed_record file=<path>` under
	// this verb's own first token, the way `packet` prints PACKET STOPPED.
	for _, pr := range p.Problems {
		fmt.Fprintf(p.Stderr, "FOLD REFUSED file=%s: %s\n",
			oneline.Field(pr.File), oneline.Escape(oneline.Cap(pr.Reason, oneline.TailBytes)))
	}
	// A record file whose path names no entry leaves the SCOPE indeterminate: there is no
	// entry to mark BLOCKED instead, so no entry's state can be trusted and none is
	// printed -- run stops before any entry is read and this stops before any entry is
	// listed. `status` REPORTS and exits 0 whatever the lane holds (the verb sentences),
	// so the news is the line, not the code, and the closing line is still printed because
	// a reader must be able to tell a stop from a death (lesson 87).
	for _, pr := range p.Problems {
		if pr.Entry == "" {
			fmt.Fprintf(p.Stderr, "STATUS STOPPED reason=malformed_record file=%s: %s; this path names no entry, so no entry's state in this lane can be reported; re-record it with the verb that wrote it\n",
				oneline.Field(pr.File), oneline.Escape(oneline.Cap(pr.Reason, oneline.TailBytes)))
			fmt.Fprintf(p.Stdout, "STATUS OK prs=%d branches=%d base=%s base_state=%s ready=0 blocked=0 waiting=0 reads=0a/0h\n",
				len(p.State.PRs), len(p.State.Branches), oneline.Field(p.State.Base), baseState)
			return 0
		}
	}

	list := bounded.Capped(p.Stdout, p.Max, "STATUS", "entry",
		fmt.Sprintf("nova-merge status --lane %s --max 0", p.Lane))
	var ready, blocked, waiting, approves, holds int
	for _, e := range p.State.Entries() {
		c := p.plan(e, baseSHA)
		list.Line(fmt.Sprintf("STATUS ENTRY kind=%s entry=%s head=%s checks=%s read=%s stale=%d gate=%s state=%s last=%s",
			e.Kind(), oneline.Field(e.ID()), oneline.Field(dashIfEmpty(Short(e.OID))), c.Checks.Field(),
			c.Reads.Field(), c.Reads.Stale, dashIfEmpty(c.Gate.Kind), c.State, oneline.Field(dashIfEmpty(e.Last))))
		approves += c.Reads.Approves
		holds += c.Reads.Holds
		switch c.State {
		case StateMergeableGreen:
			ready++
		case StateBlocked, StateRed, StateHold, StateWrongBase, StateFork:
			blocked++
		default:
			waiting++
		}
	}
	list.More()
	fmt.Fprintf(p.Stdout, "STATUS OK prs=%d branches=%d base=%s base_state=%s ready=%d blocked=%d waiting=%d reads=%da/%dh\n",
		len(p.State.PRs), len(p.State.Branches), oneline.Field(p.State.Base), baseState,
		ready, blocked, waiting, approves, holds)
	if reads != "" {
		p.listReads(reads)
	}
	return 0
}

// listReads is --reads <entry>: the list behind the flag, with the sha each verdict was
// recorded for and the file it lives in.
func (p *Pass) listReads(entry string) {
	e := p.State.Find(entry)
	if e == nil {
		fmt.Fprintf(p.Stderr, "STATUS NOTE %s is not in this lane\n", oneline.Field(entry))
		return
	}
	list := bounded.Capped(p.Stdout, p.Max, "STATUS", "read",
		fmt.Sprintf("nova-merge status --lane %s --reads %s --max 0", p.Lane, entry))
	for _, r := range e.Reads {
		current := "false"
		if r.Head == e.OID && e.OID != "" {
			current = "true"
		}
		list.Line(fmt.Sprintf("STATUS READ entry=%s who=%s verdict=%s head=%s current=%s at=%s file=%s",
			oneline.Field(e.ID()), oneline.Field(r.Who), oneline.Field(r.Verdict),
			oneline.Field(Short(r.Head)), current, oneline.Field(r.At), oneline.Field(r.File)))
	}
	list.More()
}
