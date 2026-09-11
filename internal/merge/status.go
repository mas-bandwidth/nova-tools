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
	baseSHA, baseState := "", "UNKNOWN"
	if sha, err := p.BaseSHA(); err == nil {
		baseSHA = sha
		if checks, err := p.Host.Checks(sha); err == nil {
			baseState = checks.Verdict()
			if p.baseGateGreen(sha) && baseState != "RED" {
				baseState = "GREEN"
			}
		}
	}
	p.basePending = baseState == "PENDING"
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
