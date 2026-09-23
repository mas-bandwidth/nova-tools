package land

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// PR is everything `why` reads for one PR, loaded by LoadPR.
type PR struct {
	ID          ID
	Sprint      string
	Fields      map[string]string // s:<S>:pr:<repo>:<n>; empty when the record is absent
	CI          map[string]string // ci:<repo>:<head>
	Reads       []Read
	Holds       []Hold
	Friends     map[string]FriendState
	Readers     int
	AbsentAfter time.Duration
	Rank        int64 // 0-based rank in s:<S>:landable, -1 when absent
	Landable    int64 // ZCARD s:<S>:landable
	Parent      *ID
	ParentRec   map[string]string
}

// Why prints one line per gate: ci, reads, holds, draft, mergeable, stack
// parent, drop key, then the state with the landable position. Each line is
// computed from the records; the last line says when the records and the
// PR's state disagree, which is the answer when ns_pr_eval has not run.
func Why(p *PR, now time.Time) []string {
	f := p.Fields
	head := f["head"]
	var lines []string
	pass := true

	// ci
	verdict := p.CI["verdict"]
	if verdict == "" {
		verdict = "MISSING"
	}
	lines = append(lines, fmt.Sprintf("ci %s@%s", verdict, short(head)))
	if verdict != "OK" || head == "" {
		pass = false
	}

	// reads at head
	line, ok := readsLine(p)
	lines = append(lines, line)
	pass = pass && ok

	// holds
	line, open := holdsLine(p, now)
	lines = append(lines, line)
	pass = pass && open == 0

	// draft
	switch f["draft"] {
	case "false":
		lines = append(lines, "draft no")
	case "true":
		lines = append(lines, "draft yes")
		pass = false
	default:
		lines = append(lines, "draft MISSING")
		pass = false
	}

	// mergeable (not a landable gate; a CONFLICTING PR gets a rebase card)
	if m := f["mergeable"]; m != "" {
		lines = append(lines, "mergeable "+m)
	} else {
		lines = append(lines, "mergeable MISSING")
	}

	// stack parent
	line, ok = parentLine(p)
	lines = append(lines, line)
	pass = pass && ok

	// drop key
	line, ok = dropLine(f)
	lines = append(lines, line)
	pass = pass && ok

	lines = append(lines, stateLine(p, now, pass))
	return lines
}

func readsLine(p *PR) (string, bool) {
	f := p.Fields
	head := f["head"]
	bar, err := strconv.Atoi(f["land_bar"])
	if err != nil {
		return "reads MISSING land_bar", false
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
	if need < 1 {
		need = 1
	}
	line := fmt.Sprintf("reads %d/%d", len(counted), need)
	if len(counted) > 0 {
		line += " (" + strings.Join(counted, ", ") + ")"
	}
	if len(other) > 0 {
		line += "; not counted: " + strings.Join(other, ", ")
	}
	return line, len(counted) >= need
}

func holdsLine(p *PR, now time.Time) (string, int) {
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
	return line, len(open)
}

// releasableBy applies v6 3.5 (2): a hold at the current head is released
// only by its holder (control 35); a carried hold also by a non-author
// reader with an APPROVE at head, once the holder has been absent for
// absent_after.
func releasableBy(p *PR, h Hold, st FriendState, absentFor time.Duration, head string) string {
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

func parentLine(p *PR) (string, bool) {
	if p.Parent == nil {
		if v := p.Fields["stack_parent"]; v == "" {
			return "stack parent MISSING", false
		}
		return "stack parent none", true
	}
	name := "#" + strconv.Itoa(p.Parent.N)
	if p.Parent.Repo != p.ID.Repo {
		name = p.Parent.String()
	}
	st := p.ParentRec["state"]
	switch {
	case len(p.ParentRec) == 0:
		return "stack parent " + name + " MISSING " + p.Parent.Key(p.Sprint), false
	case st == "landed":
		// landed without its merge_sha is an incomplete record: MISSING, never a pass.
		if p.ParentRec["merge_sha"] == "" {
			return "stack parent " + name + " landed, merge_sha MISSING " + p.Parent.Key(p.Sprint), false
		}
		return "stack parent " + name + " merged @" + short(p.ParentRec["merge_sha"]), true
	default:
		if st == "" {
			st = "state MISSING"
		}
		return "stack parent " + name + " open (" + st + ")", false
	}
}

func dropLine(f map[string]string) (string, bool) {
	key := f["drop_key"]
	if key == "" {
		return "drop none", true
	}
	lane, reason := f["lane"], f["drop_reason"]
	if lane == "" {
		lane = "lane MISSING"
	}
	if reason == "" {
		reason = "reason MISSING"
	}
	cur := f["head"] + ":" + f["latest_comment_id"] + ":" + f["latest_record_id"]
	if f["head"] == "" || f["latest_comment_id"] == "" || f["latest_record_id"] == "" {
		return fmt.Sprintf("drop %s: %s, key MISSING (head, latest_comment_id, latest_record_id)", lane, reason), false
	}
	if key == cur {
		return fmt.Sprintf("drop %s: %s, key unchanged", lane, reason), false
	}
	return fmt.Sprintf("drop %s: %s, key changed (re-evaluation due)", lane, reason), true
}

func stateLine(p *PR, now time.Time, pass bool) string {
	f := p.Fields
	st := f["state"]
	in := p.Rank >= 0
	pos := fmt.Sprintf("#%d of %d", p.Rank+1, p.Landable)
	switch st {
	case "landable":
		if !in {
			return "state landable but MISSING from s:" + p.Sprint + ":landable"
		}
		if !pass {
			return "landable " + pos + ": a gate above fails; landable is stale"
		}
		return "landable " + pos
	case "landing":
		age := "age MISSING"
		if t, ok := parseTime(f["lane_at"]); ok {
			age = Age(now.Sub(t))
		}
		return fmt.Sprintf("state landing (lane %s, %s)", f["lane"], age)
	case "landed":
		return "state landed @" + short(f["merge_sha"])
	case "":
		return "state MISSING"
	}
	line := "state " + st
	if pass && st != "closed" && st != "dropped" {
		line += ": every gate passes; ns_pr_eval has not run since"
	}
	if in {
		line += " (in landable " + pos + ": stale)"
	} else if pass && st != "closed" && st != "dropped" {
		line += " (not in landable)"
	}
	return line
}
