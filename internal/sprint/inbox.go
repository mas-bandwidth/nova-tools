package sprint

import (
	"cmp"
	"fmt"
	"maps"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

// StreamClock is a stream's state, since (the last change of its state) and
// progress (the last change of its state or of any of its counts).
type StreamClock struct {
	Stream   string
	State    string
	Since    time.Time
	Progress time.Time
	// Empty says nothing of the stream is on the table: never stale.
	Empty bool `json:",omitempty"`
	// Held says every card of the stream on the table and not landed waits (on a
	// sentinel, or a need): it is held by what it waits on, never stale.
	Held bool `json:",omitempty"`
	// Quiet is the time wait set on the stream's stale judgment (FieldStaleReview):
	// not shown stale before it.
	Quiet time.Time `json:",omitzero"`
}

// Stalled says a stream that has not landed has made no progress for longer
// than stale. It is pull visibility: nothing here detects a dead process.
func (c StreamClock) Stalled(now time.Time, stale time.Duration) bool {
	return c.State != StreamLanded && stale > 0 && !c.Progress.IsZero() && now.Sub(c.Progress) > stale
}

// InboxReq is what the inbox is computed from, at read time: the open
// judgments, the notifications since the cursor, the streams' clocks, and the
// clock reading of the read. Overdue is computed here, so a dead coordinator
// is visible to anyone who runs inbox.
type InboxReq struct {
	Now      time.Time
	Open     []Open
	Recent   []Note // since the cursor, oldest first
	Streams  []StreamClock
	Deadline time.Duration // a judgment open longer, in running time, is overdue
	Stale    time.Duration // a moving stream unchanged longer, in running time, needs a look
	Prefix   string        // the deployment's prefix (empty for none), for the commands that name it
	Epoch    uint64        // the sprint's epoch: the stale groups' ids carry it
	// Stopped is the time the machine was STOPPED between two clock
	// readings: the deadlines count running time only, as the tick's do. nil
	// is none.
	Stopped func(from, to time.Time) time.Duration
}

// running is the running time from a clock reading to the read's: the time
// on the clock less the time the machine was STOPPED.
func (r InboxReq) running(from time.Time) time.Duration {
	d := r.Now.Sub(from)
	if r.Stopped != nil {
		d -= r.Stopped(from, r.Now)
	}
	return d
}

// due is when a judgment raised at a clock reading is overdue, the time the
// machine was STOPPED since then added: a sprint stopped for hours shows
// nothing overdue because of those hours. A review time the coordinator set
// (wait) is its own. The sprint is done has no due time and is never overdue:
// nothing is late when all the work is.
func (r InboxReq) due(n Note) (time.Time, bool) {
	if n.Type == NSprintDone {
		return time.Time{}, false
	}
	if !n.Review.IsZero() {
		if n.ReviewSet.IsZero() {
			return n.Review, r.Now.After(n.Review)
		}
		// The review time counts running time from when wait set it.
		due := n.Review
		if r.Stopped != nil {
			due = due.Add(r.Stopped(n.ReviewSet, r.Now))
		}
		return due, r.running(n.ReviewSet) >= n.Review.Sub(n.ReviewSet)
	}
	due := n.At.Add(r.Deadline)
	if r.Stopped != nil {
		due = due.Add(r.Stopped(n.At, r.Now))
	}
	return due, r.Deadline > 0 && r.running(n.At) > r.Deadline
}

// Group is notifications of one kind, type and stream, as one line. Its ID is
// stable while the group is open: the id of its oldest notification (for a
// stalled stream, stale:<stream>), never its position in the list, so a verb
// given --group <id> acts on this group or is refused, never on another.
type Group struct {
	ID        string   `json:"id"`
	Kind      string   `json:"kind"`
	Type      string   `json:"type"`
	Stream    string   `json:"stream,omitempty"`
	Count     int      `json:"count"`
	Size      int      `json:"size"` // the members a verb given --group acts on
	Primaries []string `json:"primaries,omitempty"`
	Notes     []string `json:"notes,omitempty"`
	Marked    bool     `json:"marked,omitempty"`
	Overdue   bool     `json:"overdue,omitempty"`
	// Quiet is a judgment group every note of which the coordinator set a review time on
	// (wait) that has not come: listed after every judgment that is not quiet.
	Quiet     bool          `json:"quiet,omitempty"`
	Oldest    time.Time     `json:"oldest"`
	Due       time.Time     `json:"due"`
	Waited    time.Duration `json:"waited_ns"`
	Decisions []string      `json:"decisions,omitempty"`
	What      string        `json:"what,omitempty"`
	Before    int           `json:"before,omitempty"`
	Suspects  []string      `json:"suspects,omitempty"` // a red branch: the suspects named
	// Commands is every decision open to the coordinator as the commands
	// that make it, filled in: the group's id, --expect and --answers.
	Commands []Command `json:"commands,omitempty"`
	// Members is every subject a verb given --group acts on: the open
	// subjects of its judgments (a stopped stream's: the cards it stopped
	// on), or the primaries of its notifications; sorted, unbounded.
	Members []string `json:"-"`
	// Needs is every need the group's blocked and missing-need judgments
	// name, in the order the notes name them, unbounded: what What
	// previews, listed whole by inbox --open.
	Needs []string `json:"-"`
	// To is who the group's happened notes are addressed to (Note.To), and
	// Hint what to do next: a group addressed to someone is shown first.
	To   string `json:"to,omitempty"`
	Hint string `json:"hint,omitempty"`
}

// StaleGroupID is the id of a stalled stream's group: the stream and the sprint's epoch,
// as every judgment id carries it, so wait takes it at the epoch it is shown at.
func StaleGroupID(stream string, epoch uint64) string {
	return fmt.Sprintf("stale:%s~%d", stream, epoch)
}

// StaleStream is the stream a stalled stream's group id names, and whether id is one.
func StaleStream(id string) (string, bool) {
	rest, ok := strings.CutPrefix(id, "stale:")
	if !ok || rest == "" {
		return "", false
	}
	return CardID(rest), true
}

// FieldStaleReview is the stream control card's field wait sets on the stream's stale
// judgment: the inbox does not show the stream stale before it.
const FieldStaleReview = "stale_review"

// Inbox groups: what happened since the cursor addressed to someone first
// (the coordinator's "the sprint is done", errata 3 amendment 6: no judgment
// waits on it, and it is the first thing the coordinator reads), then open
// judgments (marked ones, repeats and overdue, first of all, then the longest
// waiting), then streams that have not moved past their deadline, then what
// happened and what was decided since the cursor, in time order.
func Inbox(r InboxReq) []Group {
	var judg []Group
	at := map[string]int{}
	seen := map[string]bool{}
	first := map[int]Note{} // each group's oldest note: its id
	members := map[int]map[string]bool{}
	member := func(i int, s string) {
		if strings.HasPrefix(s, "stream:") || s == SprintSubject {
			return
		}
		if members[i] == nil {
			members[i] = map[string]bool{}
		}
		members[i][s] = true
	}
	loud := map[int]bool{} // a group with a note no wait has quieted
	for _, o := range r.Open {
		n := o.Note
		due, overdue := r.due(n)
		// Overdue marks a group; it does not split one, so the grouping (and
		// every group's members) is the same whatever deadline is read with.
		k := n.Type + "\x00" + n.Stream + "\x00" + boolWord(n.Marked)
		i, ok := at[k]
		if !ok {
			i = len(judg)
			at[k] = i
			judg = append(judg, Group{Kind: Judgment, Type: n.Type, Stream: n.Stream, Oldest: n.At, Due: due, Decisions: n.Decisions})
		}
		if f, ok := first[i]; !ok || n.At.Before(f.At) || n.At.Equal(f.At) && n.ID < f.ID {
			first[i] = n
		}
		g := &judg[i]
		if !due.IsZero() && (g.Due.IsZero() || due.Before(g.Due)) {
			g.Due = due
		}
		g.Marked = g.Marked || n.Marked || overdue
		g.Overdue = g.Overdue || overdue
		// quiet: a review time the coordinator set (wait) that has not come
		loud[i] = loud[i] || n.Review.IsZero() || overdue
		if n.At.Before(g.Oldest) {
			g.Oldest = n.At
		}
		g.Before = max(g.Before, n.Before)
		if g.What == "" {
			g.What = n.What
		}
		if !seen[k+o.Subject()] {
			seen[k+o.Subject()] = true
			g.Count++
			shown := []string{o.Subject()}
			if n.StreamLevel || n.SprintLevel {
				shown = n.Primaries // the cards the stream stopped on; none for the sprint
			}
			for _, p := range shown {
				member(i, p)
				if len(g.Primaries) < MaxListed && !contains(g.Primaries, p) {
					g.Primaries = append(g.Primaries, p)
				}
			}
		}
		if !contains(g.Notes, n.ID) {
			g.Notes = append(g.Notes, n.ID)
		}
		for _, x := range n.Needs {
			if !contains(g.Needs, x) {
				g.Needs = append(g.Needs, x)
			}
		}
		for _, x := range n.Suspects {
			if !contains(g.Suspects, x) {
				g.Suspects = append(g.Suspects, x)
			}
		}
		if overdue && !contains(g.Decisions, "act") {
			g.Decisions = append(append([]string{}, g.Decisions...), "act")
		}
	}
	for i := range judg {
		judg[i].ID = first[i].ID
		judg[i].Waited = r.running(judg[i].Oldest)
		judg[i].Members = slices.Sorted(maps.Keys(members[i]))
		judg[i].Size = len(judg[i].Members)
		sort.Strings(judg[i].Primaries)
		sort.Strings(judg[i].Notes)
		judg[i].Commands = commands(judg[i], first[i], r.Prefix)
		judg[i].Quiet = !loud[i]
	}
	sort.SliceStable(judg, func(i, j int) bool {
		if judg[i].Marked != judg[j].Marked {
			return judg[i].Marked
		}
		return judg[i].Oldest.Before(judg[j].Oldest)
	})
	out := judg
	for _, st := range r.Streams {
		if st.State == StreamLanded || st.Empty || st.Held || r.Now.Before(st.Quiet) || r.Stale <= 0 || st.Progress.IsZero() || r.running(st.Progress) <= r.Stale {
			continue
		}
		g := Group{ID: StaleGroupID(st.Stream, r.Epoch), Kind: Judgment, Type: NStreamStale, Stream: st.Stream, Count: 1, Marked: true, Overdue: true,
			Oldest: st.Progress, Due: st.Progress.Add(r.Stale + r.Now.Sub(st.Progress) - r.running(st.Progress)), Waited: r.running(st.Progress), Decisions: Decisions[NStreamStale],
			What: "state " + st.State + " since " + st.Since.UTC().Format(time.RFC3339)}
		g.Commands = commands(g, Note{}, r.Prefix)
		out = append(out, g)
	}
	// a judgment quieted by wait is listed after every one that is not, for its
	// whole period, so it does not sit at the top of every read (section 8)
	sort.SliceStable(out, func(i, j int) bool { return !out[i].Quiet && out[j].Quiet })
	var rest []Group
	at = map[string]int{}
	restMembers := map[int]map[string]bool{}
	for _, n := range r.Recent {
		if n.Kind == Judgment || n.Kind == Acknowledged || n.Type == NTickEnd {
			continue // judgments are shown while open, above; an acknowledgement is its decided note; a tick end wakes, it says nothing
		}
		k := n.Kind + "\x00" + n.Type + "\x00" + n.Stream + "\x00" + n.To
		i, ok := at[k]
		if !ok {
			i = len(rest)
			at[k] = i
			rest = append(rest, Group{ID: n.ID, Kind: n.Kind, Type: n.Type, Stream: n.Stream, Oldest: n.At, What: n.What, To: n.To, Hint: n.Hint})
			restMembers[i] = map[string]bool{}
		}
		g := &rest[i]
		if n.To != "" {
			// the latest words of a note addressed to someone are the ones
			// that stand
			g.What, g.Hint = n.What, n.Hint
		}
		c := n.Count
		if c == 0 && len(n.Primaries) == 0 {
			c = 1
		}
		g.Count += c
		for _, p := range n.Primaries {
			restMembers[i][p] = true
			if len(g.Primaries) < MaxListed && !contains(g.Primaries, p) {
				g.Primaries = append(g.Primaries, p)
			}
		}
		g.Notes = append(g.Notes, n.ID)
	}
	var done, top, other []Group
	for i := range rest {
		rest[i].Members = slices.Sorted(maps.Keys(restMembers[i]))
		rest[i].Size = len(rest[i].Members)
		switch {
		case rest[i].Type == NSprintDone:
			done = append(done, rest[i])
		case rest[i].To != "":
			top = append(top, rest[i])
		default:
			other = append(other, rest[i])
		}
	}
	// the sprint done first, then the judgments, then the other notes to the
	// coordinator (ready to merge, the tick-end line), then the rest
	return append(append(append(done, out...), top...), other...)
}

// FindGroup is the group of the id, if it is in the inbox.
func FindGroup(groups []Group, id string) (Group, bool) {
	for _, g := range groups {
		if g.ID == id {
			return g, true
		}
	}
	return Group{}, false
}

// Command is one decision open to the coordinator as the commands that make
// it, in order, one per line, ready to copy.
type Command struct {
	Decision string   `json:"decision"`
	Lines    []string `json:"lines"`
}

// MaxLook bounds the cards "look" lists; past it, the command that lists the
// whole group.
const MaxLook = 5

// A placeholder is free text only the coordinator can give.
const (
	whyText  = "'<why>'"
	fixText  = "'<fix>'"
	didText  = "'<what you did>'"
	noneText = "'<why nothing is to be done>'"
)

// NoteCommands is one open judgment's decisions as commands, the members it
// names its subjects: as the inbox prints them for a group of one.
func NoteCommands(n Note, members []string) []Command {
	g := Group{ID: n.ID, Kind: Judgment, Type: n.Type, Stream: n.Stream, Size: len(members), Notes: []string{n.ID}, Members: members, Decisions: n.Decisions}
	return commands(g, n, "")
}

// commands is the group's decisions as commands, from its oldest note (a
// stopped stream's card and the card it needs, by their named fields). A
// decision about cards takes the group with its size and the notifications it
// answers; a decision about a stopped stream names the cards and resumes the
// stream.
func commands(g Group, first Note, prefix string) []Command {
	const cmd = "nova-sprint "
	grp := " --group " + g.ID + " --expect " + itoa(g.Size)
	ans := " --answers " + strings.Join(g.Notes, ",")
	s := g.Stream
	resume := func(did string) string { return cmd + "resume --stream " + s + " --did " + did + ans }
	look := func() []string {
		if len(g.Members) == 0 {
			return []string{cmd + "inbox --open " + g.ID}
		}
		var out []string
		for i, m := range g.Members {
			if i == MaxLook {
				return append(out, cmd+"inbox --open "+g.ID)
			}
			out = append(out, cmd+"card "+m)
		}
		return out
	}
	// The cards a decision names are the note's named ones (the card the
	// stream stopped on, the card a cross stop needs), never positions in its
	// set of primaries.
	card, other := first.Card, first.Other
	suspects, listBatch := "'<suspect>'", []string{cmd + "queue --stream " + s + " --max " + itoa(g.Size)}
	if len(g.Suspects) > 0 {
		suspects, listBatch = strings.Join(g.Suspects, " "), nil
	}
	var out []Command
	add := func(d string, lines ...string) { out = append(out, Command{Decision: d, Lines: lines}) }
	for _, d := range g.Decisions {
		switch {
		case d == RepeatDecision:
			add(d, look()...)
		case d == "act" && first.StreamLevel:
			add(d, cmd+"wait "+first.ID+" --for 30m")
		case d == "act" && contains(g.Decisions, "ack"):
			add(d, cmd+"ack "+strings.Join(g.Notes, ",")+" --reason "+noneText, cmd+"wait "+g.ID+" --for 30m")
		case d == "act":
			add(d, cmd+"wait "+g.ID+" --for 30m")
		case g.Type == NSprintDone:
			switch d {
			case "clear":
				add(d, cmd+"clear --confirm "+Names{Prefix: prefix}.View())
			case "add":
				add(d, cmd+"add --stream '<stream>' --count '<n>' --brief '<brief>'")
			}
		case g.Type == NSentinelReached:
			ids := strings.Join(g.Members, " ")
			switch d {
			case "release":
				add(d, cmd+"release "+ids+" --reason '<what you looked at and found>'"+ans)
			case "do more before going on":
				add(d, cmd+"add --stream "+s+" --before "+card+" '<new id>' --brief '<brief>'")
			case "drop":
				add(d, cmd+"drop "+ids+" --reason "+whyText+ans)
			}
		case g.Type == NStreamStale:
			add(d, cmd+"where", cmd+"queue --stream "+s)
		case g.Type == NConflict:
			switch d {
			case "resolve and resume":
				add(d, resume(didText))
			case "rework":
				add(d, cmd+"return "+card+" --reason conflict", cmd+"rework "+card+" --fix "+fixText, resume("'returned "+card+" for rework'"))
			case "drop":
				add(d, cmd+"drop "+card+" --reason "+whyText+ans, resume("'dropped "+card+"'"))
			}
		case g.Type == NRed:
			ret := cmd + "return " + suspects + " --reason 'suspect of the red batch'" + ans
			switch d {
			case "take the suspect off and resume":
				add(d, append(listBatch, ret, resume("'returned "+strings.Trim(suspects, "'")+"'"))...)
			case "rework the suspect":
				add(d, append(listBatch, ret, cmd+"rework "+suspects+" --fix "+fixText, resume("'returned "+strings.Trim(suspects, "'")+" for rework'"))...)
			}
		case g.Type == NCross:
			switch d {
			case "rank that card first":
				add(d, cmd+"rank "+other+" --first"+ans)
			case "wait":
				add(d, cmd+"wait "+first.ID+" --for 30m")
			case "look at both":
				add(d, cmd+"card "+card, cmd+"card "+other)
			case "return":
				add(d, cmd+"return "+card+" --reason "+whyText+ans, resume("'returned "+card+"'"))
			case "drop":
				add(d, cmd+"drop "+card+" --reason "+whyText+ans, resume("'dropped "+card+"'"))
			}
		case g.Type == NRejected:
			switch d {
			case "resume":
				add(d, resume(didText))
			case "return":
				add(d, cmd+"return"+grp+" --reason "+whyText+ans, resume("'returned the batch'"))
			case "drop":
				add(d, cmd+"drop"+grp+" --reason "+whyText+ans, resume("'dropped the batch'"))
			}
		case d == "rework with the finding" || d == "rework with a fix" && g.Type == NWorkFailed:
			add(d, cmd+"rework"+grp+ans) // each takes its own finding or report
		case d == "rework with a fix" || d == "rework":
			add(d, cmd+"rework"+grp+" --fix "+fixText+ans)
		case d == "ask":
			add(d, cmd+"ask"+grp+ans)
		case d == "accept":
			add(d, cmd+"accept"+grp+ans)
		case d == "ask another reader":
			add(d, cmd+"ask"+grp+" --another"+ans)
		case d == "drop":
			add(d, cmd+"drop"+grp+" --reason "+whyText+ans)
		case d == "return":
			add(d, cmd+"return"+grp+" --reason "+whyText+ans)
		case d == "look":
			add(d, look()...)
		case d == "check":
			add(d, cmd+"check")
		case d == "wait":
			add(d, cmd+"wait "+cmp.Or(first.ID, g.ID)+" --for 30m") // a stale stream's group has no note: its id
		case d == "look at the card":
			add(d, look()...)
		case d == "repair":
			add(d, cmd+"repair")
		case d == "reader add":
			add(d, cmd+"reader add '<reader>'")
		case d == "reader up":
			add(d, cmd+"reader up '<reader>'")
		case d == "route add":
			// the tier's route is config: nova-config's, applied to the store
			tier := strings.TrimPrefix(g.Stream, "tier:")
			add(d, "nova-config route add '<name>' --tier "+tier+" --provider '<provider>' --model '<model>' --deadline '<seconds>'", "nova-config apply")
		case d == "fleet up":
			add(d, cmd+"fleet up '<member>'")
		case d == "fleet beat":
			add(d, cmd+"fleet beat '<member>'")
		case d == "ask --another":
			add(d, cmd+"ask"+grp+" --another"+ans)
		case strings.HasPrefix(d, "merge --stream "):
			// a merge step is a report: it names its epoch, the judgment's
			add(d, cmd+d+" --epoch "+strconv.FormatUint(IDEpoch(g.ID), 10))
		case strings.HasPrefix(d, "fleet down ") || strings.HasPrefix(d, "goal "):
			add(d, cmd+d)
		case d == "ack":
			add(d, cmd+"ack "+strings.Join(g.Notes, ",")+" --reason "+noneText)
		case d == "resume" && s != "":
			add(d, resume(didText))
		case d == "release":
			add(d, cmd+"release "+strings.Join(g.Members, " ")+" --reason '<what you looked at and found>'"+ans)
		}
	}
	if g.Type == NRed {
		add("resume with what you did", resume(didText))
	}
	return out
}
