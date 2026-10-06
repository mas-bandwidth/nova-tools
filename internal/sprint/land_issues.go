package sprint

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/forge"
)

// A landing closes the card's issues (docs/SPEC-SPRINT.md section 7; the owner, 2026-10-05:
// "Ideally, you would close the issues as soon as the card is landed."). The landing is the
// moment the work is real, so the forge learns it then, from the machine. The merge step
// writes on each card it lands the issues the card references (FieldIssues: its brief's,
// read against its REPO:, and its landed commits' "Closes #N", forge.Refs) and the landing
// commit (FieldLandCommit). The closer (CloseLandedIssues) asks the forge to close each one
// with one comment, and the record step (IssuesClosed) writes what it closed on the card
// (FieldIssuesClosed). A close the forge refused stays pending, its why on the card, and the
// tick's closer tries it again after IssuesRetry; the landing is landed whatever the forge
// says. An issue already closed, on the forge or by another landed card (a twin's), is left
// alone and noted, with no second comment.

// The fields of a landed primary the closer reads and writes.
const (
	// FieldIssues is the issues the card's landing closes, owner/name#N comma separated,
	// written by the merge step as it lands. A card landed before it was written has none,
	// so nothing is closed for it here (the stream archive closes that backlog).
	FieldIssues = "issues"
	// FieldIssuesClosed is those the forge has closed, or that were found closed and left
	// alone: an issue in FieldIssues and not here is pending.
	FieldIssuesClosed = "issues_closed"
	// FieldIssuesWhy and FieldIssuesTried are why the last try left an issue pending, and
	// when (RFC 3339); both cleared once nothing is.
	FieldIssuesWhy   = "issues_why"
	FieldIssuesTried = "issues_tried"
	// FieldLandCommit is the landing commit, the base's tip the lander pushed with the card.
	FieldLandCommit = "land_commit"
)

// IssuesRetry is how long the tick's closer waits after a refused close before it asks the
// forge again for that card.
const IssuesRetry = time.Minute

// LandingIssues is the fields the merge step writes on a card as it lands: the landing
// commit, when the lander gave one, and every issue the card references, its brief's first,
// then closes (owner/name#N, what its landed commits close), each once. Nil when there is
// neither.
func LandingIssues(c *Card, commit string, closes []string) map[string]string {
	brief := c.F("brief")
	var all []string
	for _, i := range forge.Issues(brief, forge.BriefRepo(brief)) {
		all = append(all, i.String())
	}
	for _, s := range closes {
		if i, ok := forge.ParseIssue(s); ok && !slices.Contains(all, i.String()) {
			all = append(all, i.String())
		}
	}
	set := map[string]string{}
	if commit != "" {
		set[FieldLandCommit] = commit
	}
	if len(all) > 0 {
		set[FieldIssues] = strings.Join(all, ",")
	}
	if len(set) == 0 {
		return nil
	}
	return set
}

// PendingClose is a landed card with an issue its landing has not closed yet.
type PendingClose struct {
	Card   string    `json:"card"`
	Stream string    `json:"stream"`
	Issues []string  `json:"issues"`
	Why    string    `json:"why,omitempty"`
	Tried  time.Time `json:"tried,omitzero"`
}

// PendingCloses is every landed card with an issue not closed, in work order.
func PendingCloses(s *Snapshot) []PendingClose {
	var out []PendingClose
	for _, c := range s.Work.Column(Landed) {
		closed := splitList(c.F(FieldIssuesClosed))
		var open []string
		for _, i := range splitList(c.F(FieldIssues)) {
			if !slices.Contains(closed, i) {
				open = append(open, i)
			}
		}
		if len(open) == 0 {
			continue
		}
		p := PendingClose{Card: c.ID, Stream: c.Row, Issues: open, Why: c.F(FieldIssuesWhy)}
		if t, err := time.Parse(time.RFC3339, c.F(FieldIssuesTried)); err == nil {
			p.Tried = t
		}
		out = append(out, p)
	}
	return out
}

// IssuesReq is what one pass of the closer did for one landed card.
type IssuesReq struct {
	Card string
	// Closed is the issues the forge closed now; Noted those left alone, found closed on the
	// forge or closed by another landed card. Both are written as closed.
	Closed []string `json:",omitempty"`
	Noted  []string `json:",omitempty"`
	// Why is why the rest stay pending, "" when none does; At is when it was tried.
	Why string `json:",omitempty"`
	At  string `json:",omitempty"`
	Who string
}

// IssuesClosed records one pass of the closer on a landed card: what it closed is added to
// FieldIssuesClosed, and the why of what stays pending is written with when, or cleared.
// Refused for a card that is not landed. Writing what the card holds already plans nothing.
func IssuesClosed(s *Snapshot, r IssuesReq) Plan {
	var p Plan
	c := s.Work.Placed(r.Card)
	if c == nil || c.Col != Landed {
		p.refuse(r.Card, "not a landed card ("+placeWord(orEmpty(c, r.Card))+"); its issues close when it lands")
		return p
	}
	closed := splitList(c.F(FieldIssuesClosed))
	for _, i := range append(slices.Clone(r.Closed), r.Noted...) {
		if !slices.Contains(closed, i) {
			closed = append(closed, i)
		}
	}
	set := map[string]string{}
	var unset []string
	if v := strings.Join(closed, ","); v != c.F(FieldIssuesClosed) {
		set[FieldIssuesClosed] = v
	}
	if r.Why != "" {
		set[FieldIssuesWhy], set[FieldIssuesTried] = r.Why, r.At
	} else {
		unset = []string{FieldIssuesWhy, FieldIssuesTried}
	}
	e := setEntry(c, set, unset...)
	if len(e.Set) == 0 && len(e.Unset) == 0 {
		return p
	}
	var said []string
	if len(r.Closed) > 0 {
		said = append(said, "closed "+strings.Join(r.Closed, ", "))
	}
	if len(r.Noted) > 0 {
		said = append(said, "left alone (closed already) "+strings.Join(r.Noted, ", "))
	}
	if r.Why != "" {
		said = append(said, "pending ("+r.Why+")")
	}
	p.Units = append(p.Units, Unit{Key: c.ID, Stream: c.Row, Changes: []Change{change(Work, e)},
		Moved: c.ID + " issues: " + strings.Join(said, "; ")})
	return p
}

// IssueComment is the one comment a close leaves on the issue: the card, its stream, the
// landing commit and the release it ships in (the stream's release, FieldRelease).
func IssueComment(s *Snapshot, c *Card) string {
	commit := c.F(FieldLandCommit)
	if commit == "" {
		commit = "no commit recorded (a landing reported by hand)"
	}
	release := "the next release (stream " + c.Row + " names none)"
	if ctl := s.StreamCtl(c.Row); ctl != nil && ctl.F(FieldRelease) != "" {
		release = ctl.F(FieldRelease)
	}
	return "Closed by nova-sprint: card " + c.ID + " of stream " + c.Row + " landed.\n\n" +
		"- card: " + c.ID + "\n- stream: " + c.Row + "\n- landing commit: " + commit + "\n- ships in: " + release + "\n"
}

// CloseLandedIssues is one pass of the closer over the landed cards with an issue pending
// (PendingCloses): cards, when given, are the only ones tried (a landing's own, tried at
// once); otherwise every pending card is, but one whose last try failed less than
// IssuesRetry before now. Each pending issue is closed through f with IssueComment, or left
// alone when another landed card closed it or the forge has it closed. The pass stops at the
// first close the forge refuses: that card's rest stays pending with the why, and the cards
// after it are tried by the next pass. It returns one request per card it tried, for
// IssuesClosed; it writes nothing itself.
func CloseLandedIssues(ctx context.Context, s *Snapshot, f forge.Closer, now time.Time, cards []string, who string) []IssuesReq {
	closedBy := map[string]bool{}
	for _, c := range s.Work.Column(Landed) {
		for _, i := range splitList(c.F(FieldIssuesClosed)) {
			closedBy[i] = true
		}
	}
	var out []IssuesReq
	for _, p := range PendingCloses(s) {
		if len(cards) > 0 && !slices.Contains(cards, p.Card) {
			continue
		}
		if len(cards) == 0 && p.Why != "" && now.Sub(p.Tried) < IssuesRetry {
			continue
		}
		c := s.Work.Placed(p.Card)
		r := IssuesReq{Card: p.Card, Who: who}
		for _, ref := range p.Issues {
			if closedBy[ref] {
				r.Noted = append(r.Noted, ref)
				continue
			}
			i, ok := forge.ParseIssue(ref)
			if !ok {
				r.Noted = append(r.Noted, ref) // never written by LandingIssues; nothing to close
				continue
			}
			already, err := f.Close(ctx, i, IssueComment(s, c))
			if err != nil {
				r.Why, r.At = "the forge refused the close of "+ref+": "+oneLineOf(err.Error()), now.UTC().Format(time.RFC3339)
				break
			}
			if already {
				r.Noted = append(r.Noted, ref)
			} else {
				r.Closed = append(r.Closed, ref)
			}
			closedBy[ref] = true
		}
		out = append(out, r)
		if r.Why != "" {
			return out
		}
	}
	return out
}

// PropIssuesPending is the work table's property where shows the pending closes from, so
// where reads no card: IssuesPendingText of the last pass of the closer (IssuesShown).
const PropIssuesPending = "issues_pending"

// IssuesPendingText is the pending closes on one line: "0" when none is; else the count of
// issues and cards, then each card's issues and its last why, cut at 600 bytes.
func IssuesPendingText(pending []PendingClose) string {
	if len(pending) == 0 {
		return "0"
	}
	n := 0
	var each []string
	for _, p := range pending {
		n += len(p.Issues)
		x := p.Card + " " + strings.Join(p.Issues, ",")
		if p.Why != "" {
			x += " (" + p.Why + ")"
		}
		each = append(each, x)
	}
	v := strconv.Itoa(n) + " issues of " + strconv.Itoa(len(pending)) + " landed cards: " + strings.Join(each, "; ")
	if len(v) > 600 {
		v = v[:597] + "..."
	}
	return v
}

// IssuesShown writes PropIssuesPending from the work table as it is; nothing when it says
// that already.
func IssuesShown(s *Snapshot) Plan {
	v := IssuesPendingText(PendingCloses(s))
	was, had := s.Work.Prop(PropIssuesPending)
	if v == was || !had && v == "0" {
		return Plan{}
	}
	return Plan{Props: []PropWrite{{Table: Work, Name: PropIssuesPending, Value: v, Was: was, WasAbsent: !had}}}
}

// splitList is a comma separated field's items, blanks dropped.
func splitList(v string) []string {
	var out []string
	for _, x := range strings.Split(v, ",") {
		if x = strings.TrimSpace(x); x != "" {
			out = append(out, x)
		}
	}
	return out
}

// oneLineOf is text on one line, its runs of space one blank.
func oneLineOf(s string) string { return strings.Join(strings.Fields(s), " ") }
