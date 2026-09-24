package land

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/civerdict"
)

// Unit is everything `why` reads for one unit (2.1: the unit, not the PR),
// loaded by LoadUnit or, for the PR form, by LoadPR through s:<S>:prunit.
type Unit struct {
	Unit        string
	Sprint      string
	Repo, Base  string
	ID          ID                // the PR the card names; zero when none
	Fields      map[string]string // s:<S>:u:<unit>
	CI          map[string]string // ci:<repo>:<head>:<gid> for the expected gid
	CIGIDs      []string          // members of ci:<repo>:<head>:gids
	NoPolicy    bool              // land:<repo>:<base>:policy is absent (3.3: ci nopolicy)
	Reads       []Read            // s:<S>:read:<unit>:<who>
	Holds       []Hold            // s:<S>:hold:<unit>:<holder>
	MaxSeq      int64             // the newest rec:seq over the reads and holds
	Friends     map[string]FriendState
	Readers     int // land:<repo>:<base>:policy readers; 0 by the owner ruling (3.3 (2))
	LandBar     int // land:<repo>:<base>:policy land_bar
	AbsentAfter time.Duration
	Rank        int64             // 0-based rank in s:<S>:landable:<repo>:<base>, -1 when absent
	Landable    int64             // ZCARD s:<S>:landable:<repo>:<base>
	Parent      string            // the stack parent unit (sexp edge); "" for none
	ParentRec   map[string]string // s:<S>:u:<parent>
	Batch       map[string]string // land:<repo>:<base>:batch:<batch> when the unit names one
}

// PR is the name PR #3125 gave the record `why` reads; it is the unit now.
type PR = Unit

// Why prints one line per landable condition of 3.3 at the unit's head: ci,
// reads, holds, mergeable (informational), stack parent, drop key, then the
// state with the landable position or the batch. Each line is computed from
// the records; the last line says when the records and the unit's state
// disagree, which is the answer when ns_unit_eval has not run.
func Why(p *Unit, now time.Time) []string {
	f := p.Fields
	head := f["head"]
	var lines []string
	pass := true

	// ci (3.3 (1))
	verdict := civerdict.Of(p.CI)
	switch {
	case verdict != "":
	case p.NoPolicy:
		verdict = "nopolicy"
	case len(p.CIGIDs) > 0:
		verdict = "stale"
	default:
		verdict = civerdict.Missing
	}
	lines = append(lines, fmt.Sprintf("ci %s@%s", verdict, short(head)))
	if !civerdict.Green(verdict) || head == "" {
		pass = false
	}

	// reads at head (3.3 (2))
	line, ok := readsLine(p)
	lines = append(lines, line)
	pass = pass && ok

	// holds (3.3 (3))
	line, open := holdsLine(p, now)
	lines = append(lines, line)
	pass = pass && open == 0

	// mergeable (not a landable gate; conflicts are checked at plan time, 3.6)
	if m := f["mergeable"]; m != "" {
		lines = append(lines, "mergeable "+m)
	} else {
		lines = append(lines, "mergeable MISSING")
	}

	// stack parent (3.3 (5))
	line, ok = parentLine(p)
	lines = append(lines, line)
	pass = pass && ok

	// drop key (3.3 (6))
	line, ok = dropLine(p)
	lines = append(lines, line)
	pass = pass && ok

	lines = append(lines, stateLine(p, now, pass))
	return lines
}

func readsLine(p *Unit) (string, bool) {
	f := p.Fields
	head := f["head"]
	bar := p.LandBar
	if bar < 1 {
		bar = defaultLandBar
	}
	var counted, other []string
	for _, r := range p.Reads {
		if r.Head != head {
			continue
		}
		desc := r.Friend + " " + r.Verdict
		if r.HasScore {
			desc = fmt.Sprintf("%s %d", r.Friend, r.Score)
		}
		switch {
		case strings.EqualFold(r.Friend, "jev"):
			other = append(other, desc+" (jev never counts)")
		case r.Friend == f["author"]:
			other = append(other, desc+" (author)")
		case r.Verdict != "APPROVE":
			other = append(other, r.Friend+" "+r.Verdict)
		case !r.HasScore || r.Score < bar:
			other = append(other, fmt.Sprintf("%s (<%d)", desc, bar))
		default:
			counted = append(counted, desc+" @"+short(head))
		}
	}
	need := p.Readers
	line := fmt.Sprintf("reads %d/%d", len(counted), need)
	if need == 0 {
		line = fmt.Sprintf("reads %d at head, none required (readers 0)", len(counted))
	}
	if len(counted) > 0 {
		line += " (" + strings.Join(counted, ", ") + ")"
	}
	if len(other) > 0 {
		line += "; not counted: " + strings.Join(other, ", ")
	}
	return line, len(counted) >= need
}

func holdsLine(p *Unit, now time.Time) (string, int) {
	head := p.Fields["head"]
	var open, released []string
	for _, h := range p.Holds {
		if !h.Open() {
			released = append(released, fmt.Sprintf("%s released by %s %s", h.ID, h.ReleasedBy, h.ReleaseKind))
			continue
		}
		st := p.Friends[h.Holder]
		state := h.Holder + " state MISSING"
		var absentFor time.Duration
		if st.Known {
			state = h.Holder + " " + st.State
			if !st.Since.IsZero() {
				absentFor = now.Sub(st.Since)
				state += " " + Age(absentFor)
			}
		}
		open = append(open, fmt.Sprintf("%s HOLD %s @%s, %s, %s",
			h.Holder, h.ID, short(h.Head), state, releasableBy(p, h, st, absentFor, head)))
	}
	line := fmt.Sprintf("holds %d open", len(open))
	switch {
	case len(open) > 0:
		line += " (" + strings.Join(open, "; ") + ")"
	case len(released) > 0:
		line += " (" + strings.Join(released, "; ") + ")"
	}
	// holds_open is the unit's count (ns_hold, ns_release); a hold keyed by
	// a login outside the friends set (3.4) is counted there but not listed.
	if n, err := strconv.Atoi(p.Fields["holds_open"]); err == nil && n > len(open) {
		line += fmt.Sprintf("; holds_open=%d on %s", n, UnitKey(p.Sprint, p.Unit))
		return line, n
	}
	return line, len(open)
}

// releasableBy applies v6 3.5 (2): a hold at the current head is released
// only by its holder (control 35); a carried hold also by a non-author
// reader with an APPROVE at head, once the holder has been absent for
// absent_after.
func releasableBy(p *Unit, h Hold, st FriendState, absentFor time.Duration, head string) string {
	if h.Head == head {
		return "releasable by " + h.Holder + " only: hold at head"
	}
	var readers []string
	for _, r := range p.Reads {
		if r.Head == head && r.Verdict == "APPROVE" && r.Friend != h.Holder &&
			r.Friend != p.Fields["author"] && !strings.EqualFold(r.Friend, "jev") {
			readers = append(readers, r.Friend)
		}
	}
	sort.Strings(readers)
	if len(readers) == 0 {
		return "releasable by " + h.Holder + "; no reader has an APPROVE at head"
	}
	who := strings.Join(readers, " or ")
	if st.Absent() && !st.Since.IsZero() {
		if absentFor >= p.AbsentAfter {
			return "releasable by " + who
		}
		return fmt.Sprintf("releasable by %s; %s after %s", h.Holder, who, Age(p.AbsentAfter-absentFor))
	}
	return "releasable by " + h.Holder
}

func parentLine(p *Unit) (string, bool) {
	if p.Parent == "" {
		if v := p.Fields["stack_parent"]; v == "" {
			return "stack parent MISSING", false
		}
		return "stack parent none", true
	}
	name := p.Parent
	st := p.ParentRec["state"]
	switch {
	case len(p.ParentRec) == 0:
		return "stack parent " + name + " MISSING " + UnitKey(p.Sprint, p.Parent), false
	case st == "landed":
		// landed without its merge_sha is an incomplete record: MISSING, never a pass.
		if p.ParentRec["merge_sha"] == "" {
			return "stack parent " + name + " landed, merge_sha MISSING " + UnitKey(p.Sprint, p.Parent), false
		}
		return "stack parent " + name + " merged @" + short(p.ParentRec["merge_sha"]), true
	case st == "batched" || st == "gating" || st == "green" || st == "landing":
		// 3.3 (5): landed or earlier in the chain.
		return "stack parent " + name + " in chain (" + st + " " + p.ParentRec["batch"] + ")", true
	default:
		if st == "" {
			st = "state MISSING"
		}
		return "stack parent " + name + " open (" + st + ")", false
	}
}

// dropLine applies 3.3 (6): a dropped unit re-enters only when its drop
// key's inputs changed: drop_key is <head8>:<rec_seq>, so a new head or a
// read or hold record newer than rec_seq changes it.
func dropLine(p *Unit) (string, bool) {
	f := p.Fields
	key := f["drop_key"]
	if key == "" {
		return "drop none", true
	}
	reason := f["drop_reason"]
	if reason == "" {
		reason = "reason MISSING"
	}
	if f["state"] != "dropped" {
		return fmt.Sprintf("drop none (last %s: %s)", key, reason), true
	}
	h8, seqStr, ok := strings.Cut(key, ":")
	seq, err := strconv.ParseInt(seqStr, 10, 64)
	if !ok || err != nil || f["head"] == "" {
		return fmt.Sprintf("drop %s, key %s MISSING (want <head8>:<rec_seq> and a head)", reason, key), false
	}
	if h8 == short(f["head"]) && p.MaxSeq <= seq {
		return fmt.Sprintf("drop %s, key %s unchanged", reason, key), false
	}
	return fmt.Sprintf("drop %s, key %s changed (re-evaluation due)", reason, key), true
}

func stateLine(p *Unit, now time.Time, pass bool) string {
	f := p.Fields
	st := f["state"]
	in := p.Rank >= 0
	pos := fmt.Sprintf("#%d of %d", p.Rank+1, p.Landable)
	switch st {
	case "landable":
		if !in {
			return "state landable but MISSING from " + LandableKey(p.Sprint, p.Repo, p.Base)
		}
		if !pass {
			return "landable " + pos + ": a condition above fails; landable is stale"
		}
		return "landable " + pos
	case "batched", "gating", "green", "landing":
		b := f["batch"]
		if b == "" {
			return "state " + st + ", batch MISSING"
		}
		if len(p.Batch) == 0 {
			return "state " + st + " (batch " + b + " MISSING " + BatchKey(p.Repo, p.Base, b) + ")"
		}
		line := fmt.Sprintf("state %s (batch %s %s attempt %s", st, b, p.Batch["state"], p.Batch["attempt"])
		if p.Batch["bench"] != "" {
			line += ", " + p.Batch["bench"] + "/" + p.Batch["slot"]
			if t, ok := parseMS(p.Batch["claimed_at"]); ok {
				line += " " + Age(now.Sub(t))
			}
		}
		return line + ")"
	case "landed":
		return "state landed @" + short(f["merge_sha"])
	case "":
		return "state MISSING"
	}
	line := "state " + st
	live := st != "dropped" && st != "settled"
	if pass && live {
		line += ": every gate passes; ns_unit_eval has not run since"
	}
	if in {
		line += " (in landable " + pos + ": stale)"
	} else if pass && live {
		line += " (not in landable)"
	}
	return line
}

// parseMS reads a Redis TIME millisecond stamp (land_now_ms), or seconds.
func parseMS(v string) (time.Time, bool) {
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n <= 0 {
		return parseTime(v)
	}
	if n > 1e11 {
		return time.UnixMilli(n), true
	}
	return time.Unix(n, 0), true
}
