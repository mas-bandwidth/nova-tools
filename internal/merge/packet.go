package merge

import (
	"fmt"
	"sort"

	"github.com/mas-bandwidth/nova-tools/internal/bounded"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// Work list 12, rule 23: A READER IS HANDED THE SMALLEST SUFFICIENT PACKET, AND IT IS
// POINTERS, NEVER THE DIFF.
//
// Stella, 2026-09-11: the smallest sufficient review packet is the diff since my reviewed
// sha, the unresolved finding ids with their dispositions, and links to the whole; my
// first pass loaded too much history. So the tool prints the RANGE and never the diff,
// the summary path and never the log: the reader opens exactly what changed and nothing
// the coordinator retyped. It is derived from the fold and the host, writes nothing and
// takes no lock.

// Packet prints one bounded block per entry that needs a read from who.
func (p *Pass) Packet(who string, only string, all bool) int {
	baseSHA, _ := p.BaseSHA()
	entries := p.State.Entries()
	blocks := 0
	total := 0
	holdList := bounded.Capped(p.Stdout, p.Max, "PACKET", "hold",
		fmt.Sprintf("nova-merge packet --lane %s --who %s --all --max 0", p.Lane, who))
	for _, e := range entries {
		if !all && e.ID() != only {
			continue
		}
		c := p.plan(e, baseSHA)
		if !p.needsReadFrom(e, who) {
			continue
		}
		blocks++
		last := lastReadBy(e, who)
		rng := fmt.Sprintf("%s...%s", p.State.Base, Short(e.OID))
		if last != "" {
			rng = fmt.Sprintf("%s..%s", Short(last), Short(e.OID))
		}
		holds := unresolvedHolds(e)
		total += len(holds)
		fmt.Fprintf(p.Stdout, "PACKET ENTRY entry=%s head=%s last_read=%s range=%s holds=%d gate=%s checks=%s url=%s\n",
			oneline.Field(e.ID()), oneline.Field(dashIfEmpty(Short(e.OID))),
			oneline.Field(dashIfEmpty(Short(last))), oneline.Field(rng), len(holds),
			dashIfEmpty(c.Gate.Kind), c.Checks.Field(), oneline.Field(dashIfEmpty(c.URL)))
		for _, h := range holds {
			holdList.Line(fmt.Sprintf("PACKET HOLD who=%s head=%s: %s",
				oneline.Field(h.Who), oneline.Field(Short(h.Head)), oneline.Cap(h.Note, oneline.TailBytes)))
		}
	}
	holdList.More()
	fmt.Fprintf(p.Stdout, "PACKET OK entries=%d holds=%d\n", blocks, total)
	return 0
}

// needsReadFrom is the one condition: needs_read=yes, and no approve by that name for the
// entry's CURRENT oid.
func (p *Pass) needsReadFrom(e *Entry, who string) bool {
	if e.NeedsRead != "yes" {
		return false
	}
	for _, r := range e.Reads {
		if sameLine(r.Who, who) && r.Head == e.OID && e.OID != "" && r.Verdict == "approve" {
			return false
		}
	}
	return true
}

// lastReadBy is the sha this reader last RECORDED for, whatever the verdict: the left
// half of the range, so the reader opens the commits since the sha they read.
func lastReadBy(e *Entry, who string) string {
	best := ""
	at := ""
	for _, r := range e.Reads {
		if sameLine(r.Who, who) && r.At > at {
			at, best = r.At, r.Head
		}
	}
	return best
}

// unresolvedHolds is the FINDINGS a reader must act on, and a finding is a hold record
// rather than a reader: two holds recorded by one line on one head are two things to fix,
// and a packet that folded them to one would hand the reader half of what was said.
//
// What the fold does decide is whether they are still open: per (who, head) the newest
// record settles the pair, so a line that later recorded an approve for that head has
// resolved every hold it left there, and none of them is in the packet.
func unresolvedHolds(e *Entry) []Read {
	newest := map[string]Read{}
	for _, r := range e.Reads {
		key := r.Who + "\x00" + r.Head
		cur, seen := newest[key]
		if !seen || r.At > cur.At || (r.At == cur.At && r.Verdict == "hold") {
			newest[key] = r
		}
	}
	var out []Read
	for _, r := range e.Reads {
		if r.Verdict != "hold" {
			continue
		}
		if settled := newest[r.Who+"\x00"+r.Head]; settled.Verdict == "hold" {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].At != out[j].At {
			return out[i].At < out[j].At
		}
		return out[i].Who < out[j].Who
	})
	return out
}
